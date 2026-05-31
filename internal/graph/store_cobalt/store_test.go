package store_cobalt_test

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cobalt"
	"github.com/zzet/gortex/internal/graph/storetest"
)

// newCobaltStore builds a fresh in-memory CobaltDB store for one
// conformance sub-test. In-memory keeps the suite fast and avoids the
// engine's per-database background schedulers (disk-only).
func newCobaltStore(t *testing.T) graph.Store {
	t.Helper()
	s, err := store_cobalt.Open(":memory:")
	if err != nil {
		t.Fatalf("open cobalt store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestCobaltStoreConformance runs the shared graph.Store contract suite
// against the CobaltDB backend.
func TestCobaltStoreConformance(t *testing.T) {
	storetest.RunConformance(t, newCobaltStore)
}

// TestCobaltBackendResolverConformance runs the BackendResolver contract
// suite. It skips automatically if the backend does not implement
// graph.BackendResolver.
func TestCobaltBackendResolverConformance(t *testing.T) {
	storetest.RunBackendResolverConformance(t, newCobaltStore)
}
