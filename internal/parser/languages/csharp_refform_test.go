package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// refEdge reports whether edges contains an edge of kind k from `from` to
// `unresolved::<to>` whose Meta["ref_context"] equals refContext ("" to
// ignore the context). Used to assert the C# reference-form edges
// emitCSharpReferenceForms produces.
func hasCSharpRefEdge(edges []*graph.Edge, from, to string, k graph.EdgeKind, refContext string) bool {
	for _, e := range edges {
		if e.Kind != k || e.From != from || e.To != "unresolved::"+to {
			continue
		}
		if refContext == "" {
			return true
		}
		if rc, _ := e.Meta["ref_context"].(string); rc == refContext {
			return true
		}
	}
	return false
}

// refEdgeOrigin returns the Origin of the first matching edge, or "".
func csharpRefEdgeOrigin(edges []*graph.Edge, from, to string, k graph.EdgeKind) string {
	for _, e := range edges {
		if e.Kind == k && e.From == from && e.To == "unresolved::"+to {
			return string(e.Origin)
		}
	}
	return ""
}

const csharpRefMethodID = "x/Svc.cs::Svc.Handle"

// TestCSharpRefForm_Instantiation verifies `new Foo(...)`, `new Foo[]`,
// and `new Foo { ... }` each emit an EdgeInstantiates from the enclosing
// method to the constructed type.
func TestCSharpRefForm_Instantiation(t *testing.T) {
	src := `public class Svc {
	public void Handle() {
		var a = new RestClient();
		var b = new RestRequest[3];
		var c = new RestResponse { Code = 200 };
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	for _, tgt := range []string{"RestClient", "RestRequest", "RestResponse"} {
		if !hasCSharpRefEdge(edges, csharpRefMethodID, tgt, graph.EdgeInstantiates, "") {
			t.Errorf("expected EdgeInstantiates %s → %s", csharpRefMethodID, tgt)
		}
	}
}

// TestCSharpRefForm_Inheritance verifies `class X : Base, IFoo` still
// produces the inheritance edges — emitted by the existing
// emitCSharpBaseList pass (EdgeExtends for the base class, EdgeImplements
// for the interface), which the reference-form pass deliberately does not
// double-emit.
func TestCSharpRefForm_Inheritance(t *testing.T) {
	src := `public class Derived : BaseSvc, IHandler {
}
`
	_, edges := runCSharpExtract(t, "x/Derived.cs", src)
	const typeID = "x/Derived.cs::Derived"

	if !hasCSharpRefEdge(edges, typeID, "BaseSvc", graph.EdgeExtends, "") {
		t.Errorf("expected EdgeExtends %s → BaseSvc", typeID)
	}
	if !hasCSharpRefEdge(edges, typeID, "IHandler", graph.EdgeImplements, "") {
		t.Errorf("expected EdgeImplements %s → IHandler", typeID)
	}
	// The reference-form pass must NOT also emit a base-type EdgeReferences
	// — that would double-count the inheritance edge.
	if hasCSharpRefEdge(edges, typeID, "BaseSvc", graph.EdgeReferences, "") {
		t.Errorf("inheritance must not double-emit EdgeReferences → BaseSvc")
	}
}

// TestCSharpRefForm_CastAndTypeTest verifies `(Foo)x`, `x is Foo` (with
// and without a binding), and `x as Foo` each emit an EdgeReferences with
// ref_context "cast".
func TestCSharpRefForm_CastAndTypeTest(t *testing.T) {
	src := `public class Svc {
	public void Handle(object x) {
		var c = (RestClient)x;
		if (x is RestRequest) { }
		if (x is RestResponse r) { }
		var d = x as RestError;
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	for _, tgt := range []string{"RestClient", "RestRequest", "RestResponse", "RestError"} {
		if !hasCSharpRefEdge(edges, csharpRefMethodID, tgt, graph.EdgeReferences, "cast") {
			t.Errorf("expected EdgeReferences (cast) %s → %s", csharpRefMethodID, tgt)
		}
	}
}

// TestCSharpRefForm_StaticAccess verifies static / const member access
// (`Foo.Empty`, `Foo.Method()`), `typeof(Foo)`, and `nameof(Foo)` emit an
// EdgeReferences with ref_context "static_access".
func TestCSharpRefForm_StaticAccess(t *testing.T) {
	src := `public class Svc {
	public void Handle() {
		var e = RestClient.Empty;
		RestRequest.DoStatic();
		var t = typeof(RestResponse);
		var n = nameof(RestError);
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	for _, tgt := range []string{"RestClient", "RestRequest", "RestResponse", "RestError"} {
		if !hasCSharpRefEdge(edges, csharpRefMethodID, tgt, graph.EdgeReferences, "static_access") {
			t.Errorf("expected EdgeReferences (static_access) %s → %s", csharpRefMethodID, tgt)
		}
	}
}

// TestCSharpRefForm_Attribute verifies an attribute `[Foo]` / `[Foo(...)]`
// emits an EdgeReferences with ref_context "attribute" to the attribute
// type. The class-level attribute attributes to the file node (no
// enclosing function), so the From is the file ID.
func TestCSharpRefForm_Attribute(t *testing.T) {
	src := `[Route("api")]
public class Svc {
	[HttpGet]
	public void Handle() { }
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	// Class attribute — no enclosing function → file node owner.
	if !hasCSharpRefEdge(edges, "x/Svc.cs", "Route", graph.EdgeReferences, "attribute") {
		t.Errorf("expected EdgeReferences (attribute) from file → Route")
	}
	// Method attribute encloses the method body line range.
	if !hasCSharpRefEdge(edges, csharpRefMethodID, "HttpGet", graph.EdgeReferences, "attribute") {
		t.Errorf("expected EdgeReferences (attribute) %s → HttpGet", csharpRefMethodID)
	}
}

// TestCSharpRefForm_OriginASTResolved verifies the structural reference
// edges (cast / static_access / attribute) ride OriginASTResolved — the
// load-bearing tier that keeps the cross-package guard from reverting them
// to their unresolved placeholder.
func TestCSharpRefForm_OriginASTResolved(t *testing.T) {
	src := `public class Svc {
	public void Handle(object x) {
		var c = (RestClient)x;
		var e = RestRequest.Empty;
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	if got := csharpRefEdgeOrigin(edges, csharpRefMethodID, "RestClient", graph.EdgeReferences); got != graph.OriginASTResolved {
		t.Errorf("cast edge Origin = %q, want %q", got, graph.OriginASTResolved)
	}
	if got := csharpRefEdgeOrigin(edges, csharpRefMethodID, "RestRequest", graph.EdgeReferences); got != graph.OriginASTResolved {
		t.Errorf("static_access edge Origin = %q, want %q", got, graph.OriginASTResolved)
	}
	if got := csharpRefEdgeOrigin(edges, csharpRefMethodID, "RestClient", graph.EdgeInstantiates); got != "" && got != graph.OriginASTResolved {
		t.Errorf("instantiate edge Origin = %q, want OriginASTResolved or unset", got)
	}
}

// TestCSharpRefForm_Negatives confirms the scope guards: a lowercase free
// call (`bar()`), an instance member access (`this.x`, `local.Foo`), a
// primitive (`int`), and `var` produce no reference-form edge.
func TestCSharpRefForm_Negatives(t *testing.T) {
	src := `public class Svc {
	private int _n;
	public void Handle(object obj) {
		bar();
		this.Field = 1;
		var svc = obj;
		svc.Foo();
		int z = 3;
		var v = 4;
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	for _, e := range edges {
		if e.Kind != graph.EdgeReferences && e.Kind != graph.EdgeInstantiates {
			continue
		}
		rc, _ := e.Meta["ref_context"].(string)
		// No reference-form edge should target a lowercase / primitive
		// name, the `var` keyword, or the bare instance-field name.
		switch e.To {
		case "unresolved::bar", "unresolved::this", "unresolved::svc",
			"unresolved::obj", "unresolved::int", "unresolved::var",
			"unresolved::Field", "unresolved::Foo", "unresolved::z",
			"unresolved::v":
			t.Errorf("reference form must not emit %s (kind=%s ref_context=%q)", e.To, e.Kind, rc)
		}
	}
}
