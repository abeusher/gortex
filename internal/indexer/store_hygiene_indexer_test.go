package indexer

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/search"
)

// newSqliteMultiIndexer builds a MultiIndexer over a real on-disk sqlite
// store (the only backend with sidecar tables) for the two repos in repos.
// Returns the indexer and the store so a test can read the persisted
// sidecars directly.
func newSqliteMultiIndexer(t *testing.T, repos []config.RepoEntry) (*MultiIndexer, *store_sqlite.Store) {
	t.Helper()
	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{Repos: repos}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())
	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	s, err := store_sqlite.Open(filepath.Join(t.TempDir(), "store.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	mi := NewMultiIndexer(graph.Store(s), newTestRegistry(), search.NewBM25(), cm, zap.NewNop())
	return mi, s
}

// D1: untracking a prefixed repo must take the capability path (PurgeRepo)
// and clear its sidecar rows, not just nodes+edges — otherwise a store
// accumulates file_mtimes/etc. residue across untrack/retrack cycles.
func TestUntrackRepo_PurgesSidecarRows(t *testing.T) {
	repoA := setupRepoDir(t, "repo-a")
	repoB := setupRepoDir(t, "repo-b")
	mi, s := newSqliteMultiIndexer(t, []config.RepoEntry{
		{Path: repoA, Name: "repo-a"},
		{Path: repoB, Name: "repo-b"},
	})
	_, err := mi.IndexAll()
	require.NoError(t, err)
	require.True(t, mi.IsMultiRepo(), "two repos -> prefixed multi-repo mode")

	require.NotEmpty(t, s.LoadFileMtimes("repo-a"), "repo-a mtimes persisted under its prefix")
	require.NotEmpty(t, s.LoadFileMtimes("repo-b"), "repo-b mtimes persisted under its prefix")

	mi.UntrackRepo("repo-a")

	// PurgeRepo cleared repo-a's sidecar (EvictRepo alone would have leaked
	// it); repo-b untouched.
	assert.Empty(t, s.LoadFileMtimes("repo-a"), "untrack purged repo-a's file_mtimes sidecar")
	assert.Empty(t, s.GetRepoNodes("repo-a"), "untrack evicted repo-a's nodes")
	assert.NotEmpty(t, s.LoadFileMtimes("repo-b"), "repo-b sidecars intact")
	assert.NotEmpty(t, s.GetRepoNodes("repo-b"), "repo-b nodes intact")
}

// D3: when a second repo joins a lone single-repo daemon, the first repo's
// nodes are re-minted under its prefix — and its sidecar residue must move
// with them, or the next warm restart finds zero mtimes under the new prefix
// and full-re-tracks a repo that never changed.
func TestMigrateLoneUnprefixed_ReKeysMtimesToNewPrefix(t *testing.T) {
	repoA := setupRepoDir(t, "repo-a")
	mi, s := newSqliteMultiIndexer(t, []config.RepoEntry{{Path: repoA, Name: "repo-a"}})
	_, err := mi.IndexAll()
	require.NoError(t, err)
	require.False(t, mi.IsMultiRepo(), "one repo indexes unprefixed")

	require.NotEmpty(t, s.LoadFileMtimes(""), "solo repo persists mtimes under ''")
	require.Empty(t, s.LoadFileMtimes("repo-a"), "nothing under the basename prefix yet")

	// Track a second repo -> migrateLoneUnprefixedRepoCtx fires.
	repoB := setupRepoDir(t, "repo-b")
	_, err = mi.TrackRepoCtx(context.Background(), config.RepoEntry{Path: repoB, Name: "repo-b"})
	require.NoError(t, err)
	require.True(t, mi.IsMultiRepo())

	// The exact warm-restart bug: mtimes must now live under the new prefix,
	// and '' must no longer carry the migrated repo's residue.
	assert.NotEmpty(t, s.LoadFileMtimes("repo-a"), "migrated repo's mtimes now load under its prefix")
	assert.Empty(t, s.LoadFileMtimes(""), "no '' file_mtimes residue survives the migration")
}

// mtimeCountingStore counts BulkSetFileMtimes calls so a test can prove the
// full-index path persists mtimes INCREMENTALLY (before the final
// authoritative ReplaceFileMtimes), not just at the end. Every other method
// is the embedded on-disk store.
type mtimeCountingStore struct {
	*store_sqlite.Store
	bulkCalls atomic.Int64
}

func (m *mtimeCountingStore) BulkSetFileMtimes(prefix string, mtimes map[string]int64) error {
	m.bulkCalls.Add(1)
	return m.Store.BulkSetFileMtimes(prefix, mtimes)
}
