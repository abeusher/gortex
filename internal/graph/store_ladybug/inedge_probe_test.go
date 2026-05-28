package store_ladybug_test

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_ladybug"
)

// buildFanInStore seeds a fan-in graph (a, b, c → z) so the inbound
// traversal paths have something to find.
func buildFanInStore(t *testing.T) *store_ladybug.Store {
	t.Helper()
	dir := t.TempDir()
	s, err := store_ladybug.Open(filepath.Join(dir, "test.kuzu"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	for _, id := range []string{"a", "b", "c", "z"} {
		s.AddNode(&graph.Node{
			ID:       id,
			Name:     id,
			Kind:     graph.KindFunction,
			FilePath: id + ".go",
		})
	}
	for i, from := range []string{"a", "b", "c"} {
		s.AddEdge(&graph.Edge{
			From:     from,
			To:       "z",
			Kind:     graph.EdgeCalls,
			FilePath: from + ".go",
			Line:     i + 1,
		})
	}
	return s
}

// TestLadybugGetInEdges_InlinePropMatchesWhereClause probes a Cypher
// planner shape: inbound-edge lookup written as inline property
// match `(b:Node {id: $id})` on the arrow target vs. an outer
// `WHERE b.id = $id` clause. The two forms should be observationally
// identical; if they diverge on Ladybug the inbound path
// (find_usages / get_callers / analyze cycles / suggest_pattern)
// silently drops rows.
func TestLadybugGetInEdges_InlinePropMatchesWhereClause(t *testing.T) {
	s := buildFanInStore(t)
	in := s.GetInEdges("z")
	if got := len(in); got != 3 {
		t.Fatalf("GetInEdges(z) returned %d edges, want 3", got)
	}
	for _, e := range in {
		if e.To != "z" {
			t.Fatalf("GetInEdges(z) yielded edge with To=%q, want %q", e.To, "z")
		}
	}
}

// TestLadybugInDegreePushdowns probes the two reverse-direction Cypher
// pushdowns: the `COUNT { MATCH (:Node)-[:Edge]->(n) }` sub-query used
// by InDegreeForNodes / NodeDegreeByKinds, and the IN-list inbound
// match used by GetInEdgesByNodeIDs. Both feed the same hub-detection
// + degree-counting code paths the find_usages / get_callers /
// cycles / suggest_pattern analyzers rely on.
func TestLadybugInDegreePushdowns(t *testing.T) {
	s := buildFanInStore(t)

	t.Run("GetInEdgesByNodeIDs", func(t *testing.T) {
		got := s.GetInEdgesByNodeIDs([]string{"z"})
		if len(got["z"]) != 3 {
			t.Fatalf("GetInEdgesByNodeIDs(z) = %d edges, want 3", len(got["z"]))
		}
	})

	t.Run("InDegreeForNodes", func(t *testing.T) {
		got := s.InDegreeForNodes([]string{"z"})
		if c := got["z"]; c != 3 {
			t.Fatalf("InDegreeForNodes(z) = %d, want 3 (full map: %+v)", c, got)
		}
	})

	t.Run("NodeDegreeByKinds", func(t *testing.T) {
		rows := s.NodeDegreeByKinds([]graph.NodeKind{graph.KindFunction}, "")
		var zRow *graph.NodeDegreeRow
		for i := range rows {
			if rows[i].NodeID == "z" {
				zRow = &rows[i]
				break
			}
		}
		if zRow == nil {
			t.Fatalf("NodeDegreeByKinds did not return row for z; got %+v", rows)
		}
		if zRow.InCount != 3 {
			t.Fatalf("NodeDegreeByKinds(z).InCount = %d, want 3", zRow.InCount)
		}
	})

	t.Run("InEdgeCountsByKind", func(t *testing.T) {
		got := s.InEdgeCountsByKind([]graph.EdgeKind{graph.EdgeCalls})
		if c := got["z"]; c != 3 {
			t.Fatalf("InEdgeCountsByKind[calls][z] = %d, want 3 (full: %+v)", c, got)
		}
	})
}
