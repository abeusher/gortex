package store_cobalt_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cobalt"
	"github.com/zzet/gortex/internal/resolver"
)

// TestCobaltSubstringLiteralMatch guards against LIKE-metacharacter
// leakage: FindNodesByNameContaining must match the literal substring
// (parity with the in-memory strings.Contains), so an underscore is a
// literal underscore, not a single-char wildcard.
func TestCobaltSubstringLiteralMatch(t *testing.T) {
	s, err := store_cobalt.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	fn := graph.NodeKind("function")
	for _, name := range []string{"my_func", "myXfunc", "myfunc", "other_my_func_2"} {
		s.AddNode(&graph.Node{ID: "f.go::" + name, Kind: fn, Name: name, FilePath: "f.go", Language: "go"})
	}

	got := s.FindNodesByNameContaining("my_func", 0)
	names := map[string]bool{}
	for _, n := range got {
		names[n.Name] = true
	}
	if names["myXfunc"] {
		t.Errorf("'_' was treated as a wildcard: 'my_func' matched 'myXfunc'")
	}
	if !names["my_func"] || !names["other_my_func_2"] {
		t.Errorf("literal substring match incomplete; got %v", names)
	}
	if len(got) != 2 {
		t.Errorf("FindNodesByNameContaining(\"my_func\") = %d results, want 2 (got %v)", len(got), names)
	}
}

// TestCobaltDiskPersistence exercises the on-disk path the daemon uses:
// open a file-backed store, write, close, reopen, and confirm the data
// survives and the schema re-applies idempotently (no CREATE collision).
func TestCobaltDiskPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.cobalt")

	s, err := store_cobalt.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	fn := graph.NodeKind("function")
	s.AddNode(&graph.Node{ID: "x.go::Foo", Kind: fn, Name: "Foo", FilePath: "x.go", Language: "go"})
	s.AddEdge(&graph.Edge{From: "x.go::Foo", To: "x.go::Bar", Kind: graph.EdgeCalls, FilePath: "x.go", Line: 1})
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := store_cobalt.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if n := s2.GetNode("x.go::Foo"); n == nil || n.Name != "Foo" {
		t.Fatalf("GetNode after reopen = %+v, want Foo", n)
	}
	if out := s2.GetOutEdges("x.go::Foo"); len(out) != 1 {
		t.Fatalf("GetOutEdges after reopen = %d, want 1", len(out))
	}
}

// TestCobaltWithGoResolver drives the real Go-side resolver against a
// CobaltDB store end to end. The store does not implement
// graph.BackendResolver, so this exercises the fallback path the daemon
// uses for cobalt: the resolver walks unresolved edges and rebinds them
// through the core Store methods (EdgesWithUnresolvedTarget,
// FindNodesByName, ReindexEdge/SetEdgeProvenance). It proves cobalt is a
// functional indexing+serving backend, not just conformance-correct.
func TestCobaltWithGoResolver(t *testing.T) {
	s, err := store_cobalt.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	const repo = "myrepo"
	fn := graph.NodeKind("function")
	caller := &graph.Node{ID: "pkg/a.go::Caller", Kind: fn, Name: "Caller", FilePath: "pkg/a.go", RepoPrefix: repo, Language: "go", StartLine: 1, EndLine: 3}
	target := &graph.Node{ID: "pkg/a.go::Target", Kind: fn, Name: "Target", FilePath: "pkg/a.go", RepoPrefix: repo, Language: "go", StartLine: 5, EndLine: 7}
	// An unresolved call from Caller to a symbol named "Target".
	edge := &graph.Edge{From: caller.ID, To: "unresolved::Target", Kind: graph.EdgeCalls, FilePath: "pkg/a.go", Line: 2, Confidence: 0.5}
	s.AddBatch([]*graph.Node{caller, target}, []*graph.Edge{edge})

	// Pre-condition: exactly one unresolved edge.
	pre := 0
	for range s.EdgesWithUnresolvedTarget() {
		pre++
	}
	if pre != 1 {
		t.Fatalf("pre-resolve unresolved edges = %d, want 1", pre)
	}

	stats := resolver.New(s).ResolveAll()
	t.Logf("resolve stats: %+v", stats)

	// Post-condition 1: no unresolved edges remain.
	post := 0
	for range s.EdgesWithUnresolvedTarget() {
		post++
	}
	if post != 0 {
		t.Errorf("post-resolve unresolved edges = %d, want 0", post)
	}

	// Post-condition 2: Caller now has a calls edge to the real Target id.
	out := s.GetOutEdges(caller.ID)
	found := false
	for _, e := range out {
		if e.Kind == graph.EdgeCalls && e.To == target.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("Caller's call edge did not resolve to %q; out edges = %+v", target.ID, out)
	}

	// Post-condition 3: the resolved edge is visible from Target's in-edges.
	if in := s.GetInEdges(target.ID); len(in) == 0 {
		t.Errorf("Target has no in-edges after resolve, want the resolved call")
	}

	// Post-condition 4: total counts are consistent (2 nodes, 1 edge).
	if s.NodeCount() != 2 {
		t.Errorf("NodeCount = %d, want 2", s.NodeCount())
	}
	if s.EdgeCount() != 1 {
		t.Errorf("EdgeCount = %d, want 1", s.EdgeCount())
	}
}
