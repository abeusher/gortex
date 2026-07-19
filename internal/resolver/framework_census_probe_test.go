package resolver

import (
	"os"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
	store_sqlite "github.com/zzet/gortex/internal/graph/store_sqlite"
)

// TestFrameworkCensusProbe is a diagnostic, not a regression test: it times
// each component of the cold framework-synthesis census against a copied
// store, so a silent census regression is attributable without a cold run.
// Skipped unless GORTEX_BENCH_STORE points at a copied store file.
func TestFrameworkCensusProbe(t *testing.T) {
	path := os.Getenv("GORTEX_BENCH_STORE")
	if path == "" {
		t.Skip("set GORTEX_BENCH_STORE to a copied store.sqlite to run")
	}
	s, err := store_sqlite.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	t0 := time.Now()
	nodes := 0
	for range graph.NodesLightSeq(s) {
		nodes++
	}
	t.Logf("nodes light stream: %d rows in %s", nodes, time.Since(t0))

	t1 := time.Now()
	calls := 0
	withMeta := 0
	for e := range s.EdgesByKind(graph.EdgeCalls) {
		if e == nil {
			continue
		}
		calls++
		if e.Meta != nil {
			withMeta++
		}
	}
	t.Logf("EdgesByKind(calls) full stream: %d rows (%d with meta) in %s", calls, withMeta, time.Since(t1))

	t2 := time.Now()
	census := collectFrameworkEdgeCensus(s)
	t.Logf("collectFrameworkEdgeCensus: %s (via markers=%d)", time.Since(t2), len(census.via))

	t3 := time.Now()
	summary := summarizeFrameworkCandidatesCensus(s, nil, nil, true)
	t.Logf("summarizeFrameworkCandidatesCensus(full): %s (markers=%d, fullCensus=%v)",
		time.Since(t3), len(summary.allMarkers), summary.fullCensus)
}
