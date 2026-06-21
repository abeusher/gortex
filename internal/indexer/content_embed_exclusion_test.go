package indexer

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// TestCollectEmbedTexts_SkipsContent verifies C3: content (data_class=
// content) section nodes are excluded from the vector embedding pass, so a
// content-heavy repo's hundreds of thousands of sections never enter the
// embed-text count (nor trip the 100k auto-disable). Code symbols are
// embedded as before.
func TestCollectEmbedTexts_SkipsContent(t *testing.T) {
	idx := New(graph.New(), parser.NewRegistry(), config.Default().Index, zap.NewNop())

	nodes := []*graph.Node{
		{ID: "code1", Kind: graph.KindFunction, Name: "Foo", Language: "go"},
		{ID: "code2", Kind: graph.KindMethod, Name: "Bar", Language: "go"},
		{ID: "content1", Kind: graph.KindDoc, Name: "doc.txt::section-0",
			Meta: map[string]any{"data_class": "content", "section_text": "hello content world"}},
		{ID: "prose1", Kind: graph.KindDoc, Name: "README.md::section-0",
			Meta: map[string]any{"section_text": "markdown prose about code"}},
	}

	_, ids, _, skipped := idx.collectEmbedTexts(nodes)

	require.Contains(t, ids, "code1")
	require.Contains(t, ids, "code2")
	require.NotContains(t, ids, "content1",
		"content section node must be excluded from the embedding pass")
	require.Positive(t, skipped, "the content node must count as skipped")
	// Markdown prose (no data_class=content) is unaffected.
	require.Contains(t, ids, "prose1",
		"markdown prose must still be embedded")
}
