package indexer_test

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"sort"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

// TestDiagLargestRows indexes GORTEX_BENCH_ROOT in memory and reports the
// nodes/edges with the largest serialized row size (the metric that decides a
// CobaltDB WAL record's length), so the row that blew past the 64KiB WAL cap
// can be identified by id/kind/file and meta breakdown.
//
//	GORTEX_BENCH_ROOT=/Users/zzet/code/my/gortex/gortex \
//	  go test ./internal/indexer/ -run TestDiagLargestRows -v
func TestDiagLargestRows(t *testing.T) {
	root := os.Getenv("GORTEX_BENCH_ROOT")
	if root == "" {
		t.Skip("set GORTEX_BENCH_ROOT=<repo>")
	}
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, config.IndexConfig{Workers: runtime.NumCPU()}, zap.NewNop())
	if _, err := idx.IndexCtx(context.Background(), root); err != nil {
		t.Fatal(err)
	}

	type rowInfo struct {
		id, kind, file   string
		total, metaBytes int
	}
	nodeRowSize := func(n *graph.Node) (int, int) {
		meta, _ := json.Marshal(n.Meta)
		total := len(n.ID) + len(string(n.Kind)) + len(n.Name) + len(n.QualName) +
			len(n.FilePath) + len(n.Language) + len(n.RepoPrefix) + len(n.WorkspaceID) +
			len(n.ProjectID) + len(meta)
		return total, len(meta)
	}

	var rows []rowInfo
	over64k := 0
	var biggest *graph.Node
	biggestSize := 0
	for _, n := range g.AllNodes() {
		total, metaBytes := nodeRowSize(n)
		rows = append(rows, rowInfo{n.ID, string(n.Kind), n.FilePath, total, metaBytes})
		if total > 65535 {
			over64k++
		}
		if total > biggestSize {
			biggestSize = total
			biggest = n
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].total > rows[j].total })

	t.Logf("nodes=%d  rows over 64KiB=%d", len(rows), over64k)
	t.Logf("--- top 12 nodes by row size ---")
	for i := 0; i < 12 && i < len(rows); i++ {
		r := rows[i]
		id := r.id
		if len(id) > 70 {
			id = id[:70] + "…"
		}
		t.Logf("#%-2d total=%-7d meta=%-7d kind=%-10s file=%s\n         id=%s", i+1, r.total, r.metaBytes, r.kind, r.file, id)
	}

	// Break down the meta of the biggest node by key → value byte size.
	if biggest != nil && len(biggest.Meta) > 0 {
		t.Logf("--- meta breakdown of biggest node (%s) ---", biggest.ID)
		type kv struct {
			k    string
			size int
		}
		var kvs []kv
		for k, v := range biggest.Meta {
			b, _ := json.Marshal(v)
			kvs = append(kvs, kv{k, len(b)})
		}
		sort.Slice(kvs, func(i, j int) bool { return kvs[i].size > kvs[j].size })
		for _, e := range kvs {
			t.Logf("    meta[%q] = %d bytes", e.k, e.size)
		}
	}

	// Edges too (meta is usually small, but verify).
	maxEdge := 0
	var maxE *graph.Edge
	for _, e := range g.AllEdges() {
		meta, _ := json.Marshal(e.Meta)
		sz := len(e.From) + len(e.To) + len(string(e.Kind)) + len(e.FilePath) +
			len(e.Origin) + len(e.Tier) + len(e.ConfidenceLabel) + len(meta)
		if sz > maxEdge {
			maxEdge = sz
			maxE = e
		}
	}
	if maxE != nil {
		t.Logf("--- biggest edge: total=%d  %s -%s-> %s ---", maxEdge, maxE.From, maxE.Kind, maxE.To)
	}
}
