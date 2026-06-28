package mcp

import (
	"context"
	"path/filepath"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func lspWriteTool(t *testing.T, srv *Server, ctx context.Context, tool string, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	var (
		res *mcplib.CallToolResult
		err error
	)
	switch tool {
	case "apply_code_action":
		res, err = srv.handleApplyCodeAction(ctx, req)
	case "fix_all_in_file":
		res, err = srv.handleFixAllInFile(ctx, req)
	default:
		t.Fatalf("unknown tool %q", tool)
	}
	require.NoError(t, err)
	return res
}

// TestLSPWrite_ConfinesAgentPaths proves apply_code_action / fix_all_in_file
// refuse an agent-supplied path that escapes every indexed repository root —
// otherwise the LSP would open and rewrite a file outside the repo — while the
// local CLI / control channel (no session cwd) stays exempt. The confinement
// guard runs before any LSP is consulted, so this test needs no language
// server (an in-repo path simply fails later for lack of a provider).
func TestLSPWrite_ConfinesAgentPaths(t *testing.T) {
	srv, _, repoRoot := newSingleRepoServer(t)
	realRoot, err := filepath.EvalSymlinks(repoRoot)
	require.NoError(t, err)
	outside := filepath.Join(t.TempDir(), "victim.go")

	agentCtx := WithSessionCWD(context.Background(), realRoot)
	const confined = "outside every indexed repository"

	for _, tool := range []string{"apply_code_action", "fix_all_in_file"} {
		// Agent + out-of-repo path → refused by the confinement guard.
		res := lspWriteTool(t, srv, agentCtx, tool, map[string]any{"path": outside, "index": 0})
		require.True(t, res.IsError, "%s: agent out-of-repo path must be refused", tool)
		txt, _ := singleTextContent(res)
		require.Contains(t, txt, confined, "%s: expected confinement error", tool)

		// Agent + in-repo path → passes confinement (fails later for lack of an LSP, not confinement).
		inRepo := filepath.Join(realRoot, "main.go")
		res = lspWriteTool(t, srv, agentCtx, tool, map[string]any{"path": inRepo, "index": 0})
		inTxt, _ := singleTextContent(res)
		require.NotContains(t, inTxt, confined, "%s: in-repo path must pass confinement", tool)

		// CLI / control channel (no session cwd) + out-of-repo path → exempt.
		res = lspWriteTool(t, srv, context.Background(), tool, map[string]any{"path": outside, "index": 0})
		cliTxt, _ := singleTextContent(res)
		require.NotContains(t, cliTxt, confined, "%s: CLI path must be exempt from confinement", tool)
	}
}
