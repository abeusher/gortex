package search

import (
	"slices"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// fixtureGraph builds a small graph whose function names repeatedly
// pair the words "blast" and "radius", so co-occurrence mining should
// surface them as concept siblings.
func fixtureGraph(names []string) *graph.Graph {
	g := graph.New()
	for i, n := range names {
		g.AddNode(&graph.Node{
			ID:        "pkg/f.go::" + n,
			Kind:      graph.KindFunction,
			Name:      n,
			FilePath:  "pkg/f.go",
			StartLine: i + 1,
			EndLine:   i + 2,
			Language:  "go",
		})
	}
	return g
}

func TestBuildAutoConcepts_CoOccurrence(t *testing.T) {
	// "blast" + "radius" co-occur in 4 distinct symbol names -- well
	// over autoConceptMinPairCount.
	g := fixtureGraph([]string{
		"handleBlastRadius",
		"blastRadiusOf",
		"BlastRadiusReport",
		"computeBlastRadius",
		"unrelatedThing",
		"otherHelper",
	})
	ac := BuildAutoConcepts(g)

	radiusSibs := ac.Expand("blast")
	if !slices.Contains(radiusSibs, "radius") {
		t.Errorf("Expand(blast) should yield 'radius'; got %v", radiusSibs)
	}
	blastSibs := ac.Expand("radius")
	if !slices.Contains(blastSibs, "blast") {
		t.Errorf("Expand(radius) should yield 'blast' (symmetric); got %v", blastSibs)
	}

	// A token that never co-occurs with anything has no siblings.
	if ac.Expand("zzzneverpaired") != nil {
		t.Error("an unmined token should expand to nil")
	}
}

func TestBuildAutoConcepts_BelowThreshold(t *testing.T) {
	// "alpha" + "beta" co-occur only twice -- below the min-3 gate.
	g := fixtureGraph([]string{
		"alphaBetaOne",
		"alphaBetaTwo",
		"gammaDelta",
	})
	ac := BuildAutoConcepts(g)
	if slices.Contains(ac.Expand("alpha"), "beta") {
		t.Error("a pair below autoConceptMinPairCount must not become a concept")
	}
}

func TestBuildAutoConcepts_NilAndEmpty(t *testing.T) {
	if BuildAutoConcepts(nil).TokenCount() != 0 {
		t.Error("BuildAutoConcepts(nil) should be empty")
	}
	if BuildAutoConcepts(graph.New()).TokenCount() != 0 {
		t.Error("BuildAutoConcepts(empty graph) should be empty")
	}
	var ac *AutoConcepts
	if ac.Expand("x") != nil {
		t.Error("nil AutoConcepts.Expand should return nil")
	}
}

func TestBuildAutoConcepts_StopTokensExcluded(t *testing.T) {
	// "handle" is a stop-token; even though it appears with "payload"
	// in many names, it must not become a concept sibling.
	g := fixtureGraph([]string{
		"handlePayload", "handlePayloadV2", "handlePayloadAsync", "handlePayloadSync",
	})
	ac := BuildAutoConcepts(g)
	if slices.Contains(ac.Expand("payload"), "handle") {
		t.Error("stop-token 'handle' must not surface as a concept sibling")
	}
}
