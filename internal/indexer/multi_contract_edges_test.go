package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
)

// newMultiLangRegistry registers Go + TypeScript for tests that exercise
// cross-language contracts (e.g. TS consumer → Go provider).
func newMultiLangRegistry() *parser.Registry {
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	return reg
}

// setupHTTPProviderRepo writes a Go file declaring a Gin route that binds
// GET /api/users to a handler function. After indexing, HTTPExtractor
// produces a provider contract with SymbolID pointing at the enclosing
// function (setupRoutes).
func setupHTTPProviderRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/"+name+"\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import "github.com/gin-gonic/gin"

func setupRoutes(r *gin.Engine) {
	r.GET("/api/users", listUsers)
}

func listUsers() {}
`), 0o644))
	return dir
}

// setupHTTPConsumerRepo writes a Go file with an http.Get call to the same
// path. HTTPExtractor produces a consumer contract with SymbolID pointing
// at fetchUsers. After ReconcileContractEdges, fetchUsers --matches-->
// setupRoutes should exist in the graph.
func setupHTTPConsumerRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/"+name+"\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "client.go"), []byte(`package main

import "net/http"

func fetchUsers() {
	http.Get("http://api.example.com/api/users")
}
`), 0o644))
	return dir
}

// TestReconcileContractEdges_BridgesConsumerToProvider is the north-star
// test for cross-service request tracing. After indexing a provider and a
// consumer in two separate tracked repos, get_call_chain from the consumer
// function must traverse into the provider's handler region. Without the
// matcher's output persisted as EdgeMatches, the BFS stops at the
// consumer-side HTTP call — nothing bridges the service boundary.
func TestReconcileContractEdges_BridgesConsumerToProvider(t *testing.T) {
	providerRoot := setupHTTPProviderRepo(t, "provider-svc")
	consumerRoot := setupHTTPConsumerRepo(t, "consumer-svc")

	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{
			{Path: providerRoot, Name: "provider-svc"},
			{Path: consumerRoot, Name: "consumer-svc"},
		},
	}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())

	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	g := graph.New()
	mi := NewMultiIndexer(g, newTestRegistry(), search.NewBM25(), cm, zap.NewNop())

	for _, entry := range cm.Global().Repos {
		_, err := mi.TrackRepoCtx(context.Background(), entry)
		require.NoError(t, err, "track %s", entry.Name)
	}

	// EdgeMatches must land on the handler function (listUsers), not on
	// the registration helper (setupRoutes). The HTTP provider extractor
	// captures the handler identifier from `r.GET("/path", handler)`
	// patterns — T1.3 — so "trace a request" lands on business logic.
	consumerSym := "consumer-svc/client.go::fetchUsers"
	providerSym := "provider-svc/main.go::listUsers"

	var matchEdge *graph.Edge
	for _, e := range g.AllEdges() {
		if e.Kind != graph.EdgeMatches {
			continue
		}
		if e.From == consumerSym && e.To == providerSym {
			matchEdge = e
			break
		}
	}
	require.NotNil(t, matchEdge,
		"expected EdgeMatches %s → %s; present match edges were: %v",
		consumerSym, providerSym, collectMatchEdges(g))
	assert.True(t, matchEdge.CrossRepo,
		"consumer and provider live in different repos — CrossRepo flag must be set")

	// Positive end-to-end: walking forward from the consumer symbol reaches
	// the provider symbol. This is what "trace a request through product"
	// relies on.
	eng := query.NewEngine(g)
	chain := eng.GetCallChain(consumerSym, query.QueryOptions{Depth: 4, Limit: 50, Detail: "brief"})
	require.NotNil(t, chain)
	reached := false
	for _, n := range chain.Nodes {
		if n.ID == providerSym {
			reached = true
			break
		}
	}
	assert.True(t, reached,
		"get_call_chain(%s) did not reach %s; chain nodes: %v",
		consumerSym, providerSym, nodeIDs(chain.Nodes))
}

// setupTSConsumerRepo writes a TypeScript file that builds its request URL
// from a template literal (${API_URL}/path) — the dominant pattern in the
// web/extension/mobile consumers in the tuck audit. T1.1 normalization
// must strip the base-URL placeholder so the consumer contract ID matches
// the provider's /v1/users.
func setupTSConsumerRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"),
		[]byte(`{"name":"`+name+`","version":"0.0.0"}`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "client.ts"), []byte(
		"const API_URL = \"https://api.example.com\";\n"+
			"export async function fetchUsers() {\n"+
			"  return fetch(`${API_URL}/api/users`);\n"+
			"}\n",
	), 0o644))
	return dir
}

// TestReconcileContractEdges_TemplateLiteralConsumer is T1.1's cross-repo
// integration guard. A TypeScript consumer constructs the request URL
// from a template literal whose base is an interpolated constant; the Go
// provider declares "/api/users" verbatim. Without NormalizeHTTPPath's
// template-literal stripping, the consumer's contract ID carries the
// placeholder and never matches the provider, so no EdgeMatches forms
// and get_call_chain stops at the fetch call site.
func TestReconcileContractEdges_TemplateLiteralConsumer(t *testing.T) {
	providerRoot := setupHTTPProviderRepo(t, "provider-svc")
	consumerRoot := setupTSConsumerRepo(t, "web-ui")

	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{
			{Path: providerRoot, Name: "provider-svc"},
			{Path: consumerRoot, Name: "web-ui"},
		},
	}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())

	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	g := graph.New()
	mi := NewMultiIndexer(g, newMultiLangRegistry(), search.NewBM25(), cm, zap.NewNop())
	for _, entry := range cm.Global().Repos {
		_, err := mi.TrackRepoCtx(context.Background(), entry)
		require.NoError(t, err, "track %s", entry.Name)
	}

	consumerSym := "web-ui/src/client.ts::fetchUsers"
	providerSym := "provider-svc/main.go::listUsers"

	var matchEdge *graph.Edge
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeMatches && e.From == consumerSym && e.To == providerSym {
			matchEdge = e
			break
		}
	}
	require.NotNil(t, matchEdge,
		"expected EdgeMatches %s → %s after template-literal normalization; present match edges were: %v",
		consumerSym, providerSym, collectMatchEdges(g))
	assert.True(t, matchEdge.CrossRepo, "consumer and provider live in different repos")

	eng := query.NewEngine(g)
	chain := eng.GetCallChain(consumerSym, query.QueryOptions{Depth: 4, Limit: 50, Detail: "brief"})
	reached := false
	for _, n := range chain.Nodes {
		if n.ID == providerSym {
			reached = true
			break
		}
	}
	assert.True(t, reached,
		"get_call_chain(%s) must reach %s across service boundary; chain was: %v",
		consumerSym, providerSym, nodeIDs(chain.Nodes))
}

// TestReconcileContractEdges_PurgesStaleOnUntrack asserts that removing
// the consumer repo deletes its match edges — otherwise the graph would
// accumulate dangling edges pointing at symbols that no longer exist.
func TestReconcileContractEdges_PurgesStaleOnUntrack(t *testing.T) {
	providerRoot := setupHTTPProviderRepo(t, "provider-svc")
	consumerRoot := setupHTTPConsumerRepo(t, "consumer-svc")

	tmpCfg := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{
		Repos: []config.RepoEntry{
			{Path: providerRoot, Name: "provider-svc"},
			{Path: consumerRoot, Name: "consumer-svc"},
		},
	}
	gc.SetConfigPath(tmpCfg)
	require.NoError(t, gc.Save())

	cm, err := config.NewConfigManager(tmpCfg)
	require.NoError(t, err)

	g := graph.New()
	mi := NewMultiIndexer(g, newTestRegistry(), search.NewBM25(), cm, zap.NewNop())
	for _, entry := range cm.Global().Repos {
		_, err := mi.TrackRepoCtx(context.Background(), entry)
		require.NoError(t, err)
	}

	require.NotEmpty(t, collectMatchEdges(g), "setup precondition: at least one EdgeMatches must exist")

	mi.UntrackRepo("consumer-svc")

	remaining := collectMatchEdges(g)
	assert.Empty(t, remaining,
		"untracking the consumer must purge its match edges; found %d leftover: %v",
		len(remaining), remaining)
}

func collectMatchEdges(g *graph.Graph) []string {
	var out []string
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeMatches {
			out = append(out, e.From+" → "+e.To)
		}
	}
	return out
}

func nodeIDs(nodes []*graph.Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}
