package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func fastapiCaller(g *graph.Graph, id, file string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: "handler", FilePath: file, Language: "python"})
}

func fastapiEdge(g *graph.Graph, from, file, to string, kind graph.EdgeKind, via string) {
	g.AddEdge(&graph.Edge{From: from, To: to, Kind: kind, FilePath: file, Meta: map[string]any{"via": via}})
}

func synthFastAPIEdge(g graph.Store, kind graph.EdgeKind, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(kind) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthFastAPIResolve {
			return e
		}
	}
	return nil
}

func TestResolveFastAPIDeps_DependencyByConvention(t *testing.T) {
	g := graph.New()
	const handler = "app/routers/users.py::list_users"
	fastapiCaller(g, handler, "app/routers/users.py")
	// get_db provider lives only under /dependencies/ — reachable by
	// convention, not by a resolvable import.
	convNode(g, "app/dependencies/db.py::get_db", "app/dependencies/db.py", "get_db")
	fastapiEdge(g, handler, "app/routers/users.py", "unresolved::get_db", graph.EdgeCalls, "fastapi.Depends")

	require.Equal(t, 1, ResolveFastAPIDeps(g))
	assert.NotNil(t, synthFastAPIEdge(g, graph.EdgeCalls, handler, "app/dependencies/db.py::get_db"),
		"Depends(get_db) binds to /dependencies/db.py")
}

func TestResolveFastAPIDeps_RouterByConvention(t *testing.T) {
	g := graph.New()
	const main = "app/main.py"
	g.AddNode(&graph.Node{ID: main, Kind: graph.KindFile, Name: "main.py", FilePath: main, Language: "python"})
	convNode(g, "app/routers/api.py::api_router", "app/routers/api.py", "api_router")
	fastapiEdge(g, main, main, "unresolved::api_router", graph.EdgeReferences, "fastapi.router")

	require.Equal(t, 1, ResolveFastAPIDeps(g))
	assert.NotNil(t, synthFastAPIEdge(g, graph.EdgeReferences, main, "app/routers/api.py::api_router"),
		"include_router(api_router) binds to /routers/api.py")
}

func TestResolveFastAPIDeps_AlreadyResolvedUnchanged(t *testing.T) {
	g := graph.New()
	const handler = "app/routers/users.py::list_users"
	fastapiCaller(g, handler, "app/routers/users.py")
	// A Depends edge that the reference resolver already bound (To is a real
	// node, not unresolved) must not be touched — no double-binding.
	convNode(g, "app/dependencies/db.py::get_db", "app/dependencies/db.py", "get_db")
	g.AddEdge(&graph.Edge{
		From: handler, To: "app/services/db.py::get_db", Kind: graph.EdgeCalls,
		FilePath: "app/routers/users.py", Meta: map[string]any{"via": "fastapi.Depends"},
	})
	g.AddNode(&graph.Node{ID: "app/services/db.py::get_db", Kind: graph.KindFunction, Name: "get_db", FilePath: "app/services/db.py"})

	require.Equal(t, 0, ResolveFastAPIDeps(g))
	// The already-resolved edge still points where it did, unstamped.
	assert.Nil(t, synthFastAPIEdge(g, graph.EdgeCalls, handler, "app/dependencies/db.py::get_db"))
}

func TestResolveFastAPIDeps_NonPythonLeftAlone(t *testing.T) {
	g := graph.New()
	const goFn = "pkg/svc.go::Handler"
	g.AddNode(&graph.Node{ID: goFn, Kind: graph.KindFunction, Name: "Handler", FilePath: "pkg/svc.go", Language: "go"})
	convNode(g, "app/dependencies/db.py::get_db", "app/dependencies/db.py", "get_db")
	fastapiEdge(g, goFn, "pkg/svc.go", "unresolved::get_db", graph.EdgeCalls, "fastapi.Depends")

	require.Equal(t, 0, ResolveFastAPIDeps(g))
	assert.Nil(t, synthFastAPIEdge(g, graph.EdgeCalls, goFn, "app/dependencies/db.py::get_db"))
}
