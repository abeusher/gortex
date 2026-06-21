package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// typedAsTargets collects every EdgeTypedAs `To` target whose `From` matches
// owner (or any owner when owner == "").
func typedAsTargets(edges []*graph.Edge, owner string) map[string]bool {
	out := map[string]bool{}
	for _, e := range edges {
		if e.Kind != graph.EdgeTypedAs {
			continue
		}
		if owner != "" && e.From != owner {
			continue
		}
		out[e.To] = true
	}
	return out
}

// TestDartTypeUse_LocalVariable is the named acceptance test: a typed local
// `HttpResponse resp = get();` must emit an EdgeTypedAs to
// unresolved::HttpResponse attributed to the enclosing function, and a `int`
// local must emit no type-use edge.
func TestDartTypeUse_LocalVariable(t *testing.T) {
	src := []byte(`class Api {
  void load() {
    HttpResponse resp = get();
    int count = 0;
  }
}
`)
	res, err := NewDartExtractor().Extract("api.dart", src)
	require.NoError(t, err)

	// EdgeTypedAs to HttpResponse, attributed to the enclosing method.
	require.True(t,
		hasEdgeBetween(res.Edges, graph.EdgeTypedAs, "api.dart::Api.load", "unresolved::HttpResponse"),
		"typed local `HttpResponse resp` should emit EdgeTypedAs to the enclosing method; edges=%v", res.Edges)

	// Origin must be OriginASTInferred.
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::HttpResponse" {
			assert.Equal(t, graph.OriginASTInferred, e.Origin)
		}
	}

	// Primitive `int` must never produce a type-use edge.
	all := typedAsTargets(res.Edges, "")
	assert.NotContains(t, all, "unresolved::int", "primitive int must not emit a type-use edge")
}

// TestDartTypeUse_FieldParamReturn covers the three remaining positions:
// class fields, parameter types, and return types.
func TestDartTypeUse_FieldParamReturn(t *testing.T) {
	src := []byte(`class Service {
  final HttpClient client;
  Repository repo = makeRepo();

  Future<Response> fetch(Request req, [Logger? log]) async {
    return get();
  }
}

User build(Config cfg) {
  return User();
}
`)
	res, err := NewDartExtractor().Extract("svc.dart", src)
	require.NoError(t, err)

	// Fields attribute to the file node.
	fileTargets := typedAsTargets(res.Edges, "svc.dart")
	assert.True(t, fileTargets["unresolved::HttpClient"], "field type HttpClient")
	assert.True(t, fileTargets["unresolved::Repository"], "field type Repository")

	// Method return + param types attribute to the method node.
	methodTargets := typedAsTargets(res.Edges, "svc.dart::Service.fetch")
	assert.True(t, methodTargets["unresolved::Future"], "return head Future")
	assert.True(t, methodTargets["unresolved::Response"], "return generic arg Response")
	assert.True(t, methodTargets["unresolved::Request"], "param type Request")
	assert.True(t, methodTargets["unresolved::Logger"], "nullable param type Logger (? stripped)")

	// Top-level function return + param attribute to the function node.
	fnTargets := typedAsTargets(res.Edges, "svc.dart::build")
	assert.True(t, fnTargets["unresolved::User"], "function return type User")
	assert.True(t, fnTargets["unresolved::Config"], "function param type Config")

	// No primitive / builtin leaked anywhere.
	all := typedAsTargets(res.Edges, "")
	for _, prim := range []string{"int", "double", "num", "bool", "String", "void", "dynamic", "Object", "var"} {
		assert.NotContains(t, all, "unresolved::"+prim, "primitive %s must not emit a type-use edge", prim)
	}
}

// TestDartTypeUse_GenericContainersUnwrapped verifies List<Foo> / Map<K,V>
// unwrap to every named element type, and `var` / `final` (inferred) locals
// emit nothing.
func TestDartTypeUse_GenericContainersUnwrapped(t *testing.T) {
	src := []byte(`class Store {
  List<User> users = [];
  Map<String, Token> tokens = {};

  void run() {
    var anything = compute();
    final inferred = compute();
    Set<Account> accounts = {};
  }
}
`)
	res, err := NewDartExtractor().Extract("store.dart", src)
	require.NoError(t, err)

	all := typedAsTargets(res.Edges, "")
	assert.True(t, all["unresolved::User"], "List<User> unwraps to User")
	assert.True(t, all["unresolved::Token"], "Map<String, Token> unwraps to Token")
	assert.True(t, all["unresolved::Account"], "Set<Account> unwraps to Account")

	// String is a primitive key — never emitted.
	assert.NotContains(t, all, "unresolved::String")

	// Inferred `var` / `final` locals carry no type → no edge for them.
	// (compute is a call, captured as EdgeCalls, not EdgeTypedAs.)
	runTargets := typedAsTargets(res.Edges, "store.dart::Store.run")
	assert.True(t, runTargets["unresolved::Account"], "typed local Set<Account> attributes to run")
}

// TestDartTypeUse_NoDuplicateEdges guards against double-emitting the same
// (owner, type) pair when a type appears in multiple positions of one owner.
func TestDartTypeUse_NoDuplicateEdges(t *testing.T) {
	src := []byte(`class C {
  void f(Widget a, Widget b) {
    Widget local = a;
  }
}
`)
	res, err := NewDartExtractor().Extract("c.dart", src)
	require.NoError(t, err)

	count := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeTypedAs && e.From == "c.dart::C.f" && e.To == "unresolved::Widget" {
			count++
		}
	}
	assert.Equal(t, 1, count, "Widget used in two params + a local must emit exactly one TypedAs edge for the method")
}
