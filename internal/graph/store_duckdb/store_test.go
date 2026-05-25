package store_duckdb_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_duckdb"
	"github.com/zzet/gortex/internal/graph/storetest"
)

func TestDuckDBStoreConformance(t *testing.T) {
	storetest.RunConformance(t, func(t *testing.T) graph.Store {
		dir := t.TempDir()
		s, err := store_duckdb.Open(filepath.Join(dir, "test.duckdb"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestDuckDBBackendResolverConformance(t *testing.T) {
	storetest.RunBackendResolverConformance(t, func(t *testing.T) graph.Store {
		dir := t.TempDir()
		s, err := store_duckdb.Open(filepath.Join(dir, "test.duckdb"))
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
