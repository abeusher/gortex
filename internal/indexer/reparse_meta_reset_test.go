package indexer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/semantic"
)

// The hover-skip optimization rests on this invariant: a per-file re-parse
// mints FRESH node objects with no Meta merge, so an edited file's symbols
// lose their semantic_type stamp and are re-selected by the next enrichment
// pass. The incremental edge-reuse machinery is edge-scoped only and must not
// carry node Meta across a re-index. If this test fails, the skip predicate is
// unsafe: an edited file would keep its stale stamp and never be re-hovered.
func TestIndexFile_ReparseResetsSemanticMeta(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(src, []byte("package main\n\nfunc F() string { return \"hi\" }\n"), 0o644))

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default().Index
	cfg.Workers = 2
	idx := New(g, reg, cfg, zap.NewNop())

	if _, err := idx.Index(dir); err != nil {
		t.Fatalf("index: %v", err)
	}

	id := "main.go::F"
	n := g.GetNode(id)
	require.NotNil(t, n, "expected node for F after initial index")

	// Stamp semantic_type the way an enrichment pass would, and round-trip it
	// through the store so the stamp is durably persisted on the node.
	semantic.EnrichNodeMeta(n, "semantic_type", "func F() string", "lsp-test")
	g.AddBatch([]*graph.Node{n}, nil)
	require.Equal(t, "func F() string", g.GetNode(id).Meta["semantic_type"],
		"precondition: the stamp is persisted before the re-index")

	// Edit the file and re-index just it — the fsnotify single-file path.
	require.NoError(t, os.WriteFile(src,
		[]byte("package main\n\nfunc F() string { return \"bye\" }\n\nfunc G() int { return 0 }\n"), 0o644))
	require.NoError(t, idx.IndexFile(src))

	// The re-minted node must carry NO semantic_type: the re-parse discarded
	// the stamp, so the node is re-selected for hover naturally. No semantic
	// manager is wired here, so nothing could re-stamp it.
	reminted := g.GetNode(id)
	require.NotNil(t, reminted, "expected node for F after re-index")
	if reminted.Meta != nil {
		_, stamped := reminted.Meta["semantic_type"]
		require.False(t, stamped,
			"re-parse must mint a fresh node with no semantic_type; stale stamp survived: %v", reminted.Meta)
	}
}
