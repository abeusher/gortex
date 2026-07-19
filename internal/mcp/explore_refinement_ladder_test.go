package mcp

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// The single permitted refinement read must follow the answer-draft ladder,
// not raw rank: a generic rank-one type that shares no vocabulary with the
// query must not consume the read while an aligned callable sits lower in
// the same envelope.
func TestRefinementLadderPrefersAlignedCallableOverGenericHead(t *testing.T) {
	task := "replace trailing context lines in the printed output"
	genericHead := exploreTarget{node: &graph.Node{
		ID: "repo/printer/builder.go::OutputBuilder", Kind: graph.KindType, Name: "OutputBuilder", FilePath: "repo/printer/builder.go",
	}, score: 1.0}
	alignedFn := exploreTarget{node: &graph.Node{
		ID: "repo/printer/util.go::replace_with_context", Kind: graph.KindFunction, Name: "replace_with_context", FilePath: "repo/printer/util.go",
	}, score: 0.7}

	got := explorePreferredRefinementSymbol(task, []exploreTarget{genericHead, alignedFn})
	if got != alignedFn.node.ID {
		t.Fatalf("ladder picked %q, want the aligned callable", got)
	}

	literal := exploreTarget{node: &graph.Node{
		ID: "repo/printer/other.go::helper", Kind: graph.KindFunction, Name: "helper", FilePath: "repo/printer/other.go",
	}, score: 0.4, sourceLiteral: true}
	got = explorePreferredRefinementSymbol(task, []exploreTarget{genericHead, alignedFn, literal})
	if got != literal.node.ID {
		t.Fatalf("ladder picked %q, want the source-literal target", got)
	}

	unrelated := exploreTarget{node: &graph.Node{
		ID: "repo/other/thing.go::Widget", Kind: graph.KindType, Name: "Widget", FilePath: "repo/other/thing.go",
	}, score: 1.0}
	got = explorePreferredRefinementSymbol(task, []exploreTarget{unrelated})
	if got != unrelated.node.ID {
		t.Fatalf("ladder picked %q, want the raw head when nothing aligns", got)
	}
}
