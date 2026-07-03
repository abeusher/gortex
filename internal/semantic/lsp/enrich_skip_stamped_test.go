package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/semantic"
)

// nodeAlreadyStamped gates the hover phase: a symbol a prior pass stamped with
// a semantic type must not be re-hovered.
func TestNodeAlreadyStamped(t *testing.T) {
	assert.False(t, nodeAlreadyStamped(nil), "nil node")
	assert.False(t, nodeAlreadyStamped(&graph.Node{ID: "a"}), "nil Meta")
	assert.False(t, nodeAlreadyStamped(&graph.Node{ID: "a", Meta: map[string]any{}}), "empty Meta")
	assert.False(t, nodeAlreadyStamped(&graph.Node{ID: "a", Meta: map[string]any{"semantic_source": "lsp-go"}}),
		"semantic_source alone is not a stamp")
	assert.False(t, nodeAlreadyStamped(&graph.Node{ID: "a", Meta: map[string]any{"semantic_type": ""}}),
		"empty semantic_type is not a stamp")
	assert.True(t, nodeAlreadyStamped(&graph.Node{ID: "a", Meta: map[string]any{"semantic_type": "func F() string"}}),
		"non-empty semantic_type is a stamp")
	assert.True(t, nodeAlreadyStamped(&graph.Node{ID: "a", Meta: map[string]any{"semantic_type": 42}}),
		"non-nil non-string semantic_type is a stamp")
}

// The hover phase must skip nodes a prior pass already stamped and hover only
// the fresh ones; the skipped count is reflected in the post-filter candidate
// count and the coverage totals.
func TestLSP_Provider_SkipsAlreadyStampedNodes(t *testing.T) {
	t.Setenv("GORTEX_LSP_SWEEP", "full") // sweep every file so the fresh node is hovered; the stamp-skip still excludes the stamped one from candidate selection
	repoRoot := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(repoRoot, "main.go"),
		[]byte("package main\n\nfunc F() string { return \"hi\" }\n\nfunc G() int { return 0 }\n"),
		0o644,
	))

	var hoverCalls atomic.Int64
	server := newFakeLSPServer()
	server.handle("textDocument/hover", func(params json.RawMessage) (any, *jsonRPCError) {
		hoverCalls.Add(1)
		return map[string]any{
			"contents": map[string]any{"kind": "plaintext", "value": "func G() int"},
		}, nil
	})

	p, cleanup := providerWithFakeServer(t, server, []string{"go"})
	defer cleanup()

	g := graph.New()
	// F is already stamped by a prior pass — must not be re-hovered.
	g.AddNode(&graph.Node{
		ID: "main.go::F", Kind: graph.KindFunction, Name: "F",
		FilePath: "main.go", StartLine: 3, EndLine: 3, Language: "go",
		Meta: map[string]any{"semantic_type": "func F() string", "semantic_source": "lsp-prior"},
	})
	// G is fresh — must be hovered and stamped.
	g.AddNode(&graph.Node{
		ID: "main.go::G", Kind: graph.KindFunction, Name: "G",
		FilePath: "main.go", StartLine: 5, EndLine: 5, Language: "go",
	})

	done := make(chan *semantic.EnrichResult, 1)
	go func() {
		res, err := p.Enrich(g, repoRoot)
		require.NoError(t, err)
		done <- res
	}()
	var res *semantic.EnrichResult
	select {
	case res = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Enrich timed out")
	}

	// Only G was a hover candidate; F was skipped as already stamped.
	assert.Equal(t, int64(1), hoverCalls.Load(), "only the fresh node is hovered")
	assert.Equal(t, 1, res.HoverCandidates, "post-filter candidate count excludes the stamped node")
	assert.Equal(t, 2, res.SymbolsTotal, "SymbolsTotal still counts every symbol")
	// F pre-seeded as covered, G newly covered.
	assert.Equal(t, 2, res.SymbolsCovered)

	// F keeps its prior stamp (a re-hover would have overwritten it).
	assert.Equal(t, "func F() string", g.GetNode("main.go::F").Meta["semantic_type"])
	assert.Equal(t, "lsp-prior", g.GetNode("main.go::F").Meta["semantic_source"])
	// G is now stamped by this pass.
	assert.Equal(t, "func G() int", g.GetNode("main.go::G").Meta["semantic_type"])
	assert.Equal(t, "lsp-fake-lsp", g.GetNode("main.go::G").Meta["semantic_source"])
}
