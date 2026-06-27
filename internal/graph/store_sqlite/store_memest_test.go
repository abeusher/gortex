package store_sqlite_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// TestAllRepoMemoryEstimates_Memoized verifies the short-TTL memoisation:
// within the TTL a second call serves the cached estimate instead of
// re-running the COUNT … GROUP BY scan, so the status path can poll cheaply
// even while enrichment is writing to the same store.
func TestAllRepoMemoryEstimates_Memoized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.sqlite")
	s, err := store_sqlite.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	totalNodes := func(m map[string]graph.RepoMemoryEstimate) int {
		n := 0
		for _, e := range m {
			n += e.NodeCount
		}
		return n
	}

	s.AddBatch([]*graph.Node{
		{ID: "a.go::Foo", Kind: graph.KindFunction, Name: "Foo", FilePath: "a.go", RepoPrefix: "r1"},
		{ID: "a.go::Bar", Kind: graph.KindFunction, Name: "Bar", FilePath: "a.go", RepoPrefix: "r1"},
	}, nil)

	first := s.AllRepoMemoryEstimates()
	require.Equal(t, 2, totalNodes(first))

	// Add another node; within the memoisation TTL the estimate is served
	// from cache, so the freshly-added node is intentionally not yet
	// reflected — proof the COUNT scan was skipped.
	s.AddBatch([]*graph.Node{
		{ID: "b.go::Baz", Kind: graph.KindFunction, Name: "Baz", FilePath: "b.go", RepoPrefix: "r1"},
	}, nil)
	cached := s.AllRepoMemoryEstimates()
	require.Equal(t, 2, totalNodes(cached), "within the TTL the result should be the memoised value, not a fresh COUNT")

	// The returned map is a copy: mutating it must not corrupt the cache.
	for k := range cached {
		delete(cached, k)
	}
	again := s.AllRepoMemoryEstimates()
	require.Equal(t, 2, totalNodes(again), "the cache must hand back an independent copy")
}
