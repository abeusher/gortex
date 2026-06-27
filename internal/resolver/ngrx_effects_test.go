package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestResolveNgRxEffects(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID: "effects.ts::UserEffects.loadUsers$", Kind: graph.KindVariable, Name: "loadUsers$",
		FilePath: "effects.ts", Language: "typescript", Meta: map[string]any{"ngrx_effect": "loadUsers$"},
	})
	g.AddNode(&graph.Node{
		ID: "actions.ts::LoadUsers", Kind: graph.KindConstant, Name: "LoadUsers",
		FilePath: "actions.ts", Language: "typescript",
	})
	g.AddEdge(&graph.Edge{
		From: "effects.ts::UserEffects.loadUsers$", To: "unresolved::*.LoadUsers", Kind: graph.EdgeCalls,
		FilePath: "effects.ts", Meta: map[string]any{"via": "ngrx-effect", "ngrx_action": "LoadUsers"},
	})

	n := ResolveNgRxEffects(g)
	assert.Equal(t, 1, n, "one effect placeholder should resolve")

	var found *graph.Edge
	for _, e := range g.GetOutEdges("effects.ts::UserEffects.loadUsers$") {
		if e.Kind == graph.EdgeCalls && e.To == "actions.ts::LoadUsers" {
			found = e
		}
	}
	require.NotNil(t, found, "effect should resolve to the LoadUsers action node")
	assert.Equal(t, SynthNgRxEffect, found.Meta[MetaSynthesizedBy])
}

func TestResolveNgRxEffects_NoEffectsIsNoOp(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "a.ts::x", Kind: graph.KindFunction, Name: "x", FilePath: "a.ts"})
	assert.Equal(t, 0, ResolveNgRxEffects(g))
}
