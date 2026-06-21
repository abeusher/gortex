package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// swiftTypedAsTargets collects the To target of every EdgeTypedAs edge.
func swiftTypedAsTargets(edges []*graph.Edge) map[string]bool {
	out := map[string]bool{}
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs {
			out[e.To] = true
		}
	}
	return out
}

// hasTypedAsFrom reports whether an EdgeTypedAs edge exists from `from`
// to `unresolved::`+typeName, stamped with the AST-inferred origin.
func hasTypedAsFrom(edges []*graph.Edge, from, typeName string) bool {
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.From == from &&
			e.To == "unresolved::"+typeName && e.Origin == graph.OriginASTInferred {
			return true
		}
	}
	return false
}

func TestSwiftExtractor_LocalVariableTypeUse(t *testing.T) {
	// A type used ONLY in a local annotation must produce a usage edge so
	// find_usages lands it without a language server. A primitive (Int)
	// must NOT.
	src := []byte(`func handle() {
    let resp: HttpResponse = makeResponse()
    let count: Int = 0
}
`)
	res, err := NewSwiftExtractor().Extract("h.swift", src)
	if err != nil {
		t.Fatal(err)
	}
	targets := swiftTypedAsTargets(res.Edges)

	if !targets["unresolved::HttpResponse"] {
		t.Fatalf("expected EdgeTypedAs to unresolved::HttpResponse for `let resp: HttpResponse`; got %v", targets)
	}
	if !hasTypedAsFrom(res.Edges, "h.swift::resp", "HttpResponse") {
		t.Errorf("HttpResponse type-use edge should originate from the resp binding with OriginASTInferred; edges=%v", res.Edges)
	}
	if targets["unresolved::Int"] {
		t.Errorf("primitive `Int` must NOT produce a type-use edge; got %v", targets)
	}
}

func TestSwiftExtractor_PropertyTypeUse(t *testing.T) {
	// Stored-property annotation, including optional / array / dictionary
	// / generic sugar — each names a user type that should land an edge,
	// while the primitive component (Int key) is dropped.
	src := []byte(`class Store {
    let resp: HttpResponse = HttpResponse()
    var widgets: [Widget] = []
    var maybe: Thing? = nil
    var table: [Int: Record] = [:]
    var port: Int = 0
}
`)
	res, err := NewSwiftExtractor().Extract("s.swift", src)
	if err != nil {
		t.Fatal(err)
	}
	targets := swiftTypedAsTargets(res.Edges)

	for _, want := range []string{"HttpResponse", "Widget", "Thing", "Record"} {
		if !targets["unresolved::"+want] {
			t.Errorf("expected EdgeTypedAs to unresolved::%s; got %v", want, targets)
		}
	}
	if !hasTypedAsFrom(res.Edges, "s.swift::Store.resp", "HttpResponse") {
		t.Errorf("property type-use edge should originate from Store.resp; edges=%v", res.Edges)
	}
	if targets["unresolved::Int"] {
		t.Errorf("primitive `Int` (dictionary key + port) must NOT produce a type-use edge; got %v", targets)
	}
}

func TestSwiftExtractor_ParameterAndReturnTypeUse(t *testing.T) {
	// Parameter and return type annotations must each emit a type-use
	// edge attributed to the function / method node; primitives skipped;
	// generic placeholders (T) skipped; generic arguments surfaced.
	src := []byte(`func handle(req: Request, n: Int) -> HttpResponse {
    return HttpResponse()
}

class Api {
    func lookup(id: String) -> Result<User, Failure> {
        return .success(User())
    }
}

func generic<T>(x: T) -> Box<Widget> {
    return Box()
}
`)
	res, err := NewSwiftExtractor().Extract("api.swift", src)
	if err != nil {
		t.Fatal(err)
	}

	// Free function: param Request + return HttpResponse, no Int.
	if !hasTypedAsFrom(res.Edges, "api.swift::handle", "Request") {
		t.Errorf("expected param type-use edge handle -> Request; edges=%v", res.Edges)
	}
	if !hasTypedAsFrom(res.Edges, "api.swift::handle", "HttpResponse") {
		t.Errorf("expected return type-use edge handle -> HttpResponse; edges=%v", res.Edges)
	}

	// Method: param String (primitive — skipped) + return Result<User, Failure>.
	for _, want := range []string{"User", "Failure", "Result"} {
		if !hasTypedAsFrom(res.Edges, "api.swift::Api.lookup", want) {
			t.Errorf("expected type-use edge Api.lookup -> %s; edges=%v", want, res.Edges)
		}
	}

	// Generic placeholder T must NOT leak as a type; generic arg Widget must.
	targets := swiftTypedAsTargets(res.Edges)
	if targets["unresolved::T"] {
		t.Errorf("generic placeholder T must NOT produce a type-use edge; got %v", targets)
	}
	if !hasTypedAsFrom(res.Edges, "api.swift::generic", "Box") {
		t.Errorf("expected return type-use edge generic -> Box; edges=%v", res.Edges)
	}
	if !hasTypedAsFrom(res.Edges, "api.swift::generic", "Widget") {
		t.Errorf("expected generic-arg type-use edge generic -> Widget; edges=%v", res.Edges)
	}
	if targets["unresolved::String"] || targets["unresolved::Int"] {
		t.Errorf("primitives String/Int must NOT produce type-use edges; got %v", targets)
	}
}
