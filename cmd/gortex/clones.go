package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// clonesDaemonTool is the daemon-tool relay seam. It is indirected through a
// package var so tests can stub the daemon call (asserting the lowered
// tool + args) without a running daemon.
var clonesDaemonTool = requireDaemonTool

var (
	clonesIndex         string
	clonesMinSimilarity float64
	clonesDeadOnly      bool
	clonesPathPrefix    string
	clonesRepo          string
	clonesLimit         int
	clonesFormat        string
)

var clonesCmd = &cobra.Command{
	Use:   "clones",
	Short: "Surface near-duplicate (clone) function clusters (find_clones)",
	Long: `Queries the EdgeSimilarTo graph layer for near-duplicate function/method
clusters found by the MinHash + LSH clone-detection pass — copy-paste and
renamed-variable (Type-1/Type-2) clones. Every member is flagged dead (zero
incoming calls/refs), so --dead-only yields the "dead duplicates of live code"
diagnostic.

  gortex clones --dead-only --path-prefix internal/
  gortex clones --min-similarity 0.85 --limit 20

Requires a running daemon that tracks the repo.`,
	SilenceUsage: true,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("min-similarity") {
			toolArgs["min_similarity"] = clonesMinSimilarity
		}
		if cmd.Flags().Changed("dead-only") {
			toolArgs["dead_only"] = clonesDeadOnly
		}
		if cmd.Flags().Changed("path-prefix") {
			toolArgs["path_prefix"] = clonesPathPrefix
		}
		if cmd.Flags().Changed("repo-filter") {
			toolArgs["repo"] = clonesRepo
		}
		if cmd.Flags().Changed("limit") {
			toolArgs["limit"] = clonesLimit
		}
		if clonesFormat != "" {
			toolArgs["format"] = clonesFormat
		}
		raw, err := clonesDaemonTool(clonesIndex, "find_clones", toolArgs)
		if err != nil {
			return err
		}
		switch clonesFormat {
		case "gcx", "toon":
			fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(raw), "\n"))
			return nil
		default:
			return emitDaemonJSON(cmd, raw)
		}
	},
}

func init() {
	clonesCmd.Flags().StringVar(&clonesIndex, "index", ".", "repository path the daemon must track")
	clonesCmd.Flags().StringVar(&clonesIndex, "repo", ".", "alias for --index")
	clonesCmd.Flags().Float64Var(&clonesMinSimilarity, "min-similarity", 0, "report clone pairs at or above this estimated Jaccard similarity (0..1) (min_similarity)")
	clonesCmd.Flags().BoolVar(&clonesDeadOnly, "dead-only", false, "only clusters containing a dead-code symbol — the dead-duplicates view (dead_only)")
	clonesCmd.Flags().StringVar(&clonesPathPrefix, "path-prefix", "", "restrict to symbols whose file path starts with this prefix (path_prefix)")
	clonesCmd.Flags().StringVar(&clonesRepo, "repo-filter", "", "restrict to symbols in a specific repository (RepoPrefix exact match) (repo)")
	clonesCmd.Flags().IntVar(&clonesLimit, "limit", 0, "maximum clusters to return (default 50, largest-first)")
	clonesCmd.Flags().StringVar(&clonesFormat, "format", "json", "output / wire format: json|gcx|toon|text")
	rootCmd.AddCommand(clonesCmd)
}
