package indexer

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/intern"
)

// backingPtr returns the address of a string's backing array so two
// strings can be checked for *sharing storage* rather than merely being
// byte-equal. The empty string has no backing array.
func backingPtr(s string) uintptr {
	if len(s) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(unsafe.StringData(s)))
}

// freshString returns a heap copy of s with its own backing array, so
// equal values never share storage by accident. It models un-interned
// parse output, where every node holds a separate allocation of the
// same repetitive field value.
func freshString(s string) string {
	b := make([]byte, len(s))
	copy(b, s)
	return string(b)
}

// buildFreshNodes constructs files*perFile nodes (and one edge per node)
// where every repetitive field — file_path, language, kind — is a
// distinct backing array, as if freshly minted by the parser. The file
// path is identical within a file but a separate allocation on each node.
func buildFreshNodes(files, perFile int) ([]*graph.Node, []*graph.Edge) {
	nodes := make([]*graph.Node, 0, files*perFile)
	edges := make([]*graph.Edge, 0, files*perFile)
	for f := 0; f < files; f++ {
		path := fmt.Sprintf("internal/some/deeply/nested/pkg%d/source_file_%d.go", f, f)
		for k := 0; k < perFile; k++ {
			id := fmt.Sprintf("pkg%d/source_file_%d.go::Sym%d", f, f, k)
			next := fmt.Sprintf("pkg%d/source_file_%d.go::Sym%d", f, f, (k+1)%perFile)
			nodes = append(nodes, &graph.Node{
				ID:       id,
				Kind:     graph.NodeKind(freshString("function")),
				Name:     fmt.Sprintf("Sym%d", k),
				FilePath: freshString(path),
				Language: freshString("go"),
			})
			edges = append(edges, &graph.Edge{
				From:     id,
				To:       next,
				Kind:     graph.EdgeCalls,
				FilePath: freshString(path),
			})
		}
	}
	return nodes, edges
}

// internRepetitiveFields routes only the four repetitive node fields
// through the interner, mirroring what applyRepoPrefix does to them in
// isolation from per-node ID prefixing — used to measure the live-heap
// effect of the field interning without the ID table as a confound.
func internRepetitiveFields(nodes []*graph.Node, edges []*graph.Edge) {
	for _, n := range nodes {
		n.FilePath = intern.String(n.FilePath)
		n.Language = intern.String(n.Language)
		n.Kind = graph.NodeKind(intern.String(string(n.Kind)))
	}
	for _, e := range edges {
		e.FilePath = intern.String(e.FilePath)
	}
}

// applyRepoPrefixPerNodeBaseline reproduces the pre-cache behaviour:
// it concatenates prefix+FilePath for every node and edge, minting a
// throwaway string per reference. It exists only as the benchmark
// baseline the per-file cache improves on.
func applyRepoPrefixPerNodeBaseline(repoPrefix string, nodes []*graph.Node, edges []*graph.Edge) {
	if repoPrefix == "" {
		return
	}
	prefix := repoPrefix + "/"
	const unresolvedMarker = "unresolved::"
	for _, n := range nodes {
		n.ID = intern.String(prefix + n.ID)
		n.FilePath = intern.String(prefix + n.FilePath)
		n.RepoPrefix = repoPrefix
		n.Name = intern.String(n.Name)
		n.Language = intern.String(n.Language)
		n.Kind = graph.NodeKind(intern.String(string(n.Kind)))
	}
	for _, e := range edges {
		e.From = intern.String(prefix + e.From)
		if strings.HasPrefix(e.To, unresolvedMarker) {
			e.To = intern.String(e.To)
		} else {
			e.To = intern.String(prefix + e.To)
		}
		e.FilePath = intern.String(prefix + e.FilePath)
	}
}

// TestApplyRepoPrefix_InternsRepetitiveFields verifies that after the
// node-emit stamping pass the four repetitive fields keep their exact
// values AND that two nodes sharing a value share one backing array —
// proving the interning actually collapses the duplicates.
func TestApplyRepoPrefix_InternsRepetitiveFields(t *testing.T) {
	idx := &Indexer{}
	idx.SetRepoPrefix("myrepo")

	nodes := []*graph.Node{
		{ID: "a.go::A", Kind: graph.NodeKind(freshString("function")), Name: "A", FilePath: freshString("pkg/a.go"), Language: freshString("go")},
		{ID: "a.go::B", Kind: graph.NodeKind(freshString("function")), Name: "B", FilePath: freshString("pkg/a.go"), Language: freshString("go")},
	}
	edges := []*graph.Edge{
		{From: "a.go::A", To: "a.go::B", Kind: graph.EdgeCalls, FilePath: freshString("pkg/a.go")},
	}

	// Sanity: the two nodes hold genuinely distinct backing arrays for
	// the repetitive fields before interning, so a post-pass match is
	// meaningful rather than an accident of allocation.
	if backingPtr(nodes[0].FilePath) == backingPtr(nodes[1].FilePath) {
		t.Fatal("setup: file paths already share a backing array before interning")
	}
	if backingPtr(string(nodes[0].Kind)) == backingPtr(string(nodes[1].Kind)) {
		t.Fatal("setup: kinds already share a backing array before interning")
	}

	idx.applyRepoPrefix(nodes, edges)

	// Values are byte-identical to the unchanged-behaviour expectation.
	const wantPath = "myrepo/pkg/a.go"
	wantID := []string{"myrepo/a.go::A", "myrepo/a.go::B"}
	for i, n := range nodes {
		if n.ID != wantID[i] {
			t.Fatalf("node %d ID = %q, want %q", i, n.ID, wantID[i])
		}
		if n.FilePath != wantPath {
			t.Fatalf("node %d FilePath = %q, want %q", i, n.FilePath, wantPath)
		}
		if string(n.Kind) != "function" {
			t.Fatalf("node %d Kind = %q, want %q", i, n.Kind, "function")
		}
		if n.Language != "go" {
			t.Fatalf("node %d Language = %q, want %q", i, n.Language, "go")
		}
		if n.RepoPrefix != "myrepo" {
			t.Fatalf("node %d RepoPrefix = %q, want %q", i, n.RepoPrefix, "myrepo")
		}
	}
	if edges[0].From != "myrepo/a.go::A" || edges[0].To != "myrepo/a.go::B" {
		t.Fatalf("edge endpoints = %q -> %q, want myrepo/a.go::A -> myrepo/a.go::B", edges[0].From, edges[0].To)
	}
	if edges[0].FilePath != wantPath {
		t.Fatalf("edge FilePath = %q, want %q", edges[0].FilePath, wantPath)
	}

	// Identity: equal values now share exactly one backing array.
	if backingPtr(nodes[0].FilePath) != backingPtr(nodes[1].FilePath) {
		t.Fatal("FilePath not interned: nodes hold distinct backing arrays after the pass")
	}
	if backingPtr(string(nodes[0].Kind)) != backingPtr(string(nodes[1].Kind)) {
		t.Fatal("Kind not interned: nodes hold distinct backing arrays after the pass")
	}
	if backingPtr(nodes[0].Language) != backingPtr(nodes[1].Language) {
		t.Fatal("Language not interned: nodes hold distinct backing arrays after the pass")
	}
	if backingPtr(nodes[0].RepoPrefix) != backingPtr(nodes[1].RepoPrefix) {
		t.Fatal("RepoPrefix not shared: nodes hold distinct backing arrays after the pass")
	}
	// The edge's file path shares storage with the node's — the per-file
	// cache hands both the same interned instance.
	if backingPtr(edges[0].FilePath) != backingPtr(nodes[0].FilePath) {
		t.Fatal("edge FilePath not interned to the same backing array as the node FilePath")
	}
}

// TestApplyRepoPrefix_PerFileCacheMatchesPerNode pins the invariant that
// the per-file cache produces byte-identical results to the per-node
// concatenation it replaces, for both nodes and edges.
func TestApplyRepoPrefix_PerFileCacheMatchesPerNode(t *testing.T) {
	cachedNodes, cachedEdges := buildFreshNodes(3, 4)
	baseNodes, baseEdges := buildFreshNodes(3, 4)

	idx := &Indexer{}
	idx.SetRepoPrefix("repo")
	idx.applyRepoPrefix(cachedNodes, cachedEdges)
	applyRepoPrefixPerNodeBaseline("repo", baseNodes, baseEdges)

	if len(cachedNodes) != len(baseNodes) {
		t.Fatalf("node count drift: cached=%d base=%d", len(cachedNodes), len(baseNodes))
	}
	for i := range cachedNodes {
		if cachedNodes[i].FilePath != baseNodes[i].FilePath {
			t.Fatalf("node %d FilePath: cached=%q base=%q", i, cachedNodes[i].FilePath, baseNodes[i].FilePath)
		}
		if cachedNodes[i].ID != baseNodes[i].ID {
			t.Fatalf("node %d ID: cached=%q base=%q", i, cachedNodes[i].ID, baseNodes[i].ID)
		}
		if cachedNodes[i].Kind != baseNodes[i].Kind {
			t.Fatalf("node %d Kind: cached=%q base=%q", i, cachedNodes[i].Kind, baseNodes[i].Kind)
		}
	}
	for i := range cachedEdges {
		if cachedEdges[i].FilePath != baseEdges[i].FilePath {
			t.Fatalf("edge %d FilePath: cached=%q base=%q", i, cachedEdges[i].FilePath, baseEdges[i].FilePath)
		}
		if cachedEdges[i].From != baseEdges[i].From || cachedEdges[i].To != baseEdges[i].To {
			t.Fatalf("edge %d endpoints differ", i)
		}
	}
}

// BenchmarkApplyRepoPrefix_PerFileCache measures the production stamping
// pass; the file path is interned once per file via the cache.
func BenchmarkApplyRepoPrefix_PerFileCache(b *testing.B) {
	idx := &Indexer{}
	idx.SetRepoPrefix("benchrepo")
	benchPrefixPass(b, func(nodes []*graph.Node, edges []*graph.Edge) {
		idx.applyRepoPrefix(nodes, edges)
	})
}

// BenchmarkApplyRepoPrefix_PerNodeConcat measures the pre-cache baseline
// that concatenates prefix+path for every node and edge.
func BenchmarkApplyRepoPrefix_PerNodeConcat(b *testing.B) {
	benchPrefixPass(b, func(nodes []*graph.Node, edges []*graph.Edge) {
		applyRepoPrefixPerNodeBaseline("benchrepo", nodes, edges)
	})
}

func benchPrefixPass(b *testing.B, apply func([]*graph.Node, []*graph.Edge)) {
	const files, perFile = 40, 500
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		nodes, edges := buildFreshNodes(files, perFile)
		b.StartTimer()
		apply(nodes, edges)
	}
}

// BenchmarkNodeFields_LiveHeap_Interned and _NotInterned report the
// resident heap of a 100k-node corpus with vs without interning the
// repetitive fields, so the live-set reduction is directly observable
// as the live-heap-bytes custom metric.
func BenchmarkNodeFields_LiveHeap_Interned(b *testing.B)    { benchLiveHeap(b, true) }
func BenchmarkNodeFields_LiveHeap_NotInterned(b *testing.B) { benchLiveHeap(b, false) }

func benchLiveHeap(b *testing.B, interned bool) {
	const files, perFile = 50, 2000 // 100k nodes / 100k edges
	var lastHeap float64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodes, edges := buildFreshNodes(files, perFile)
		if interned {
			internRepetitiveFields(nodes, edges)
		}
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		lastHeap = float64(ms.HeapAlloc)
		runtime.KeepAlive(nodes)
		runtime.KeepAlive(edges)
	}
	b.ReportMetric(lastHeap, "live-heap-bytes")
}
