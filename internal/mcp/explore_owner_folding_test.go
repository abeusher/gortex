package mcp

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
)

// Neutral persist-middleware shape: several members of one options type rank
// above the type itself; folding promotes the owner ahead of its first
// member without evicting anything.
func newOwnerFoldFixture(t *testing.T) (*Server, *graph.Graph) {
	t.Helper()
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "repo/mw/persist.ts::PersistOptions", Kind: graph.KindType, Name: "PersistOptions", FilePath: "repo/mw/persist.ts", Language: "typescript"},
		{ID: "repo/mw/persist.ts::PersistOptions.serialize", Kind: graph.KindMethod, Name: "serialize", FilePath: "repo/mw/persist.ts", Language: "typescript"},
		{ID: "repo/mw/persist.ts::PersistOptions.hydrate", Kind: graph.KindMethod, Name: "hydrate", FilePath: "repo/mw/persist.ts", Language: "typescript"},
		{ID: "repo/mw/other.ts::unrelated", Kind: graph.KindFunction, Name: "unrelated", FilePath: "repo/mw/other.ts", Language: "typescript"},
	}, []*graph.Edge{
		{From: "repo/mw/persist.ts::PersistOptions.serialize", To: "repo/mw/persist.ts::PersistOptions", Kind: graph.EdgeMemberOf},
		{From: "repo/mw/persist.ts::PersistOptions.hydrate", To: "repo/mw/persist.ts::PersistOptions", Kind: graph.EdgeMemberOf},
	})
	return NewServer(query.NewEngine(g), g, nil, nil, zap.NewNop(), nil), g
}

func TestFoldMemberOwnersPromotesSharedOwner(t *testing.T) {
	s, g := newOwnerFoldFixture(t)
	targets := []exploreTarget{
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions.serialize"), score: 1.0},
		{node: g.GetNode("repo/mw/other.ts::unrelated"), score: 0.9},
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions.hydrate"), score: 0.8},
	}
	folded := s.foldMemberOwners(context.Background(), targets)
	if folded[0].node.ID != "repo/mw/persist.ts::PersistOptions" {
		t.Fatalf("owner not promoted ahead of first member: head=%s", folded[0].node.ID)
	}
	if len(folded) != len(targets)+1 {
		t.Fatalf("folding must not evict members: len=%d", len(folded))
	}

	// One member alone must not fold its owner.
	single := []exploreTarget{
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions.serialize"), score: 1.0},
		{node: g.GetNode("repo/mw/other.ts::unrelated"), score: 0.9},
	}
	unfolded := s.foldMemberOwners(context.Background(), single)
	if unfolded[0].node.ID != "repo/mw/persist.ts::PersistOptions.serialize" {
		t.Fatal("a lone member must not trigger folding")
	}

	// An owner already leading its members stays put; no duplicate appears.
	led := []exploreTarget{
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions"), score: 1.0},
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions.serialize"), score: 0.9},
		{node: g.GetNode("repo/mw/persist.ts::PersistOptions.hydrate"), score: 0.8},
	}
	same := s.foldMemberOwners(context.Background(), led)
	if len(same) != 3 || same[0].node.ID != "repo/mw/persist.ts::PersistOptions" {
		t.Fatalf("leading owner must be untouched, got %d entries head=%s", len(same), same[0].node.ID)
	}
}

// A same-file helper named in a wrapper body is promotable into the answer
// draft as a callee of the ranked wrapper — the generic wrapper-to-helper
// shape. Verified covered by the existing draft promotion; pinned so
// selection work cannot regress it.
func TestDraftPromotesWrapperHelperCallee(t *testing.T) {
	task := "case insensitive path lookup returns the wrong file"
	wrapper := &graph.Node{ID: "repo/fs/path.go::findCaseInsensitivePath", Kind: graph.KindFunction,
		Name: "findCaseInsensitivePath", FilePath: "repo/fs/path.go"}
	helper := &graph.Node{ID: "repo/fs/path.go::findCaseInsensitivePathRec", Kind: graph.KindFunction,
		Name: "findCaseInsensitivePathRec", FilePath: "repo/fs/path.go"}
	targets := []exploreTarget{{
		node: wrapper, score: 1.0,
		source:  "func findCaseInsensitivePath(p string) string { return findCaseInsensitivePathRec(p, 0) }",
		callees: []*graph.Node{helper},
	}}
	found := false
	for _, entry := range exploreAnswerDraft(task, targets) {
		if entry.node != nil && entry.node.ID == helper.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("wrapper body helper must be promotable into the answer draft")
	}
}
