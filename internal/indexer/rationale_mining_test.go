package indexer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

func TestMineDocSignal(t *testing.T) {
	require.Equal(t, "adr", mineDocSignal("## Decision\nWe chose batching."))
	require.Equal(t, "adr", mineDocSignal("implemented_by: pkg/x.go::Frob"))
	require.Equal(t, "adr", mineDocSignal("This supersedes the earlier approach."))
	require.Equal(t, "adr", mineDocSignal("See ADR-0007 for context."))
	require.Equal(t, "rfc2119", mineDocSignal("The handler MUST validate input."))
	require.Equal(t, "ticket", mineDocSignal("Closes PROJ-1234 by batching writes."))
	require.Equal(t, "lexical", mineDocSignal("This slide explains ProcessOrder."))
	require.Equal(t, "lexical", mineDocSignal(""))
}

func TestLinkContentToCode_UpgradesSignal(t *testing.T) {
	g := graph.New()
	g.AddBatch([]*graph.Node{
		{ID: "pkg/order.go::ProcessOrder", Kind: graph.KindFunction, Name: "ProcessOrder", FilePath: "pkg/order.go"},
		{ID: "adr.pptx::doc:slide-1", Kind: graph.KindDoc, FilePath: "adr.pptx",
			Meta: map[string]any{"data_class": "content", "asset_kind": "slide",
				"section_text": "## Decision\n\nWe keep ProcessOrder idempotent so retries are safe."}},
	}, nil)

	idx := New(g, parser.NewRegistry(), config.Default().Index, zap.NewNop())
	idx.linkContentToCode()

	var e *graph.Edge
	for _, edge := range g.AllEdges() {
		if edge.Kind == graph.EdgeMotivates {
			e = edge
			break
		}
	}
	require.NotNil(t, e)
	require.Equal(t, "adr", e.Meta["signal"], "a Decision/ADR chunk upgrades the link signal above lexical")
}
