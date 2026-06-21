package store_sqlite

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestContentFTS_BasicAndFileWipe(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var cs graph.ContentSearcher = s // runtime assertion the store satisfies the capability

	require.NoError(t, cs.WipeContent("")) // clean table
	items := []graph.ContentFTSItem{
		{NodeID: "a.txt::doc:section-0", FilePath: "a.txt", Ordinal: 0, Body: "the quick brown fox jumps over the lazy dog"},
		{NodeID: "a.txt::doc:section-1", FilePath: "a.txt", Ordinal: 1, Body: "lorem ipsum dolor sit amet consectetur"},
		{NodeID: "b.pdf::doc:pdf_page-0", FilePath: "b.pdf", Ordinal: 0, Body: "quantum entanglement and superposition explained"},
	}
	require.NoError(t, cs.AppendContent("", items))
	require.NoError(t, cs.BuildContentIndex())

	// A body term resolves to exactly its section, with a non-empty snippet.
	hits, err := cs.SearchContent("quantum", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "b.pdf::doc:pdf_page-0", hits[0].NodeID)
	require.Equal(t, "b.pdf", hits[0].FilePath)
	require.Equal(t, 0, hits[0].Ordinal)
	require.NotEmpty(t, hits[0].Snippet)

	hits, err = cs.SearchContent("fox", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "a.txt::doc:section-0", hits[0].NodeID)
	require.Equal(t, 0, hits[0].Ordinal)

	// WipeContentFile drops only a.txt's rows; b.pdf survives.
	require.NoError(t, cs.WipeContentFile("a.txt"))
	hits, err = cs.SearchContent("fox", "", 10)
	require.NoError(t, err)
	require.Empty(t, hits)
	hits, err = cs.SearchContent("quantum", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
}

func TestContentFTS_RepoScoping(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, s.AppendContent("repoA", []graph.ContentFTSItem{
		{NodeID: "repoA::x.txt::doc:section-0", FilePath: "x.txt", Body: "alpha beta gamma"},
	}))
	require.NoError(t, s.AppendContent("repoB", []graph.ContentFTSItem{
		{NodeID: "repoB::y.txt::doc:section-0", FilePath: "y.txt", Body: "alpha delta epsilon"},
	}))

	// Scoped search returns only the matching repo's hit.
	hits, err := s.SearchContent("alpha", "repoA", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "repoA::x.txt::doc:section-0", hits[0].NodeID)

	// Unscoped search spans both repos.
	hits, err = s.SearchContent("alpha", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 2)

	// WipeContent scopes to one repo, leaving the sibling intact.
	require.NoError(t, s.WipeContent("repoA"))
	hits, err = s.SearchContent("alpha", "", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.Equal(t, "repoB::y.txt::doc:section-0", hits[0].NodeID)
}
