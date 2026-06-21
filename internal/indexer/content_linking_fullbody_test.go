package indexer

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestContentLink_UsesFullBodyNotSnippet verifies C8: the content->code
// linker matches symbol references against the FULL section body (streamed
// from the content index) rather than the leaned node snippet. The symbol
// name is placed well past the snippet cap, so a link can only be minted if
// the linker read the whole body. linkContentToCode is the post-index global
// pass (RunGlobalGraphPasses), so the test drives it explicitly after IndexCtx.
func TestContentLink_UsesFullBodyNotSnippet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "code.go"), "package p\n\nfunc ZzWidgetMaker() {}\n")
	// "ZzWidgetMaker" appears only after ~480 chars of filler — beyond the
	// 240-byte snippet the node retains, so it lives only in the content index.
	body := strings.Repeat("filler word ", 40) + " ZzWidgetMaker is described in this specification"
	writeFile(t, filepath.Join(dir, "spec.txt"), body)

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "s.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default().Index
	cfg.Workers = 2
	idx := New(store, reg, cfg, zap.NewNop())
	_, err = idx.IndexCtx(context.Background(), dir)
	require.NoError(t, err)

	// Sanity: the content node's retained snippet does NOT contain the symbol.
	for _, n := range store.AllNodes() {
		if isContentNode(n) {
			st, _ := n.Meta["section_text"].(string)
			require.NotContains(t, st, "ZzWidgetMaker",
				"precondition: the symbol must live past the node snippet")
		}
	}

	// Drive the post-index content->code linking pass.
	idx.linkContentToCode()

	// The link must exist — proving it came from the full body, not the snippet.
	var linked bool
	for _, e := range store.AllEdges() {
		if e.Kind == graph.EdgeMotivates && strings.Contains(e.To, "ZzWidgetMaker") {
			linked = true
		}
	}
	require.True(t, linked,
		"content->code link must be minted from the full body (symbol past the snippet cap)")
}
