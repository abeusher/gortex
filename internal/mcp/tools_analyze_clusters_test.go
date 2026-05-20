package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

func newClustersTestServer(t *testing.T) *Server {
	t.Helper()
	g := graph.New()

	// Five members in a tight cluster — three call edges among them.
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: "auth/" + id + ".go", Language: "go"})
	}
	g.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "b", To: "c", Kind: graph.EdgeCalls})
	g.AddEdge(&graph.Edge{From: "c", To: "d", Kind: graph.EdgeCalls})

	// Two-member cluster — should be filtered out under default min_size=3.
	for _, id := range []string{"x", "y"} {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: "utils/" + id + ".go", Language: "python"})
	}

	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	s.analysisMu.Lock()
	s.communities = &analysis.CommunityResult{
		Communities: []analysis.Community{
			{
				ID: "c-auth", Label: "auth", Hub: "b", Size: 5,
				Members: []string{"a", "b", "c", "d", "e"},
				Files:   []string{"auth/a.go", "auth/b.go", "auth/c.go", "auth/d.go", "auth/e.go"},
			},
			{
				ID: "c-utils", Label: "utils", Size: 2,
				Members: []string{"x", "y"},
				Files:   []string{"utils/x.go", "utils/y.go"},
			},
		},
	}
	s.analysisMu.Unlock()
	return s
}

func callAnalyzeClusters(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAnalyzeClusters(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

func callAnalyzeConcepts(t *testing.T, s *Server, args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Arguments = args
	res, err := s.handleAnalyzeConcepts(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, res.IsError)
	tc, ok := res.Content[0].(mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}

// clusters ----------------------------------------------------------

func TestClusters_DefaultMinSize(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{})

	clusters, _ := out["clusters"].([]any)
	require.Len(t, clusters, 1, "min_size default 3 drops the 2-member cluster")
	row := clusters[0].(map[string]any)
	assert.Equal(t, "c-auth", row["id"])
	assert.EqualValues(t, 5, row["size"].(float64))
}

func TestClusters_AlgorithmEchoed(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{})
	assert.Equal(t, "leiden", out["algorithm"])

	// The algorithm argument selects the detector and is echoed back.
	for _, algo := range []string{"louvain", "spectral"} {
		out := callAnalyzeClusters(t, s, map[string]any{"algorithm": algo})
		assert.Equal(t, algo, out["algorithm"], "algorithm %q must be echoed", algo)
	}

	// An unknown algorithm is a clean error.
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"algorithm": "bogus"}
	res, err := s.handleAnalyzeClusters(context.Background(), req)
	require.NoError(t, err)
	require.True(t, res.IsError, "unknown algorithm must return an error")
}

func TestClusters_DensityCorrect(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{})

	clusters, _ := out["clusters"].([]any)
	row := clusters[0].(map[string]any)
	// 3 intra edges (a→b, b→c, c→d). Possible-directed-pairs = 5*4=20.
	assert.InDelta(t, 0.15, row["density"].(float64), 1e-6, "3/20 = 0.15")
}

func TestClusters_FileSpread(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{})
	clusters, _ := out["clusters"].([]any)
	row := clusters[0].(map[string]any)
	// 5 files / 5 members = 1.0.
	assert.InDelta(t, 1.0, row["file_spread"].(float64), 1e-6)
}

func TestClusters_LanguageMix(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{})
	clusters, _ := out["clusters"].([]any)
	row := clusters[0].(map[string]any)
	langs := row["languages"].(map[string]any)
	assert.EqualValues(t, 5, langs["go"].(float64))
}

func TestClusters_LowerMinSizeReturnsSmaller(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{"min_size": 1})
	clusters, _ := out["clusters"].([]any)
	assert.Len(t, clusters, 2)
}

func TestClusters_PathPrefix(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeClusters(t, s, map[string]any{"path_prefix": "utils/", "min_size": 1})
	clusters, _ := out["clusters"].([]any)
	require.Len(t, clusters, 1)
	assert.Equal(t, "c-utils", clusters[0].(map[string]any)["id"])
}

func TestClusters_EmptyCommunities(t *testing.T) {
	g := graph.New()
	s := &Server{
		graph:      g,
		session:    newSessionState(),
		tokenStats: &tokenStats{},
		symHistory: &symbolHistory{entries: make(map[string][]SymbolModification)},
		sessions:   newSessionMap(),
		toolScopes: newScopeRegistry(),
	}
	out := callAnalyzeClusters(t, s, map[string]any{})
	clusters, _ := out["clusters"].([]any)
	assert.Empty(t, clusters)
	assert.Equal(t, "leiden", out["algorithm"])
}

func TestClusters_IntegrationViaDispatch(t *testing.T) {
	s := newClustersTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"kind": "clusters"}
	res, err := s.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.IsError)
}

// concepts ----------------------------------------------------------

func TestConcepts_HeuristicLabelWhenNoLLM(t *testing.T) {
	s := newClustersTestServer(t)
	// LLM is nil — concepts should produce heuristic labels.
	out := callAnalyzeConcepts(t, s, map[string]any{})
	concepts, _ := out["concepts"].([]any)
	require.NotEmpty(t, concepts)
	for _, c := range concepts {
		row := c.(map[string]any)
		assert.Equal(t, "heuristic", row["source"])
		assert.NotEmpty(t, row["theme"])
	}
}

func TestConcepts_LabelFallsBackToHub(t *testing.T) {
	s := newClustersTestServer(t)
	// Strip the cached Label so the heuristic has to derive one.
	s.analysisMu.Lock()
	s.communities.Communities[0].Label = ""
	s.analysisMu.Unlock()
	out := callAnalyzeConcepts(t, s, map[string]any{})

	concepts, _ := out["concepts"].([]any)
	require.NotEmpty(t, concepts)
	theme := concepts[0].(map[string]any)["theme"].(string)
	assert.Contains(t, theme, "auth", "auth/ common prefix should land in the label")
}

func TestConcepts_MinSizeFilter(t *testing.T) {
	s := newClustersTestServer(t)
	out := callAnalyzeConcepts(t, s, map[string]any{"min_size": 4})
	concepts, _ := out["concepts"].([]any)
	for _, c := range concepts {
		assert.GreaterOrEqual(t, int(c.(map[string]any)["member_size"].(float64)), 4)
	}
}

func TestConcepts_IntegrationViaDispatch(t *testing.T) {
	s := newClustersTestServer(t)
	req := mcp.CallToolRequest{}
	req.Params.Arguments = map[string]any{"kind": "concepts"}
	res, err := s.handleAnalyze(context.Background(), req)
	require.NoError(t, err)
	assert.False(t, res.IsError)
}

// helpers -----------------------------------------------------------

func TestHeuristicConceptLabel(t *testing.T) {
	c := analysis.Community{ID: "c1", Label: "explicit"}
	assert.Equal(t, "explicit", heuristicConceptLabel(c))

	c2 := analysis.Community{ID: "c2", Hub: "Foo", Files: []string{"auth/x.go", "auth/y.go"}}
	assert.Contains(t, heuristicConceptLabel(c2), "Foo")

	c3 := analysis.Community{ID: "c3", Files: []string{"a/b.go", "x/y.go"}}
	assert.Equal(t, "cluster-c3", heuristicConceptLabel(c3), "no common prefix + no hub falls back to cluster-id")
}

func TestCommonFilePrefix(t *testing.T) {
	assert.Equal(t, "auth", commonFilePrefix([]string{"auth/x.go", "auth/y.go"}))
	assert.Equal(t, "auth/jwt", commonFilePrefix([]string{"auth/jwt/a.go", "auth/jwt/b.go"}))
	assert.Equal(t, "", commonFilePrefix([]string{"auth/a.go", "main.go"}))
	assert.Equal(t, "", commonFilePrefix(nil))
}

func TestShortenLabel(t *testing.T) {
	assert.Equal(t, "Authentication flow", shortenLabel("Authentication flow\nlonger explanation"))
	assert.Equal(t, "label", shortenLabel(`"label."`))
}

func TestTopN(t *testing.T) {
	got := topN(map[string]int{"a": 3, "b": 5, "c": 1}, 2)
	assert.Equal(t, []string{"b", "a"}, got)
}

func TestSliceFirstN(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, sliceFirstN([]string{"a", "b", "c"}, 2))
	assert.Equal(t, []string{"a"}, sliceFirstN([]string{"a"}, 5))
	assert.Nil(t, sliceFirstN(nil, 3))
}
