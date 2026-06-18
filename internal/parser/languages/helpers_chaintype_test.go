package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// TestChainReturnTypeResolution exercises the shared chained-receiver /
// factory-chain resolver across the idioms of the AST languages that feed it
// (Go, TypeScript, C#). The resolver reads only return_type / receiver Meta, so
// one synthetic result covers every language: each sub-case models a different
// language's static-factory or fluent-builder shape and asserts the receiver
// expression resolves to the type the chain evaluates to.
func TestChainReturnTypeResolution(t *testing.T) {
	mkSym := func(name, recv, ret string) *graph.Node {
		meta := map[string]any{}
		kind := graph.KindFunction
		if recv != "" {
			meta["receiver"] = recv
			kind = graph.KindMethod
		}
		if ret != "" {
			meta["return_type"] = ret
		}
		return &graph.Node{Name: name, Kind: kind, Meta: meta}
	}

	result := &parser.ExtractionResult{Nodes: []*graph.Node{
		// Go: func NewServer() *Server; func (*Server) Router() *Router
		mkSym("NewServer", "", "Server"),
		mkSym("Router", "Server", "Router"),
		// TypeScript: function createClient(): ApiClient; ApiClient.users(): UserApi
		mkSym("createClient", "", "ApiClient"),
		mkSym("users", "ApiClient", "UserApi"),
		// C#: static Builder Create(); Widget Builder.Build()
		mkSym("Create", "", "Builder"),
		mkSym("Build", "Builder", "Widget"),
		// A method that shares a name with a factory, to prove the free
		// function wins as a chain seed over a same-named method.
		mkSym("NewServer", "Other", "Decoy"),
	}}

	cases := []struct {
		name string
		expr string
		tenv typeEnv
		want string
	}{
		// Factory chains: the base segment is a called free function, not a
		// typed variable — the J3 capability.
		{"go_factory_single", "NewServer()", nil, "Server"},
		{"go_factory_chain", "NewServer().Router()", nil, "Router"},
		{"ts_factory_chain", "createClient().users()", nil, "UserApi"},
		{"csharp_factory_chain", "Create().Build()", nil, "Widget"},
		// A typed variable still seeds the chain as before.
		{"var_method_chain", "srv.Router()", typeEnv{"srv": "Server"}, "Router"},
		// An ordinary method call on an untyped variable must NOT be treated
		// as a factory seed (base is not itself a call).
		{"untyped_var_no_factory", "obj.Router()", nil, ""},
		// Unresolvable: the base factory has no known return type.
		{"unknown_factory", "mystery().Build()", nil, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveChainType(c.expr, c.tenv, result)
			if got != c.want {
				t.Errorf("resolveChainType(%q) = %q, want %q", c.expr, got, c.want)
			}
		})
	}
}
