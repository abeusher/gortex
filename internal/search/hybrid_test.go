package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRRFFuse(t *testing.T) {
	textResults := []SearchResult{
		{ID: "a", Score: 10},
		{ID: "b", Score: 8},
		{ID: "c", Score: 5},
	}
	vecIDs := []string{"b", "d", "a"}

	results := rrfFuse(textResults, vecIDs, 60, 10)
	require.GreaterOrEqual(t, len(results), 3)

	// "a" and "b" appear in both lists → highest RRF scores.
	// "b" is rank 1 in text, rank 0 in vec → should score high.
	topIDs := make([]string, len(results))
	for i, r := range results {
		topIDs[i] = r.ID
	}
	assert.Contains(t, topIDs[:2], "a", "a should be in top 2 (in both lists)")
	assert.Contains(t, topIDs[:2], "b", "b should be in top 2 (in both lists)")

	// All results should have non-zero scores.
	for _, r := range results {
		assert.Greater(t, r.Score, float64(0))
	}
}

func TestRRFFuse_EmptyVec(t *testing.T) {
	textResults := []SearchResult{
		{ID: "a", Score: 10},
		{ID: "b", Score: 8},
	}
	results := rrfFuse(textResults, nil, 60, 10)
	// With no vec results, only text results contribute.
	assert.Len(t, results, 2)
	assert.Equal(t, "a", results[0].ID)
}

func TestRRFFuse_Limit(t *testing.T) {
	textResults := []SearchResult{
		{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"},
	}
	vecIDs := []string{"f", "g", "h", "i", "j"}

	results := rrfFuse(textResults, vecIDs, 60, 3)
	assert.Len(t, results, 3)
}
