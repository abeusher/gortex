package indexer_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cobalt"
	"github.com/zzet/gortex/internal/graph/store_ladybug"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestBackendBench cold-indexes GORTEX_BENCH_ROOT through the full indexer
// pipeline (parse → extract → resolve) into the backend named by
// GORTEX_BENCH_BACKEND (memory | cobalt | ladybug), then runs a fixed query
// workload. It reports cold-index time, graph size, process RSS, and query
// throughput so the cobalt backend can be compared head-to-head with ladybug
// and the in-memory baseline on real repositories.
//
// Run one backend per invocation (clean per-process RSS):
//
//	GORTEX_BENCH_ROOT=/Users/zzet/code/my/gortex/gortex \
//	GORTEX_BENCH_BACKEND=cobalt \
//	  go test ./internal/indexer/ -run TestBackendBench -timeout 40m -v
func TestBackendBench(t *testing.T) {
	root := os.Getenv("GORTEX_BENCH_ROOT")
	if root == "" {
		t.Skip("bench harness; set GORTEX_BENCH_ROOT=<repo> and GORTEX_BENCH_BACKEND=memory|cobalt|ladybug")
	}
	if _, err := os.Stat(root); err != nil {
		t.Skipf("bench root not available: %v", err)
	}
	backendName := os.Getenv("GORTEX_BENCH_BACKEND")
	if backendName == "" {
		backendName = "memory"
	}

	store, cleanup := openBenchStore(t, backendName)
	defer cleanup()

	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	workers := runtime.NumCPU()
	if v := os.Getenv("GORTEX_BENCH_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			workers = n
		}
	}
	idx := indexer.New(store, reg, config.IndexConfig{Workers: workers}, zap.NewNop())

	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)

	start := time.Now()
	res, err := idx.IndexCtx(context.Background(), root)
	indexDur := time.Since(start)
	if err != nil {
		t.Fatalf("index: %v", err)
	}

	rssAfterIndex := processRSSMB()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	fmt.Fprintf(os.Stderr, ">>> %s INDEX DONE in %s (files=%d nodes=%d edges=%d) — starting query workload\n",
		backendName, indexDur.Round(time.Millisecond), res.FileCount, res.NodeCount, res.EdgeCount)

	qStart := time.Now()
	q := runQueryWorkload(store)
	fmt.Fprintf(os.Stderr, ">>> %s QUERY WORKLOAD DONE in %s\n", backendName, time.Since(qStart).Round(time.Millisecond))

	mb := func(b uint64) float64 { return float64(b) / (1024 * 1024) }
	t.Logf("================ BACKEND BENCH ================")
	t.Logf("backend=%s root=%s workers=%d", backendName, root, workers)
	t.Logf("cold index : %s  files=%d nodes=%d edges=%d errors=%d",
		indexDur.Round(time.Millisecond), res.FileCount, res.NodeCount, res.EdgeCount, len(res.Errors))
	if indexDur.Seconds() > 0 {
		t.Logf("throughput : %.0f files/s  %.0f nodes/s",
			float64(res.FileCount)/indexDur.Seconds(), float64(res.NodeCount)/indexDur.Seconds())
	}
	t.Logf("memory     : processRSS=%.0fMB  goHeapAlloc=%.0fMB  goTotalAlloc=%.0fMB",
		rssAfterIndex, mb(m1.HeapAlloc), mb(m1.TotalAlloc-m0.TotalAlloc))
	t.Logf("queries    : %s", q)
	t.Logf("==============================================")
	runtime.KeepAlive(store)
}

func openBenchStore(t *testing.T, name string) (graph.Store, func()) {
	t.Helper()
	switch strings.ToLower(name) {
	case "", "memory", "mem":
		return graph.New(), func() {}
	case "cobalt":
		s, err := store_cobalt.Open(filepath.Join(t.TempDir(), "bench.cobalt"))
		if err != nil {
			t.Fatalf("open cobalt: %v", err)
		}
		return s, func() { _ = s.Close() }
	case "ladybug", "lbug":
		s, err := store_ladybug.Open(filepath.Join(t.TempDir(), "bench.lbug"))
		if err != nil {
			t.Fatalf("open ladybug: %v", err)
		}
		return s, func() { _ = s.Close() }
	default:
		t.Fatalf("unknown GORTEX_BENCH_BACKEND %q (memory|cobalt|ladybug)", name)
		return nil, func() {}
	}
}

// runQueryWorkload times a fixed, deterministic read mix against the freshly
// indexed store: point lookups + adjacency over a node sample, exact-name
// lookups, substring search, Stats, and a full AllEdges scan.
func runQueryWorkload(store graph.Store) string {
	nodes := store.AllNodes()
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sample := sampleNodes(nodes, 2000)

	// Point lookups + both adjacency directions.
	ptStart := time.Now()
	ptOps := 0
	for _, n := range sample {
		store.GetNode(n.ID)
		store.GetOutEdges(n.ID)
		store.GetInEdges(n.ID)
		ptOps += 3
	}
	ptDur := time.Since(ptStart)

	// Exact-name lookups.
	nameStart := time.Now()
	nameOps := 0
	for _, n := range sample {
		if n.Name != "" {
			store.FindNodesByName(n.Name)
			nameOps++
		}
	}
	nameDur := time.Since(nameStart)

	// Substring search.
	subStart := time.Now()
	for _, frag := range []string{"Index", "resolve", "Store", "config", "handler"} {
		store.FindNodesByNameContaining(frag, 50)
	}
	subDur := time.Since(subStart)

	// Aggregate + full scan.
	statsStart := time.Now()
	st := store.Stats()
	statsDur := time.Since(statsStart)

	allStart := time.Now()
	allEdges := store.AllEdges()
	allDur := time.Since(allStart)

	opsPerSec := func(ops int, d time.Duration) float64 {
		if d <= 0 {
			return 0
		}
		return float64(ops) / d.Seconds()
	}
	return fmt.Sprintf(
		"sample=%d | point %d ops %s (%.0f op/s) | name %d ops %s (%.0f op/s) | substr 5q %s | Stats(%dn/%de) %s | AllEdges %d %s",
		len(sample),
		ptOps, ptDur.Round(time.Millisecond), opsPerSec(ptOps, ptDur),
		nameOps, nameDur.Round(time.Millisecond), opsPerSec(nameOps, nameDur),
		subDur.Round(time.Millisecond),
		st.TotalNodes, st.TotalEdges, statsDur.Round(time.Millisecond),
		len(allEdges), allDur.Round(time.Millisecond),
	)
}

// sampleNodes picks up to n nodes spread evenly across the (already sorted)
// slice so the workload is deterministic across backends.
func sampleNodes(nodes []*graph.Node, n int) []*graph.Node {
	if len(nodes) <= n {
		return nodes
	}
	step := len(nodes) / n
	out := make([]*graph.Node, 0, n)
	for i := 0; i < len(nodes) && len(out) < n; i += step {
		out = append(out, nodes[i])
	}
	return out
}

// processRSSMB returns the current process resident set size in MiB. It reads
// /proc on Linux and falls back to `ps` on macOS, so it captures native memory
// (ladybug's buffer pool) that Go's runtime.MemStats cannot see.
func processRSSMB() float64 {
	if b, err := os.ReadFile("/proc/self/statm"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 2 {
			if pages, err := strconv.ParseInt(f[1], 10, 64); err == nil {
				return float64(pages*int64(os.Getpagesize())) / (1024 * 1024)
			}
		}
	}
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(os.Getpid())).Output()
	if err == nil {
		if kb, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); err == nil {
			return float64(kb) / 1024
		}
	}
	return 0
}
