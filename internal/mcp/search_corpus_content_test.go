package mcp

import (
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestParseCorpus_Content(t *testing.T) {
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"corpus": "content"}
	c, err := parseCorpus(req)
	require.NoError(t, err)
	require.Equal(t, corpusContent, c)
}

func TestParseCorpus_InvalidMentionsContent(t *testing.T) {
	req := mcpgo.CallToolRequest{}
	req.Params.Arguments = map[string]any{"corpus": "bogus"}
	_, err := parseCorpus(req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "content")
}

func TestFilterNodesByCorpus_Content(t *testing.T) {
	fn := &graph.Node{ID: "fn", Kind: graph.KindFunction}
	md := &graph.Node{ID: "md", Kind: graph.KindDoc, Meta: map[string]any{"asset_kind": "markdown_section"}}
	pdf := &graph.Node{ID: "pdf", Kind: graph.KindDoc, Meta: map[string]any{"data_class": "content", "asset_kind": "pdf_page"}}
	all := []*graph.Node{fn, md, pdf}

	ids := func(ns []*graph.Node) []string {
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}

	require.Equal(t, []string{"pdf"}, ids(filterNodesByCorpus(all, corpusContent)),
		"content corpus keeps only data_class=content chunks, not Markdown prose")
	require.Equal(t, []string{"fn"}, ids(filterNodesByCorpus(all, corpusCode)),
		"code corpus excludes every KindDoc, content included")
	require.ElementsMatch(t, []string{"md", "pdf"}, ids(filterNodesByCorpus(all, corpusDocs)))
	require.Len(t, filterNodesByCorpus(all, corpusAll), 3)
}

func TestIsContentNode(t *testing.T) {
	require.True(t, isContentNode(&graph.Node{Meta: map[string]any{"data_class": "content"}}))
	require.False(t, isContentNode(&graph.Node{Meta: map[string]any{"data_class": "data"}}))
	require.False(t, isContentNode(&graph.Node{Meta: map[string]any{}}))
	require.False(t, isContentNode(&graph.Node{}))
}
