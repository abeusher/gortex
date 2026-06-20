package main

import (
	"github.com/spf13/cobra"
)

// feedbackDaemonTool is the daemon-tool relay seam. It is indirected through a
// package var so tests can stub the daemon call (asserting the lowered
// tool + args) without a running daemon.
var feedbackDaemonTool = requireDaemonTool

// Shared persistent flags for the feedback group.
var (
	feedbackIndex  string
	feedbackFormat string
)

var feedbackCmd = &cobra.Command{
	Use:   "feedback",
	Short: "Agent-learning feedback — score & query smart_context suggestions (feedback)",
	Long: `The feedback verb group records and queries the agent-learning feedback
loop over the daemon:

  record — mark which smart_context / prefetch_context suggestions were useful,
           not needed, or missing (improves future context quality)
  query  — aggregated stats: most useful, most missed, accuracy

Every subcommand is a thin shell over the MCP feedback tool on the daemon that
owns the repo. Requires a running daemon that tracks the repo.`,
	SilenceUsage: true,
}

// runFeedbackTool calls the feedback tool and renders the result. With --format
// json (or as the fallback for any shape) it pretty-prints the JSON.
func runFeedbackTool(cmd *cobra.Command, args map[string]any) error {
	if feedbackFormat != "" {
		args["format"] = feedbackFormat
	}
	raw, err := feedbackDaemonTool(feedbackIndex, "feedback", args)
	if err != nil {
		return err
	}
	return emitDaemonJSON(cmd, raw)
}

// --- feedback record --------------------------------------------------------

var (
	feedbackRecordTask       string
	feedbackRecordUseful     string
	feedbackRecordNotNeeded  string
	feedbackRecordMissing    string
	feedbackRecordToolSource string
)

var feedbackRecordCmd = &cobra.Command{
	Use:   "record",
	Short: "Report which context suggestions were useful / not needed / missing",
	Long: `Records feedback on a prior smart_context / prefetch_context call: the
comma-separated symbol IDs that were useful, returned-but-not-needed, or that
should have been included. This trains future context quality.

  gortex feedback record --task "fix the auth bug" --useful pkg/a.go::Foo,pkg/b.go::Bar`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		args := map[string]any{"action": "record"}
		if cmd.Flags().Changed("task") {
			args["task"] = feedbackRecordTask
		}
		if cmd.Flags().Changed("useful") {
			args["useful"] = feedbackRecordUseful
		}
		if cmd.Flags().Changed("not-needed") {
			args["not_needed"] = feedbackRecordNotNeeded
		}
		if cmd.Flags().Changed("missing") {
			args["missing"] = feedbackRecordMissing
		}
		if cmd.Flags().Changed("tool-source") {
			args["tool_source"] = feedbackRecordToolSource
		}
		return runFeedbackTool(cmd, args)
	},
}

// --- feedback query ---------------------------------------------------------

var (
	feedbackQueryToolSource string
	feedbackQueryTopN       int
	feedbackQueryCompact    bool
)

var feedbackQueryCmd = &cobra.Command{
	Use:   "query",
	Short: "Aggregated feedback stats — most useful, most missed, accuracy",
	RunE: func(cmd *cobra.Command, _ []string) error {
		args := map[string]any{"action": "query"}
		if cmd.Flags().Changed("tool-source") {
			args["tool_source"] = feedbackQueryToolSource
		}
		if cmd.Flags().Changed("top-n") {
			args["top_n"] = feedbackQueryTopN
		}
		if cmd.Flags().Changed("compact") {
			args["compact"] = feedbackQueryCompact
		}
		return runFeedbackTool(cmd, args)
	},
}

func init() {
	feedbackCmd.PersistentFlags().StringVar(&feedbackIndex, "index", ".", "repository path the daemon must track")
	feedbackCmd.PersistentFlags().StringVar(&feedbackIndex, "repo", ".", "alias for --index")
	feedbackCmd.PersistentFlags().StringVar(&feedbackFormat, "format", "json", "output / wire format: json|gcx|toon|text")

	feedbackRecordCmd.Flags().StringVar(&feedbackRecordTask, "task", "", "the task description used in the original context call")
	feedbackRecordCmd.Flags().StringVar(&feedbackRecordUseful, "useful", "", "comma-separated symbol IDs that were useful")
	feedbackRecordCmd.Flags().StringVar(&feedbackRecordNotNeeded, "not-needed", "", "comma-separated symbol IDs returned but not needed (not_needed)")
	feedbackRecordCmd.Flags().StringVar(&feedbackRecordMissing, "missing", "", "comma-separated symbol IDs that should have been included")
	feedbackRecordCmd.Flags().StringVar(&feedbackRecordToolSource, "tool-source", "", "which tool produced the context: smart_context (default) or prefetch_context (tool_source)")

	feedbackQueryCmd.Flags().StringVar(&feedbackQueryToolSource, "tool-source", "", "filter by source: smart_context, prefetch_context, or all (tool_source)")
	feedbackQueryCmd.Flags().IntVar(&feedbackQueryTopN, "top-n", 0, "number of top symbols per category (default 10) (top_n)")
	feedbackQueryCmd.Flags().BoolVar(&feedbackQueryCompact, "compact", false, "one-line-per-symbol text output")

	feedbackCmd.AddCommand(feedbackRecordCmd)
	feedbackCmd.AddCommand(feedbackQueryCmd)
	rootCmd.AddCommand(feedbackCmd)
}
