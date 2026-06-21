package mcp

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// TestMergeContentChannel_PullsFromContentIndex verifies C4: with content
// out of the symbol search, the content retrieval channel pulls matching
// content sections from the dedicated ContentSearcher and materialises the
// nodes — even when a body term appears nowhere in the symbol index.
func TestMergeContentChannel_PullsFromContentIndex(t *testing.T) {
	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "s.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// A lean content node in the graph + its full body in the content index.
	store.AddNode(&graph.Node{
		ID:       "doc.txt::doc:section-0",
		Kind:     graph.KindDoc,
		FilePath: "doc.txt",
		Meta:     map[string]any{"data_class": "content", "section_text": "snippet"},
	})
	require.NoError(t, store.AppendContent("", []graph.ContentFTSItem{
		{NodeID: "doc.txt::doc:section-0", FilePath: "doc.txt", Body: "the zzcontentterm appears only in the full body"},
	}))
	require.NoError(t, store.BuildContentIndex())

	s := &Server{
		graph:      store,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}

	merged := s.mergeContentChannel(context.Background(), "zzcontentterm", nil, 10)
	var ids []string
	for _, n := range merged {
		ids = append(ids, n.ID)
	}
	require.Contains(t, ids, "doc.txt::doc:section-0",
		"the content channel must surface the content node from the content index")

	// A term in no content body yields nothing extra.
	require.Empty(t, s.mergeContentChannel(context.Background(), "zznomatchterm", nil, 10))
}
