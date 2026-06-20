package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedClones records the (repo, tool, args) the clones verb lowered to.
type capturedClones struct {
	repo string
	tool string
	args map[string]any
}

func runClones(t *testing.T, argv ...string) (*capturedClones, *bytes.Buffer, error) {
	t.Helper()
	resetClonesFlags()

	orig := clonesDaemonTool
	t.Cleanup(func() { clonesDaemonTool = orig })

	var cap *capturedClones
	clonesDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedClones{repo: repo, tool: tool, args: args}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(append([]string{"clones"}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

func resetClonesFlags() {
	resetCobraFlags(clonesCmd)
	clonesIndex = "."
	clonesMinSimilarity = 0
	clonesDeadOnly = false
	clonesPathPrefix = ""
	clonesRepo = ""
	clonesLimit = 0
	clonesFormat = "json"
}

func TestClones_DeadOnly(t *testing.T) {
	cap, _, err := runClones(t, "--dead-only")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "find_clones", cap.tool)
	require.Equal(t, true, cap.args["dead_only"])
}

func TestClones_AllFlags(t *testing.T) {
	cap, _, err := runClones(t,
		"--min-similarity", "0.85", "--dead-only",
		"--path-prefix", "internal/", "--repo-filter", "gortex", "--limit", "20")
	require.NoError(t, err)
	require.Equal(t, "find_clones", cap.tool)
	require.Equal(t, 0.85, cap.args["min_similarity"])
	require.Equal(t, true, cap.args["dead_only"])
	require.Equal(t, "internal/", cap.args["path_prefix"])
	require.Equal(t, "gortex", cap.args["repo"])
	require.Equal(t, 20, cap.args["limit"])
}

func TestClones_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runClones(t)
	require.NoError(t, err)
	require.Equal(t, "find_clones", cap.tool)
	_, hasSim := cap.args["min_similarity"]
	require.False(t, hasSim, "unset --min-similarity must not be forwarded")
	_, hasDead := cap.args["dead_only"]
	require.False(t, hasDead, "unset --dead-only must not be forwarded")
	require.Equal(t, "json", cap.args["format"])
}

// TestClones_DaemonRequired asserts the real call path returns the
// daemon-required error when no daemon tracks the repo.
func TestClones_DaemonRequired(t *testing.T) {
	resetClonesFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"clones", "--dead-only", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
