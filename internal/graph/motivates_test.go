package graph

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMotivatesKinds(t *testing.T) {
	require.Equal(t, NodeKind("rationale"), KindRationale)
	require.Equal(t, EdgeKind("motivates"), EdgeMotivates)
	require.Equal(t, EdgeKind("cross_repo_motivates"), EdgeCrossRepoMotivates)
}

func TestCrossRepoMotivatesRegistered(t *testing.T) {
	cr, ok := CrossRepoKindFor(EdgeMotivates)
	require.True(t, ok, "EdgeMotivates must have a cross-repo parallel")
	require.Equal(t, EdgeCrossRepoMotivates, cr)

	base, ok := BaseKindForCrossRepo(EdgeCrossRepoMotivates)
	require.True(t, ok)
	require.Equal(t, EdgeMotivates, base)

	require.Contains(t, BaseKindsForCrossRepo(), EdgeMotivates,
		"DetectCrossRepoEdges must materialise the motivates parallel")
}
