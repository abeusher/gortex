package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestCSharpTypeUse_LocalAnnotation verifies that a type used only in a
// local-variable annotation inside a method body emits an EdgeTypedAs
// attributed to the enclosing method — the gap this work closes: such a
// type previously seeded only tenv and produced no reference, so
// find_usages missed it without an LSP.
func TestCSharpTypeUse_LocalAnnotation(t *testing.T) {
	src := `public class Svc {
	public void Handle() {
		HttpResponse resp = Get();
		int n = Count();
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)

	typed := edgesByKind(edges, graph.EdgeTypedAs)

	const methodID = "x/Svc.cs::Svc.Handle"
	foundFromMethod := false
	for _, e := range typed {
		if e.To != "unresolved::HttpResponse" {
			continue
		}
		if e.From != methodID {
			t.Errorf("expected EdgeTypedAs → HttpResponse from enclosing method %q, got From=%q", methodID, e.From)
		}
		if e.Origin != graph.OriginASTInferred {
			t.Errorf("expected Origin=OriginASTInferred, got %v", e.Origin)
		}
		foundFromMethod = true
	}
	if !foundFromMethod {
		t.Errorf("expected EdgeTypedAs → unresolved::HttpResponse from a local annotation; got %v", edgeTargets(typed))
	}

	// A primitive local annotation (`int n = ...`) must NOT emit a
	// type-use edge.
	for _, e := range typed {
		if e.To == "unresolved::int" {
			t.Errorf("primitive int must not emit EdgeTypedAs; got %v", edgeTargets(typed))
		}
	}
}

// TestCSharpTypeUse_VarLocalNoEdge confirms that `var x = ...` — which
// has no explicit annotation — emits no spurious unresolved::var edge.
func TestCSharpTypeUse_VarLocalNoEdge(t *testing.T) {
	src := `public class Svc {
	public void Handle() {
		var u = new User();
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)
	for _, e := range edgesByKind(edges, graph.EdgeTypedAs) {
		if e.To == "unresolved::var" {
			t.Fatalf("`var` must not emit an EdgeTypedAs to unresolved::var")
		}
	}
}

// TestCSharpTypeUse_NullableAndGeneric verifies nullable (`Session?`) and
// container-generic (`List<Order>`) local annotations canonicalise to
// their bare named type.
func TestCSharpTypeUse_NullableAndGeneric(t *testing.T) {
	src := `using System.Collections.Generic;

public class Svc {
	public void Handle() {
		Session? s = Resolve();
		List<Order> orders = Load();
	}
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)
	typed := edgesByKind(edges, graph.EdgeTypedAs)
	want := map[string]bool{"unresolved::Session": false, "unresolved::Order": false}
	for _, e := range typed {
		if _, ok := want[e.To]; ok {
			want[e.To] = true
		}
	}
	for tgt, found := range want {
		if !found {
			t.Errorf("expected EdgeTypedAs → %s (nullable/generic unwrapped); got %v", tgt, edgeTargets(typed))
		}
	}
}

// TestCSharpTypeUse_FieldAndProperty verifies that a type used only as a
// field's or property's declared type emits an EdgeTypedAs from the
// member node.
func TestCSharpTypeUse_FieldAndProperty(t *testing.T) {
	src := `public class Svc {
	private Session _s;
	private int _count;
	public Repository Repo { get; set; }
}
`
	_, edges := runCSharpExtract(t, "x/Svc.cs", src)
	typed := edgesByKind(edges, graph.EdgeTypedAs)

	type want struct {
		from, to string
	}
	wants := []want{
		{"x/Svc.cs::Svc._s", "unresolved::Session"},
		{"x/Svc.cs::Svc.Repo", "unresolved::Repository"},
	}
	for _, w := range wants {
		found := false
		for _, e := range typed {
			if e.From == w.from && e.To == w.to {
				found = true
			}
		}
		if !found {
			t.Errorf("expected EdgeTypedAs %s → %s; got %v", w.from, w.to, edgeTargets(typed))
		}
	}

	// The primitive field `_count` (int) must not emit a type-use edge.
	for _, e := range typed {
		if e.To == "unresolved::int" {
			t.Errorf("primitive int field must not emit EdgeTypedAs; got %v", edgeTargets(typed))
		}
	}
}
