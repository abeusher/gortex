package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// TestGetRepoNonContentNodes verifies the SQL-level content filter (which
// json_extracts data_class out of the JSON meta blob) drops only content
// section nodes — keeping code, markdown prose, and data assets — so the
// code passes can enumerate without materialising content sections.
func TestGetRepoNonContentNodes(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "n.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	s.AddBatch([]*graph.Node{
		{ID: "code1", Kind: graph.KindFunction, Name: "Foo", RepoPrefix: "r"},
		{ID: "content1", Kind: graph.KindDoc, Name: "doc.txt::0", RepoPrefix: "r",
			Meta: map[string]any{"data_class": "content", "section_text": "x"}},
		{ID: "prose1", Kind: graph.KindDoc, Name: "README.md::0", RepoPrefix: "r",
			Meta: map[string]any{"asset_kind": "markdown_section"}},
		{ID: "data1", Kind: graph.KindFile, Name: "x.parquet", RepoPrefix: "r",
			Meta: map[string]any{"data_class": "data"}},
	}, nil)

	// Runtime assertion the store satisfies the optional capability.
	var cr graph.NonContentNodeReader = s

	ids := map[string]bool{}
	for _, n := range cr.GetRepoNonContentNodes("r") {
		ids[n.ID] = true
	}
	require.True(t, ids["code1"], "code node kept")
	require.True(t, ids["prose1"], "markdown prose kept (not data_class=content)")
	require.True(t, ids["data1"], "data asset kept (data_class=data, not content)")
	require.False(t, ids["content1"], "content section dropped at the SQL level")
	require.Len(t, ids, 3)

	// Empty prefix spans all repos (still drops content).
	require.Len(t, s.GetRepoNonContentNodes(""), 3)
}
