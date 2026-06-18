package resolver

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestGinMiddlewareDispatcherToHandlerEdges drives the Gin middleware-chain
// synthesizer end-to-end: the Go extractor's source parse stamps the dispatcher
// (the method indexing `c.handlers[idx](c)`) and the registered handler names,
// and the resolver bridges the dispatcher to each handler with a tier-tagged
// traversable call edge. That edge is the reachability a same-file scanner
// drops at the indexed-slice indirection — get_call_chain now reaches the
// handlers from the dispatcher. String path args and inline closures are not
// handlers, and with no dispatcher in the graph nothing is synthesized.
func TestGinMiddlewareDispatcherToHandlerEdges(t *testing.T) {
	const router = `package web

type Context struct {
	index    int
	handlers []HandlerFunc
}
type HandlerFunc func(*Context)

func (c *Context) Next() {
	for c.index < len(c.handlers) {
		c.handlers[c.index](c)
		c.index++
	}
}

func Logger() HandlerFunc { return nil }
func listUsers(c *Context) {}
func createUser(c *Context) {}

func setup(r *Engine) {
	r.Use(Logger())
	r.GET("/users", listUsers)
	r.POST("/users", createUser, func(c *Context) {})
}
`
	build := func(src string) *graph.Graph {
		res, err := languages.NewGoExtractor().Extract("web/web.go", []byte(src))
		if err != nil {
			t.Fatalf("extract: %v", err)
		}
		g := graph.New()
		for _, n := range res.Nodes {
			g.AddNode(n)
		}
		for _, e := range res.Edges {
			g.AddEdge(e)
		}
		return g
	}

	g := build(router)
	n := ResolveGinMiddlewareCalls(g)
	if n != 3 {
		t.Fatalf("synthesized %d dispatcher→handler edges, want 3", n)
	}

	// Index the synthesized edges by target.
	const disp = "web/web.go::Context.Next"
	targets := map[string]*graph.Edge{}
	for _, e := range g.AllEdges() {
		if e.Meta != nil && e.Meta["via"] == ginMiddlewareVia && e.From == disp {
			targets[e.To] = e
		}
	}
	for _, want := range []string{"web/web.go::Logger", "web/web.go::listUsers", "web/web.go::createUser"} {
		e, ok := targets[want]
		if !ok {
			t.Errorf("missing dispatcher→handler edge to %q", want)
			continue
		}
		// Tier-tagged + provenance-stamped so the edge is min_tier filterable
		// and attributable — not an opaque heuristic.
		if e.Origin != graph.OriginSpeculative {
			t.Errorf("edge to %q origin=%v, want OriginSpeculative", want, e.Origin)
		}
		if e.Meta[MetaSynthesizedBy] != SynthGinMiddleware {
			t.Errorf("edge to %q synthesized_by=%v, want %q", want, e.Meta[MetaSynthesizedBy], SynthGinMiddleware)
		}
		if e.Kind != graph.EdgeCalls {
			t.Errorf("edge to %q kind=%v, want EdgeCalls (traversable in get_call_chain)", want, e.Kind)
		}
	}

	// The inline closure passed to POST is not a named handler.
	for to := range targets {
		if to == "" {
			t.Errorf("synthesized an edge to an empty target (a closure leaked in)")
		}
	}

	// Gate: no dispatcher in the graph → nothing synthesized.
	const noDispatcher = `package web
type HandlerFunc func()
func listUsers() {}
func setup(r *Engine) {
	r.GET("/users", listUsers)
}
`
	g2 := build(noDispatcher)
	if got := ResolveGinMiddlewareCalls(g2); got != 0 {
		t.Errorf("with no dispatcher present, synthesized %d edges, want 0", got)
	}
}
