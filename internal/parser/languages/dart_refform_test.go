package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

// dartRefEdge reports whether edges contains an edge of kind to
// "unresolved::"+typeName whose Meta["ref_context"] equals refContext (pass
// "" to require no ref_context, e.g. for EdgeInstantiates).
func dartRefEdge(edges []*graph.Edge, kind graph.EdgeKind, typeName, refContext string) bool {
	want := "unresolved::" + typeName
	for _, e := range edges {
		if e.Kind != kind || e.To != want {
			continue
		}
		got := ""
		if e.Meta != nil {
			if v, ok := e.Meta["ref_context"].(string); ok {
				got = v
			}
		}
		if got == refContext {
			return true
		}
	}
	return false
}

// countRefEdge counts edges of kind to "unresolved::"+typeName.
func countRefEdge(edges []*graph.Edge, kind graph.EdgeKind, typeName string) int {
	want := "unresolved::" + typeName
	n := 0
	for _, e := range edges {
		if e.Kind == kind && e.To == want {
			n++
		}
	}
	return n
}

// TestDartRefForm_Instantiation covers `Foo()` (unadorned), `new Foo()`, and
// `const Foo.named()`. For a local type, extractCalls owns the bare-call
// instantiation; new / const forms are owned by this pass.
func TestDartRefForm_Instantiation(t *testing.T) {
	src := []byte(`class Widget {
  Widget.named();
}

void run() {
  var a = Widget();
  var b = new Widget();
  var c = const Widget.named();
  foo();
}

void foo() {}
`)
	res, err := NewDartExtractor().Extract("w.dart", src)
	require.NoError(t, err)

	// `Widget()`, `new Widget()`, `const Widget.named()` all construct Widget.
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "Widget", ""),
		"Widget construction should emit EdgeInstantiates; edges=%v", res.Edges)

	// Origin is OriginASTResolved (guard-survival + consistency).
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeInstantiates && e.To == "unresolved::Widget" {
			assert.Equal(t, graph.OriginASTResolved, e.Origin)
		}
	}

	// A lowercase free-function call `foo()` is NOT a construction.
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "foo", ""),
		"lowercase call foo() must not be an instantiation")

	// `const Widget.named()` references Widget once as instantiation; the bare
	// `Widget()` is emitted by extractCalls (local type). This pass must not
	// double-emit the bare local construction.
	assert.GreaterOrEqual(t, countRefEdge(res.Edges, graph.EdgeInstantiates, "Widget"), 1)
}

// TestDartRefForm_InstantiationExternalType verifies an unadorned `Client()`
// for a type NOT defined in the file (imported) surfaces as an instantiation,
// while a lowercase callee does not.
func TestDartRefForm_InstantiationExternalType(t *testing.T) {
	src := []byte(`void run() {
  var c = Client();
  var r = Response();
  helper();
}
`)
	res, err := NewDartExtractor().Extract("api.dart", src)
	require.NoError(t, err)

	assert.True(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "Client", ""),
		"imported Client() should emit EdgeInstantiates; edges=%v", res.Edges)
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "Response", ""),
		"imported Response() should emit EdgeInstantiates")
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "helper", ""),
		"lowercase call helper() must not be an instantiation")
}

// TestDartRefForm_Inheritance covers extends / with / implements.
func TestDartRefForm_Inheritance(t *testing.T) {
	src := []byte(`class Base {}
mixin M {}
abstract class I {}

class X extends Base with M implements I {}
`)
	res, err := NewDartExtractor().Extract("x.dart", src)
	require.NoError(t, err)

	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Base", graph.RefContextInherit),
		"extends Base should emit inherit reference; edges=%v", res.Edges)
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "M", graph.RefContextInherit),
		"with M should emit inherit reference")
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "I", graph.RefContextInherit),
		"implements I should emit inherit reference")

	// Inherit references attribute to the file node (the class line is not
	// inside a function range) and ride OriginASTResolved.
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && e.Meta != nil &&
			e.Meta["ref_context"] == graph.RefContextInherit {
			assert.Equal(t, graph.OriginASTResolved, e.Origin)
			assert.Equal(t, "x.dart", e.From)
		}
	}
}

// TestDartRefForm_CastAndTypeTest covers `x as Foo`, `x is Foo`, `x is! Foo`.
func TestDartRefForm_CastAndTypeTest(t *testing.T) {
	src := []byte(`void run(Object o) {
  var a = o as Account;
  if (o is User) {}
  if (o is! Token) {}
}
`)
	res, err := NewDartExtractor().Extract("c.dart", src)
	require.NoError(t, err)

	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Account", graph.RefContextCast),
		"`o as Account` should emit cast reference; edges=%v", res.Edges)
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "User", graph.RefContextCast),
		"`o is User` should emit cast reference")
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Token", graph.RefContextCast),
		"`o is! Token` should emit cast reference")

	// Casts inside run() attribute to the enclosing method.
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && e.Meta != nil &&
			e.Meta["ref_context"] == graph.RefContextCast {
			assert.Equal(t, "c.dart::run", e.From)
		}
	}
}

// TestDartRefForm_StaticAccess covers `Foo.constant`, `Foo.empty()`,
// `Foo.staticMethod()`.
func TestDartRefForm_StaticAccess(t *testing.T) {
	src := []byte(`void run() {
  var a = Colors.red;
  var b = Logger.empty();
  var c = Math.max(1, 2);
}
`)
	res, err := NewDartExtractor().Extract("s.dart", src)
	require.NoError(t, err)

	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Colors", graph.RefContextStaticAccess),
		"`Colors.red` should emit static_access reference; edges=%v", res.Edges)
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Logger", graph.RefContextStaticAccess),
		"`Logger.empty()` should emit static_access reference")
	assert.True(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Math", graph.RefContextStaticAccess),
		"`Math.max(...)` should emit static_access reference")
}

// TestDartRefForm_Negatives verifies the false-positive guards: a lowercase
// free call, a lowercase instance call, `this.x`, a chained access, and a
// primitive emit nothing on the reference surface.
func TestDartRefForm_Negatives(t *testing.T) {
	src := []byte(`class C {
  var x = 0;
  void run() {
    foo();
    obj.method();
    this.x = 1;
    one.Two.three;
    int n = 5;
  }
}
`)
	res, err := NewDartExtractor().Extract("n.dart", src)
	require.NoError(t, err)

	// Lowercase free call → not an instantiation.
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeInstantiates, "foo", ""),
		"foo() must not be an instantiation")

	// Lowercase instance call → not a static access (head is lowercase `obj`).
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeReferences, "obj", graph.RefContextStaticAccess),
		"obj.method() must not be a static access")
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeReferences, "method", graph.RefContextStaticAccess),
		"method must not be referenced as a static access")

	// `this.x` → no reference (`this` is filtered by capitalization gate).
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeReferences, "this", graph.RefContextStaticAccess),
		"this.x must not emit a static access")

	// Chained `one.Two.three`: the head is lowercase `one`, so even though
	// `Two` is capitalized it is NOT a static-access head (it lives inside a
	// selector). Two must not be referenced as a static access.
	assert.False(t, dartRefEdge(res.Edges, graph.EdgeReferences, "Two", graph.RefContextStaticAccess),
		"chained one.Two.three must not treat Two as a static-access head")

	// Primitive `int` emits nothing on any reference surface.
	for _, e := range res.Edges {
		assert.NotEqual(t, "unresolved::int", e.To, "primitive int must not appear on a reference edge")
	}
}

// TestDartRefForm_NoDuplicateEdges guards the per-(owner,type,line,ref_context)
// dedup: the same static access twice on one line emits a single edge.
func TestDartRefForm_NoDuplicateEdges(t *testing.T) {
	src := []byte(`void run() {
  var a = Config.a + Config.b;
}
`)
	res, err := NewDartExtractor().Extract("d.dart", src)
	require.NoError(t, err)

	// Two `Config.` accesses on the same line dedup to one static_access edge.
	n := 0
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && e.To == "unresolved::Config" &&
			e.Meta != nil && e.Meta["ref_context"] == graph.RefContextStaticAccess {
			n++
		}
	}
	assert.Equal(t, 1, n, "two Config. accesses on one line must dedup to one static_access edge")
}
