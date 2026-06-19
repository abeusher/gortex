package mcp

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestAdaptiveSizingOffSpineSiblingSkeletonization exercises the flow-spine-
// aware manifest sizing decision: off-spine polymorphic siblings skeletonize,
// on-spine concrete symbols stay full, the family supertype skeletonizes even
// on-spine (the family-file override), and the focus order floats the
// skeletonizable symbols ahead of the unique ones.
func TestAdaptiveSizingOffSpineSiblingSkeletonization(t *testing.T) {
	t.Run("decision", func(t *testing.T) {
		cases := []struct {
			name    string
			kind    graph.NodeKind
			onSpine bool
			count   int
			want    bool
		}{
			{"supertype interface skeletonizes even on-spine (family override)", graph.KindInterface, true, 5, true},
			{"supertype interface off-spine skeletonizes", graph.KindInterface, false, 5, true},
			{"overridden parent method skeletonizes on-spine (family override)", graph.KindMethod, true, 5, true},
			{"off-spine concrete sibling skeletonizes", graph.KindType, false, 5, true},
			{"on-spine concrete symbol stays full (answer path)", graph.KindType, true, 5, false},
			{"sole/small family is never skeletonized", graph.KindType, false, 2, false},
			{"non-polymorphic kind is never skeletonized", graph.KindFunction, false, 5, false},
		}
		for _, c := range cases {
			if got := manifestShouldSkeletonize(c.kind, c.onSpine, c.count); got != c.want {
				t.Errorf("%s: manifestShouldSkeletonize(%s, onSpine=%v, count=%d) = %v, want %v",
					c.name, c.kind, c.onSpine, c.count, got, c.want)
			}
		}
	})

	t.Run("ordering_floats_skeletonizable_first", func(t *testing.T) {
		unique := &graph.Node{ID: "pkg/a.go::Handle", Kind: graph.KindFunction}      // unique, full
		onSpineType := &graph.Node{ID: "pkg/b.go::ConcreteA", Kind: graph.KindType}  // on-spine, full
		offSpineType := &graph.Node{ID: "pkg/c.go::ConcreteB", Kind: graph.KindType} // off-spine sibling, skeletonized
		supertype := &graph.Node{ID: "pkg/d.go::Store", Kind: graph.KindInterface}   // supertype, skeletonized

		onSpine := map[string]bool{onSpineType.ID: true}
		count := func(n *graph.Node) int {
			switch n.Kind {
			case graph.KindType, graph.KindInterface, graph.KindMethod:
				return 5 // large family
			default:
				return 0
			}
		}

		focus := []*graph.Node{unique, onSpineType, offSpineType, supertype}
		got := manifestOrderFocus(focus, onSpine, count)

		// The two skeletonizable symbols (off-spine sibling + supertype) come
		// first, in stable input order; the full-source ones follow.
		wantOrder := []string{offSpineType.ID, supertype.ID, unique.ID, onSpineType.ID}
		if len(got) != len(wantOrder) {
			t.Fatalf("reordered focus length = %d, want %d", len(got), len(wantOrder))
		}
		for i, n := range got {
			if n.ID != wantOrder[i] {
				t.Errorf("focus[%d] = %s, want %s (full order: %v)", i, n.ID, wantOrder[i], nodeIDList(got))
			}
		}
	})
}

func nodeIDList(nodes []*graph.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.ID
	}
	return out
}
