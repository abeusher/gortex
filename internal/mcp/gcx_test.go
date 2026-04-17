package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/wire"
)

func newTestNode(id, name string, kind graph.NodeKind, path string, line int) *graph.Node {
	return &graph.Node{
		ID:        id,
		Name:      name,
		Kind:      kind,
		FilePath:  path,
		StartLine: line,
		EndLine:   line + 5,
		Meta:      map[string]any{"signature": "func " + name + "()"},
	}
}

func TestEncodeSearchSymbols_HeaderAndRows(t *testing.T) {
	nodes := []*graph.Node{
		newTestNode("a.go::Foo", "Foo", graph.KindFunction, "a.go", 10),
		newTestNode("b.go::Bar", "Bar", graph.KindMethod, "b.go", 20),
	}
	payload, err := encodeSearchSymbols(nodes, 2, 10)
	require.NoError(t, err)

	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, err := dec.Header()
	require.NoError(t, err)
	require.Equal(t, "search_symbols", h.Tool)
	require.Equal(t, []string{"id", "kind", "name", "path", "line", "sig"}, h.Fields)
	require.Equal(t, "2", h.Meta["total"])
	require.Equal(t, "false", h.Meta["truncated"])

	rows, err := dec.All()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, "a.go::Foo", rows[0]["id"])
	require.Equal(t, "function", rows[0]["kind"])
	require.Equal(t, "Foo", rows[0]["name"])
	require.Equal(t, "10", rows[0]["line"])
	require.Equal(t, "func Foo()", rows[0]["sig"])
}

func TestEncodeSearchSymbols_RespectsLimitAndTruncation(t *testing.T) {
	nodes := make([]*graph.Node, 5)
	for i := range nodes {
		nodes[i] = newTestNode("x.go::N", "N", graph.KindFunction, "x.go", i)
	}
	payload, err := encodeSearchSymbols(nodes, 5, 3)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, _ := dec.Header()
	require.Equal(t, "true", h.Meta["truncated"])
	rows, _ := dec.All()
	require.Len(t, rows, 3)
}

func TestEncodeSearchSymbols_SkipsFileAndImport(t *testing.T) {
	nodes := []*graph.Node{
		newTestNode("f.go", "f.go", graph.KindFile, "f.go", 1),
		newTestNode("f.go::Foo", "Foo", graph.KindFunction, "f.go", 5),
		newTestNode("f.go::imp", "imp", graph.KindImport, "f.go", 2),
	}
	payload, err := encodeSearchSymbols(nodes, 3, 10)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	_, _ = dec.Header()
	rows, _ := dec.All()
	require.Len(t, rows, 1)
	require.Equal(t, "Foo", rows[0]["name"])
}

func TestEncodeGetSymbolSource_EmbeddedNewlinesRoundTrip(t *testing.T) {
	node := newTestNode("f.go::Foo", "Foo", graph.KindFunction, "f.go", 10)
	src := "func Foo() {\n\tfmt.Println(\"x\\ty\")\n}"
	payload, err := encodeGetSymbolSource(node, src, 9, "etag123")
	require.NoError(t, err)

	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, err := dec.Header()
	require.NoError(t, err)
	require.Equal(t, "get_symbol_source", h.Tool)
	require.Equal(t, "etag123", h.Meta["etag"])

	rows, err := dec.All()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, src, rows[0]["source"])
	require.Equal(t, "9", rows[0]["from_line"])
	require.Equal(t, "etag123", rows[0]["etag"])
}

func TestEncodeBatchSymbols_IncludeSource(t *testing.T) {
	rows := []map[string]any{
		{
			"id":         "a.go::Foo",
			"kind":       graph.KindFunction,
			"name":       "Foo",
			"file_path":  "a.go",
			"start_line": 10,
			"end_line":   20,
			"signature":  "func Foo()",
			"source":     "func Foo() {}",
		},
		{
			"id":    "x.go::Missing",
			"error": "symbol not found",
		},
	}
	payload, err := encodeBatchSymbols(rows, true)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, err := dec.Header()
	require.NoError(t, err)
	require.Contains(t, h.Fields, "source")
	require.Contains(t, h.Fields, "error")
	got, err := dec.All()
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, "func Foo()", got[0]["sig"])
	require.Equal(t, "symbol not found", got[1]["error"])
}

func TestEncodeSubGraph_NodesAndEdgesSections(t *testing.T) {
	sg := &query.SubGraph{
		Nodes: []*graph.Node{
			newTestNode("a.go::Foo", "Foo", graph.KindFunction, "a.go", 10),
			newTestNode("b.go::Bar", "Bar", graph.KindFunction, "b.go", 20),
		},
		Edges: []*graph.Edge{
			{From: "a.go::Foo", To: "b.go::Bar", Kind: "calls", Confidence: 0.9, Origin: "ast_resolved"},
		},
		TotalNodes: 2,
	}
	payload, err := encodeSubGraph("get_callers", sg)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))

	h1, err := dec.Header()
	require.NoError(t, err)
	require.Equal(t, "get_callers.nodes", h1.Tool)
	rows, err := dec.All()
	require.NoError(t, err)
	require.Len(t, rows, 2)

	h2, err := dec.NextSection()
	require.NoError(t, err)
	require.Equal(t, "get_callers.edges", h2.Tool)
	edges, err := dec.All()
	require.NoError(t, err)
	require.Len(t, edges, 1)
	require.Equal(t, "calls", edges[0]["kind"])
	require.Equal(t, "ast_resolved", edges[0]["origin"])
}

func TestEncodeFindUsages_OneRowPerEdge(t *testing.T) {
	sg := &query.SubGraph{
		Nodes: []*graph.Node{
			newTestNode("a.go::Caller", "Caller", graph.KindFunction, "a.go", 10),
			newTestNode("b.go::Target", "Target", graph.KindFunction, "b.go", 20),
		},
		Edges: []*graph.Edge{
			{From: "a.go::Caller", To: "b.go::Target", Kind: "calls", Origin: "lsp_resolved", Confidence: 1.0},
		},
	}
	payload, err := encodeFindUsages(sg)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	_, err = dec.Header()
	require.NoError(t, err)
	rows, err := dec.All()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "a.go::Caller", rows[0]["from"])
	require.Equal(t, "b.go::Target", rows[0]["to"])
	require.Equal(t, "Caller", rows[0]["from_name"])
	require.Equal(t, "10", rows[0]["from_line"])
}

func TestEncodeAnalyze_DeadCode(t *testing.T) {
	items := []deadCodeItem{
		{ID: "a.go::Unused", Kind: "function", Name: "Unused", Path: "a.go", Line: 42, Reason: "no incoming edges"},
	}
	payload, err := encodeAnalyze("dead_code", items)
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, _ := dec.Header()
	require.Equal(t, "analyze.dead_code", h.Tool)
	rows, _ := dec.All()
	require.Len(t, rows, 1)
	require.Equal(t, "Unused", rows[0]["name"])
	require.Equal(t, "no incoming edges", rows[0]["reason"])
}

func TestEncodeAnalyze_UnknownKindFallsBackToGeneric(t *testing.T) {
	payload, err := encodeAnalyze("weird", map[string]any{"x": 1})
	require.NoError(t, err)
	dec := wire.NewDecoder(strings.NewReader(string(payload)))
	h, err := dec.Header()
	require.NoError(t, err)
	require.Equal(t, "analyze.weird", h.Tool)
}

func TestRequestedFormat_CoversCompactAndFormatArgs(t *testing.T) {
	f := wire.ParseFormat("gcx")
	require.Equal(t, wire.FormatGCX, f)
	require.Equal(t, wire.FormatText, wire.ParseFormat("compact"))
	require.Equal(t, wire.FormatJSON, wire.ParseFormat(""))
}
