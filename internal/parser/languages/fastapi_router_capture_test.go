package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func fastapiRouterRef(edges []*graph.Edge, from, router string) *graph.Edge {
	for _, e := range edges {
		if e.Kind != graph.EdgeReferences || e.From != from || e.To != "unresolved::"+router {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == "fastapi.router" {
			return e
		}
	}
	return nil
}

func TestFastAPIRouterCapture_ModuleLevelMount(t *testing.T) {
	src := `from fastapi import FastAPI
from .routers.api import api_router

app = FastAPI()
app.include_router(api_router, prefix="/api")
`
	res, err := NewPythonExtractor().Extract("app/main.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	// Module-level mount attributes to the file node.
	ref := fastapiRouterRef(res.Edges, "app/main.py", "api_router")
	if ref == nil {
		t.Fatalf("expected a fastapi.router reference to api_router from the file node")
	}
	if rn, _ := ref.Meta["router_name"].(string); rn != "api_router" {
		t.Errorf("router_name = %q (want api_router)", rn)
	}
}

func TestFastAPIRouterCapture_DottedArgIgnored(t *testing.T) {
	// `include_router(users.router)` — a dotted argument is not a bare router
	// identifier and is left to the import resolver.
	src := `app.include_router(users.router)
`
	res, err := NewPythonExtractor().Extract("app/main.py", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == "fastapi.router" {
				t.Errorf("dotted include_router arg should not emit a fastapi.router ref, got %s", e.To)
			}
		}
	}
}
