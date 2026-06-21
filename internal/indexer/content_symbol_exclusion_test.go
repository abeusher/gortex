package indexer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestContentSplit_ExcludedFromSymbolSearch verifies C2: content
// (data_class=content) section bodies never enter the symbol search — a
// body term yields no content node from SearchSymbols — while remaining
// findable via the dedicated content index, and code symbol search is
// unaffected.
func TestContentSplit_ExcludedFromSymbolSearch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "doc.txt"),
		"zzcontentterm "+strings.Repeat("filler word ", 60))
	writeFile(t, filepath.Join(dir, "code.go"),
		"package p\n\nfunc ZzCodeFunc() {}\n")

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "store.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default().Index
	cfg.Workers = 2
	_, err = New(store, reg, cfg, zap.NewNop()).IndexCtx(context.Background(), dir)
	require.NoError(t, err)

	// Content nodes exist in the graph...
	var contentExists bool
	for _, n := range store.AllNodes() {
		if isContentNode(n) {
			contentExists = true
			break
		}
	}
	require.True(t, contentExists, "content section nodes must exist in the graph")

	// ...but the symbol search returns no content node for a body term.
	symHits, err := store.SearchSymbols("zzcontentterm", 50)
	require.NoError(t, err)
	for _, h := range symHits {
		n := store.GetNode(h.NodeID)
		require.False(t, n != nil && isContentNode(n),
			"content node leaked into symbol search: %s", h.NodeID)
	}

	// The content index does return it.
	cHits, err := store.SearchContent("zzcontentterm", "", 10)
	require.NoError(t, err)
	require.NotEmpty(t, cHits, "content term must be findable via the content index")

	// Code symbol search is unaffected.
	codeHits, err := store.SearchSymbols("ZzCodeFunc", 10)
	require.NoError(t, err)
	require.NotEmpty(t, codeHits, "code symbol search must still work")
}
