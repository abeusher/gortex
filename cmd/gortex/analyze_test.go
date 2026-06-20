package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedAnalyze records the (repo, tool, args) the analyze verb lowered to.
type capturedAnalyze struct {
	repo string
	tool string
	args map[string]any
}

// runAnalyzeCmd drives the real command tree (rootCmd → analyze) with the given
// argv, stubbing the daemon seam so the call never leaves the process. Returns
// the captured call (or nil if none was made), the combined out/err buffer, and
// any error from RunE.
func runAnalyzeCmd(t *testing.T, argv ...string) (*capturedAnalyze, *bytes.Buffer, error) {
	t.Helper()
	resetAnalyzeFlags()

	orig := analyzeDaemonTool
	t.Cleanup(func() { analyzeDaemonTool = orig })

	var cap *capturedAnalyze
	analyzeDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedAnalyze{repo: repo, tool: tool, args: args}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(append([]string{"analyze"}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

func resetAnalyzeFlags() {
	resetCobraFlags(analyzeCmd)
	analyzeIndex = "."
	analyzeFormat = "json"
	analyzeKind = ""
	analyzeLimit = 0
	analyzeCompact = false
	analyzePathPrefix = ""
	analyzeArgs = nil
}

// TestAnalyze_KindArgAndUniversalFlags asserts the kind, universal --limit, and
// a --arg coerced number all lower correctly onto the analyze tool.
func TestAnalyze_KindArgAndUniversalFlags(t *testing.T) {
	cap, _, err := runAnalyzeCmd(t,
		"--kind", "hotspots", "--arg", "threshold:=0.8", "--limit", "5")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "analyze", cap.tool)
	require.Equal(t, "hotspots", cap.args["kind"])
	require.Equal(t, 0.8, cap.args["threshold"]) // walrus raw-JSON number
	require.Equal(t, 5, cap.args["limit"])
	require.Equal(t, "json", cap.args["format"])
}

// TestAnalyze_PathPrefixAndCompact asserts the typed --path-prefix and --compact
// flags lower to path_prefix / compact.
func TestAnalyze_PathPrefixAndCompact(t *testing.T) {
	cap, _, err := runAnalyzeCmd(t,
		"--kind", "ownership", "--path-prefix", "internal/auth/", "--compact")
	require.NoError(t, err)
	require.Equal(t, "ownership", cap.args["kind"])
	require.Equal(t, "internal/auth/", cap.args["path_prefix"])
	require.Equal(t, true, cap.args["compact"])
}

// TestAnalyze_OnlyChangedTypedFlagsSent asserts unset universal flags are not
// forwarded, so the daemon's kind-specific defaults hold.
func TestAnalyze_OnlyChangedTypedFlagsSent(t *testing.T) {
	cap, _, err := runAnalyzeCmd(t, "--kind", "dead_code")
	require.NoError(t, err)
	require.Equal(t, "dead_code", cap.args["kind"])
	_, hasLimit := cap.args["limit"]
	require.False(t, hasLimit, "unset --limit must not be forwarded")
	_, hasCompact := cap.args["compact"]
	require.False(t, hasCompact, "unset --compact must not be forwarded")
	_, hasPrefix := cap.args["path_prefix"]
	require.False(t, hasPrefix, "unset --path-prefix must not be forwarded")
}

// TestAnalyze_ArgOverridesTypedFlag asserts a --arg pair wins over the matching
// universal typed flag.
func TestAnalyze_ArgOverridesTypedFlag(t *testing.T) {
	cap, _, err := runAnalyzeCmd(t,
		"--kind", "cross_repo", "--limit", "10", "--arg", "limit=99")
	require.NoError(t, err)
	require.Equal(t, int64(99), cap.args["limit"], "--arg must override --limit")
}

// TestAnalyze_BogusKindClientSideError asserts an invalid --kind is rejected
// client-side (no daemon call) with a message listing valid kinds.
func TestAnalyze_BogusKindClientSideError(t *testing.T) {
	called := false
	orig := analyzeDaemonTool
	t.Cleanup(func() { analyzeDaemonTool = orig })
	resetAnalyzeFlags()
	analyzeDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		called = true
		return nil, nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"analyze", "--kind", "bogus"})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.False(t, called, "an invalid --kind must NOT call the daemon")
	require.Contains(t, err.Error(), "unknown analyze kind")
	require.Contains(t, err.Error(), "hotspots", "the error should list valid kinds")
}

// TestAnalyze_MissingKindError asserts --kind is required.
func TestAnalyze_MissingKindError(t *testing.T) {
	_, _, err := runAnalyzeCmd(t)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--kind is required")
}

// TestAnalyzeKinds_PrintsKindsNoDaemon asserts `analyze kinds` lists the kinds
// from the in-process SSOT without touching the daemon.
func TestAnalyzeKinds_PrintsKindsNoDaemon(t *testing.T) {
	called := false
	orig := analyzeDaemonTool
	t.Cleanup(func() { analyzeDaemonTool = orig })
	resetAnalyzeFlags()
	analyzeDaemonTool = func(string, string, map[string]any) (json.RawMessage, error) {
		called = true
		return nil, nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"analyze", "kinds"})
	require.NoError(t, rootCmd.Execute())
	require.False(t, called, "`analyze kinds` must not call the daemon")

	out := buf.String()
	for _, k := range []string{"hotspots", "dead_code", "cycles", "todos", "coverage_gaps"} {
		require.Contains(t, out, k)
	}
}

// TestAnalyze_DaemonRequired asserts the real call path returns the actionable
// daemon-required error when no daemon tracks the repo.
func TestAnalyze_DaemonRequired(t *testing.T) {
	resetAnalyzeFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"analyze", "--kind", "hotspots", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
