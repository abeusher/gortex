package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func flavorResultContainsConfig(ids []string) bool {
	for _, id := range ids {
		if strings.Contains(id, "Config") {
			return true
		}
	}
	return false
}

// TestSearchSymbols_FlavorFilter drives the flavor: clause, the codegraph
// kind:→flavor shim, kind+flavor composition, and the fuzzy fallback
// through the real search_symbols handler. The fixture is a Go file whose
// only type is `Config` (a struct → type_flavor=struct); main/helper are
// functions and carry no type_flavor.
func TestSearchSymbols_FlavorFilter(t *testing.T) {
	srv, _ := setupTestServer(t)

	// flavor:struct narrows to the Config struct.
	resp := searchSymbolsResp(t, srv, "flavor:struct Config")
	ids := resultIDs(resp)
	require.NotEmpty(t, ids, "flavor:struct Config should surface the struct")
	require.True(t, flavorResultContainsConfig(ids))
	require.Nil(t, resp["filters_relaxed"], "an exact flavor match must not relax")

	// codegraph shim: kind:struct is a flavor, not a real node kind — it
	// routes to the flavor filter instead of returning empty.
	resp = searchSymbolsResp(t, srv, "kind:struct Config")
	require.True(t, flavorResultContainsConfig(resultIDs(resp)),
		"kind:struct should route to the flavor filter and find Config")
	require.Nil(t, resp["filters_relaxed"])

	// flavor + kind compose (node-kind AND flavor): Config is KindType + struct.
	resp = searchSymbolsResp(t, srv, "kind:type flavor:struct Config")
	require.True(t, flavorResultContainsConfig(resultIDs(resp)),
		"kind:type flavor:struct should keep the Config struct")

	// An unsatisfiable flavor (no enum in the fixture) relaxes via the
	// fuzzy fallback, identical to an over-narrow kind: clause.
	resp = searchSymbolsResp(t, srv, "flavor:enum Config")
	require.Equal(t, true, resp["filters_relaxed"], "unsatisfiable flavor must relax")
	require.NotEmpty(t, resultIDs(resp), "fallback should still surface Config")
}

// TestSearchSymbols_FlavorParam proves the top-level flavor param filters
// the same way the inline clause does.
func TestSearchSymbols_FlavorParam(t *testing.T) {
	srv, _ := setupTestServer(t)
	res := callTool(t, srv, "search_symbols", map[string]any{"query": "Config", "flavor": "struct"})
	require.False(t, res.IsError)
	text := res.Content[0].(mcplib.TextContent).Text
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &resp))
	require.True(t, flavorResultContainsConfig(resultIDs(resp)),
		"the flavor param should filter to the Config struct")
}
