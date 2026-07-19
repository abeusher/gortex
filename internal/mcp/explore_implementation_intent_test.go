package mcp

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

// Neutral fixture: Storage.load is declared by an interface and implemented
// by DiskStorage and CloudStorage in separate files.
func newStorageFixtureServer(t *testing.T) (*Server, *graph.Graph) {
	t.Helper()
	g := graph.New()
	nodes := []*graph.Node{
		{ID: "repo/storage/storage.go::Storage", Kind: graph.KindInterface, Name: "Storage", FilePath: "repo/storage/storage.go", Language: "go"},
		{ID: "repo/storage/storage.go::Storage.load", Kind: graph.KindMethod, Name: "load", FilePath: "repo/storage/storage.go", Language: "go"},
		{ID: "repo/storage/disk.go::DiskStorage", Kind: graph.KindType, Name: "DiskStorage", FilePath: "repo/storage/disk.go", Language: "go"},
		{ID: "repo/storage/disk.go::DiskStorage.load", Kind: graph.KindMethod, Name: "load", FilePath: "repo/storage/disk.go", Language: "go"},
		{ID: "repo/storage/cloud.go::CloudStorage", Kind: graph.KindType, Name: "CloudStorage", FilePath: "repo/storage/cloud.go", Language: "go"},
		{ID: "repo/storage/cloud.go::CloudStorage.load", Kind: graph.KindMethod, Name: "load", FilePath: "repo/storage/cloud.go", Language: "go"},
	}
	edges := []*graph.Edge{
		{From: "repo/storage/storage.go::Storage.load", To: "repo/storage/storage.go::Storage", Kind: graph.EdgeMemberOf},
		{From: "repo/storage/disk.go::DiskStorage", To: "repo/storage/storage.go::Storage", Kind: graph.EdgeImplements},
		{From: "repo/storage/cloud.go::CloudStorage", To: "repo/storage/storage.go::Storage", Kind: graph.EdgeImplements},
		{From: "repo/storage/disk.go::DiskStorage.load", To: "repo/storage/disk.go::DiskStorage", Kind: graph.EdgeMemberOf},
		{From: "repo/storage/cloud.go::CloudStorage.load", To: "repo/storage/cloud.go::CloudStorage", Kind: graph.EdgeMemberOf},
	}
	g.AddBatch(nodes, edges)
	return NewServer(query.NewEngine(g), g, nil, nil, zap.NewNop(), nil), g
}

func TestExploreImplementationIntentVocabulary(t *testing.T) {
	positives := []string{
		"where is the concrete implementation of Storage.load",
		"find the classes that implement Storage",
		"which subclasses override load",
	}
	for _, task := range positives {
		if !exploreImplementationIntent(task) {
			t.Fatalf("intent not detected: %q", task)
		}
	}
	if exploreImplementationIntent("where is Storage.load declared") {
		t.Fatal("declaration query must not carry implementation intent")
	}
}

// An interface seed expands to its implementing types; an interface-member
// seed expands to the same-name concrete members. The abstract seed is never
// evicted and new files are bounded.
func TestExpandImplementationTargets(t *testing.T) {
	s, g := newStorageFixtureServer(t)
	seed := exploreTarget{node: g.GetNode("repo/storage/storage.go::Storage.load"), score: 1.0}

	expanded := s.expandImplementationTargets(context.Background(), []exploreTarget{seed})
	if expanded[0].node.ID != "repo/storage/storage.go::Storage.load" {
		t.Fatal("abstract seed must stay at its rank")
	}
	got := map[string]bool{}
	for _, tgt := range expanded[1:] {
		got[tgt.node.ID] = true
	}
	if !got["repo/storage/disk.go::DiskStorage.load"] || !got["repo/storage/cloud.go::CloudStorage.load"] {
		t.Fatalf("member seed must expand to same-name concrete members, got %v", got)
	}

	ifaceSeed := exploreTarget{node: g.GetNode("repo/storage/storage.go::Storage"), score: 1.0}
	expanded = s.expandImplementationTargets(context.Background(), []exploreTarget{ifaceSeed})
	got = map[string]bool{}
	for _, tgt := range expanded[1:] {
		got[tgt.node.ID] = true
	}
	if !got["repo/storage/disk.go::DiskStorage"] || !got["repo/storage/cloud.go::CloudStorage"] {
		t.Fatalf("interface seed must expand to implementing types, got %v", got)
	}
}

// Only-abstract evidence blocks answer_ready for implementation intent;
// the presence of one concrete implementor unblocks it, and non-intent
// queries are never touched.
func TestImplementationAnswerBlockedOnAbstractOnlyHead(t *testing.T) {
	s, g := newStorageFixtureServer(t)
	eng := s.engineFor(context.Background())
	task := "find the concrete implementation of Storage.load"
	abstractOnly := []exploreTarget{
		{node: g.GetNode("repo/storage/storage.go::Storage.load")},
		{node: g.GetNode("repo/storage/storage.go::Storage")},
	}
	if !exploreImplementationAnswerBlocked(task, abstractOnly, eng.GetOutEdges, eng.GetSymbol) {
		t.Fatal("abstract-only head must block answer_ready for implementation intent")
	}
	withConcrete := append(abstractOnly, exploreTarget{node: g.GetNode("repo/storage/disk.go::DiskStorage.load")})
	if exploreImplementationAnswerBlocked(task, withConcrete, eng.GetOutEdges, eng.GetSymbol) {
		t.Fatal("a concrete implementor in the head must unblock answer_ready")
	}
	if exploreImplementationAnswerBlocked("where is Storage.load declared", abstractOnly, eng.GetOutEdges, eng.GetSymbol) {
		t.Fatal("non-intent queries must be untouched")
	}
}
