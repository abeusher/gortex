package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedFlow records the (repo, tool, args) a dataflow verb lowered to.
type capturedFlow struct {
	repo string
	tool string
	args map[string]any
}

// runDataflow drives the real command tree for one dataflow verb (flow/taint),
// stubbing the shared daemon seam. Returns the captured call, the buffer, and
// any error.
func runDataflow(t *testing.T, verb string, argv ...string) (*capturedFlow, *bytes.Buffer, error) {
	t.Helper()
	resetFlowFlags()

	orig := flowDaemonTool
	t.Cleanup(func() { flowDaemonTool = orig })

	var cap *capturedFlow
	flowDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedFlow{repo: repo, tool: tool, args: args}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(append([]string{verb}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

func resetFlowFlags() {
	resetCobraFlags(flowCmd)
	resetCobraFlags(taintCmd)

	flowIndex = "."
	flowFrom, flowTo = "", ""
	flowMaxDepth, flowMaxPaths = 0, 0
	flowMinTier, flowFormat = "", "json"

	taintIndex = "."
	taintSource, taintSink = "", ""
	taintMaxDepth, taintLimit = 0, 0
	taintMinTier, taintFormat = "", "json"
}

// --- flow (flow_between) ----------------------------------------------------

func TestFlow_Lowers(t *testing.T) {
	cap, _, err := runDataflow(t, "flow", "--from", "A", "--to", "B")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "flow_between", cap.tool)
	require.Equal(t, "A", cap.args["source_id"])
	require.Equal(t, "B", cap.args["sink_id"])
	require.Equal(t, "json", cap.args["format"])
}

func TestFlow_AllFlags(t *testing.T) {
	cap, _, err := runDataflow(t, "flow",
		"--from", "A", "--to", "B",
		"--max-depth", "6", "--max-paths", "3", "--min-tier", "ast_resolved")
	require.NoError(t, err)
	require.Equal(t, "flow_between", cap.tool)
	require.Equal(t, 6, cap.args["max_depth"])
	require.Equal(t, 3, cap.args["max_paths"])
	require.Equal(t, "ast_resolved", cap.args["min_tier"])
}

func TestFlow_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runDataflow(t, "flow", "--from", "A", "--to", "B")
	require.NoError(t, err)
	_, hasDepth := cap.args["max_depth"]
	require.False(t, hasDepth, "unset --max-depth must not be forwarded")
	_, hasTier := cap.args["min_tier"]
	require.False(t, hasTier, "unset --min-tier must not be forwarded")
}

func TestFlow_MissingFromTo(t *testing.T) {
	_, _, err := runDataflow(t, "flow", "--from", "A")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--from and --to are required")
}

// --- taint (taint_paths) ----------------------------------------------------

func TestTaint_Lowers(t *testing.T) {
	cap, _, err := runDataflow(t, "taint", "--source", "s", "--sink", "k")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "taint_paths", cap.tool)
	require.Equal(t, "s", cap.args["source_pattern"])
	require.Equal(t, "k", cap.args["sink_pattern"])
}

func TestTaint_AllFlags(t *testing.T) {
	cap, _, err := runDataflow(t, "taint",
		"--source", "s", "--sink", "k",
		"--max-depth", "10", "--limit", "30", "--min-tier", "lsp_resolved")
	require.NoError(t, err)
	require.Equal(t, "taint_paths", cap.tool)
	require.Equal(t, 10, cap.args["max_depth"])
	require.Equal(t, 30, cap.args["limit"])
	require.Equal(t, "lsp_resolved", cap.args["min_tier"])
}

func TestTaint_MissingSourceSink(t *testing.T) {
	_, _, err := runDataflow(t, "taint", "--source", "s")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--source and --sink are required")
}

// TestFlow_DaemonRequired asserts the real call path returns the daemon-required
// error when no daemon tracks the repo.
func TestFlow_DaemonRequired(t *testing.T) {
	resetFlowFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"flow", "--from", "A", "--to", "B", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
