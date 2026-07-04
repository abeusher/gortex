package semantic

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// TestRepoEnrichmentMarker_RoundTrip: RecordRepoEnrichmentComplete persists a
// whole-repo completion marker keyed on the reserved provider, and
// RepoEnrichmentMarkerState reads it back as current only at the recorded sha on
// a clean tree — the signal a warm restart uses to skip an already-enriched
// repo.
func TestRepoEnrichmentMarker_RoundTrip(t *testing.T) {
	mgr := NewManager(Config{Enabled: true}, zap.NewNop())
	g := newMarkerStore(t)

	// No marker yet: not current, but the backend does persist state, so the
	// caller must treat the repo as incomplete (resume enrichment).
	current, persisted := mgr.RepoEnrichmentMarkerState(g, markerRepo, "sha-1")
	assert.True(t, persisted, "sqlite backend persists enrichment state")
	assert.False(t, current, "no marker row yet ⇒ not current")

	mgr.RecordRepoEnrichmentComplete(g, markerRepo, "sha-1", false)

	current, persisted = mgr.RepoEnrichmentMarkerState(g, markerRepo, "sha-1")
	assert.True(t, persisted)
	assert.True(t, current, "marker at the recorded sha on a clean tree is current")

	// A moved HEAD invalidates the marker — the graph's enrichment describes the
	// old revision, so the repo must re-enrich.
	current, _ = mgr.RepoEnrichmentMarkerState(g, markerRepo, "sha-2")
	assert.False(t, current, "marker sha differs from HEAD ⇒ not current")

	// The marker lives under the reserved key, never a real provider's slot.
	_, found, err := g.GetEnrichmentState(markerRepo, repoEnrichMarkerProvider)
	require.NoError(t, err)
	assert.True(t, found, "the whole-repo marker is stored under the reserved provider key")
}

// TestRepoEnrichmentMarker_DirtyAndEmptyShaWriteNothing: the write shares
// recordEnrichMarker's discipline — a dirty tree or an empty sha persists no
// marker, because it would not describe the committed state the sha names.
func TestRepoEnrichmentMarker_DirtyAndEmptyShaWriteNothing(t *testing.T) {
	mgr := NewManager(Config{Enabled: true}, zap.NewNop())

	t.Run("dirty tree", func(t *testing.T) {
		g := newMarkerStore(t)
		mgr.RecordRepoEnrichmentComplete(g, markerRepo, "sha-1", true)
		_, found, err := g.GetEnrichmentState(markerRepo, repoEnrichMarkerProvider)
		require.NoError(t, err)
		assert.False(t, found, "a dirty completion must persist no whole-repo marker")
	})

	t.Run("empty sha", func(t *testing.T) {
		g := newMarkerStore(t)
		mgr.RecordRepoEnrichmentComplete(g, markerRepo, "", false)
		_, found, err := g.GetEnrichmentState(markerRepo, repoEnrichMarkerProvider)
		require.NoError(t, err)
		assert.False(t, found, "an empty sha must persist no whole-repo marker")
	})
}

// TestRepoEnrichmentMarker_MemoryBackendNotPersisted: a backend that does not
// implement EnrichmentStateStore (the in-memory graph) reports persisted=false,
// so the warm-restart seeder never forces a pass on marker evidence it cannot
// read.
func TestRepoEnrichmentMarker_MemoryBackendNotPersisted(t *testing.T) {
	mgr := NewManager(Config{Enabled: true}, zap.NewNop())
	g := graph.New()

	current, persisted := mgr.RepoEnrichmentMarkerState(g, markerRepo, "sha-1")
	assert.False(t, persisted, "memory backend does not persist enrichment state")
	assert.False(t, current)

	// The write is a safe no-op against a non-persisting backend.
	assert.NotPanics(t, func() {
		mgr.RecordRepoEnrichmentComplete(g, markerRepo, "sha-1", false)
	})
}
