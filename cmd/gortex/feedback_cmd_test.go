package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedFeedback records the (repo, tool, args) the feedback verb lowered to.
type capturedFeedback struct {
	repo string
	tool string
	args map[string]any
}

func runFeedback(t *testing.T, argv ...string) (*capturedFeedback, *bytes.Buffer, error) {
	t.Helper()
	resetFeedbackFlags()

	orig := feedbackDaemonTool
	t.Cleanup(func() { feedbackDaemonTool = orig })

	var cap *capturedFeedback
	feedbackDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedFeedback{repo: repo, tool: tool, args: args}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(append([]string{"feedback"}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

func resetFeedbackFlags() {
	resetCobraFlags(feedbackCmd)
	feedbackIndex = "."
	feedbackFormat = "json"

	feedbackRecordTask, feedbackRecordUseful = "", ""
	feedbackRecordNotNeeded, feedbackRecordMissing = "", ""
	feedbackRecordToolSource = ""

	feedbackQueryToolSource = ""
	feedbackQueryTopN = 0
	feedbackQueryCompact = false
}

func TestFeedbackRecord_Lowers(t *testing.T) {
	cap, _, err := runFeedback(t, "record", "--task", "t", "--useful", "a,b")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "feedback", cap.tool)
	require.Equal(t, "record", cap.args["action"])
	require.Equal(t, "t", cap.args["task"])
	require.Equal(t, "a,b", cap.args["useful"])
}

func TestFeedbackRecord_AllFlags(t *testing.T) {
	cap, _, err := runFeedback(t, "record",
		"--task", "fix", "--useful", "a", "--not-needed", "b",
		"--missing", "c", "--tool-source", "prefetch_context")
	require.NoError(t, err)
	require.Equal(t, "record", cap.args["action"])
	require.Equal(t, "fix", cap.args["task"])
	require.Equal(t, "a", cap.args["useful"])
	require.Equal(t, "b", cap.args["not_needed"])
	require.Equal(t, "c", cap.args["missing"])
	require.Equal(t, "prefetch_context", cap.args["tool_source"])
}

func TestFeedbackRecord_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runFeedback(t, "record", "--task", "t")
	require.NoError(t, err)
	require.Equal(t, "record", cap.args["action"])
	require.Equal(t, "t", cap.args["task"])
	_, hasUseful := cap.args["useful"]
	require.False(t, hasUseful, "unset --useful must not be forwarded")
	_, hasMissing := cap.args["missing"]
	require.False(t, hasMissing, "unset --missing must not be forwarded")
}

func TestFeedbackQuery_Lowers(t *testing.T) {
	cap, _, err := runFeedback(t, "query", "--tool-source", "all", "--top-n", "5")
	require.NoError(t, err)
	require.Equal(t, "feedback", cap.tool)
	require.Equal(t, "query", cap.args["action"])
	require.Equal(t, "all", cap.args["tool_source"])
	require.Equal(t, 5, cap.args["top_n"])
}

func TestFeedbackQuery_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runFeedback(t, "query")
	require.NoError(t, err)
	require.Equal(t, "query", cap.args["action"])
	_, hasTopN := cap.args["top_n"]
	require.False(t, hasTopN, "unset --top-n must not be forwarded")
}

// TestFeedback_DaemonRequired asserts the real call path returns the
// daemon-required error when no daemon tracks the repo.
func TestFeedback_DaemonRequired(t *testing.T) {
	resetFeedbackFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"feedback", "query", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
