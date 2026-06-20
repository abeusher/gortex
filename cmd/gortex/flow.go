package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// flowDaemonTool is the daemon-tool relay seam. It is indirected through a
// package var so tests can stub the daemon call (asserting the lowered
// tool + args) without a running daemon. Shared by `flow` and `taint`.
var flowDaemonTool = requireDaemonTool

// --- flow (flow_between) ----------------------------------------------------

var (
	flowIndex    string
	flowFrom     string
	flowTo       string
	flowMaxDepth int
	flowMaxPaths int
	flowMinTier  string
	flowFormat   string
)

var flowCmd = &cobra.Command{
	Use:   "flow",
	Short: "Trace ranked dataflow paths between two symbols (flow_between)",
	Long: `Walks the CPG-lite dataflow graph (value-flow / arg-of / returns-to edges)
forward from --from to --to and returns the ranked paths — the primitive that
answers "where does this value flow?". Pair with 'gortex taint' for the
pattern-driven sweep.

  gortex flow --from pkg/a.go::Input --to pkg/b.go::Sink --max-depth 6

Requires a running daemon that tracks the repo.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if flowFrom == "" || flowTo == "" {
			return fmt.Errorf("--from and --to are required (source and sink symbol IDs)")
		}
		toolArgs := map[string]any{
			"source_id": flowFrom,
			"sink_id":   flowTo,
		}
		if cmd.Flags().Changed("max-depth") {
			toolArgs["max_depth"] = flowMaxDepth
		}
		if cmd.Flags().Changed("max-paths") {
			toolArgs["max_paths"] = flowMaxPaths
		}
		if cmd.Flags().Changed("min-tier") {
			toolArgs["min_tier"] = flowMinTier
		}
		return runFlowTool(cmd, "flow_between", toolArgs, flowFormat)
	},
}

// --- taint (taint_paths) ----------------------------------------------------

var (
	taintIndex    string
	taintSource   string
	taintSink     string
	taintMaxDepth int
	taintLimit    int
	taintMinTier  string
	taintFormat   string
)

var taintCmd = &cobra.Command{
	Use:   "taint",
	Short: "Pattern-driven dataflow sweep from matching sources to sinks (taint_paths)",
	Long: `Resolves every symbol matching --source and --sink, then walks the dataflow
graph to find paths between each pair — the security-audit primitive ("every
flow from os.Getenv to db.Query"). Pattern syntax: a bare token is a
case-insensitive substring on the symbol name; exact:Foo matches exactly;
path:dir/ filters by file-path prefix; kind:method restricts the node kind.
Combine clauses with spaces.

  gortex taint --source 'path:handlers/' --sink 'exact:Exec' --limit 30

Requires a running daemon that tracks the repo.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if taintSource == "" || taintSink == "" {
			return fmt.Errorf("--source and --sink are required (source and sink patterns)")
		}
		toolArgs := map[string]any{
			"source_pattern": taintSource,
			"sink_pattern":   taintSink,
		}
		if cmd.Flags().Changed("max-depth") {
			toolArgs["max_depth"] = taintMaxDepth
		}
		if cmd.Flags().Changed("limit") {
			toolArgs["limit"] = taintLimit
		}
		if cmd.Flags().Changed("min-tier") {
			toolArgs["min_tier"] = taintMinTier
		}
		return runFlowTool(cmd, "taint_paths", toolArgs, taintFormat)
	},
}

// runFlowTool calls a dataflow tool and renders the result. gcx/toon are printed
// verbatim (re-indenting would corrupt them); every other format pretty-prints
// the JSON via emitDaemonJSON. The repo path comes from the per-command --index.
func runFlowTool(cmd *cobra.Command, tool string, args map[string]any, format string) error {
	repo := flowIndex
	if tool == "taint_paths" {
		repo = taintIndex
	}
	if format != "" {
		args["format"] = format
	}
	raw, err := flowDaemonTool(repo, tool, args)
	if err != nil {
		return err
	}
	switch format {
	case "gcx", "toon":
		fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(raw), "\n"))
		return nil
	default:
		return emitDaemonJSON(cmd, raw)
	}
}

func init() {
	flowCmd.Flags().StringVar(&flowIndex, "index", ".", "repository path the daemon must track")
	flowCmd.Flags().StringVar(&flowIndex, "repo", ".", "alias for --index")
	flowCmd.Flags().StringVar(&flowFrom, "from", "", "source symbol node ID (source_id)")
	flowCmd.Flags().StringVar(&flowTo, "to", "", "sink symbol node ID (sink_id)")
	flowCmd.Flags().IntVar(&flowMaxDepth, "max-depth", 0, "maximum BFS hops (max_depth; default 8)")
	flowCmd.Flags().IntVar(&flowMaxPaths, "max-paths", 0, "maximum paths to return (max_paths; default 10)")
	flowCmd.Flags().StringVar(&flowMinTier, "min-tier", "", "minimum per-edge Origin tier to traverse (min_tier)")
	flowCmd.Flags().StringVar(&flowFormat, "format", "json", "output / wire format: json|gcx|toon|text")
	rootCmd.AddCommand(flowCmd)

	taintCmd.Flags().StringVar(&taintIndex, "index", ".", "repository path the daemon must track")
	taintCmd.Flags().StringVar(&taintIndex, "repo", ".", "alias for --index")
	taintCmd.Flags().StringVar(&taintSource, "source", "", "source pattern (source_pattern)")
	taintCmd.Flags().StringVar(&taintSink, "sink", "", "sink pattern (sink_pattern)")
	taintCmd.Flags().IntVar(&taintMaxDepth, "max-depth", 0, "maximum BFS hops per pair (max_depth; default 8)")
	taintCmd.Flags().IntVar(&taintLimit, "limit", 0, "maximum findings to return (default 20)")
	taintCmd.Flags().StringVar(&taintMinTier, "min-tier", "", "minimum per-edge Origin tier to traverse (min_tier)")
	taintCmd.Flags().StringVar(&taintFormat, "format", "json", "output / wire format: json|gcx|toon|text")
	rootCmd.AddCommand(taintCmd)
}
