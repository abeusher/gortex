package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	gortexmcp "github.com/zzet/gortex/internal/mcp"
)

// analyzeDaemonTool is the daemon-tool relay seam. It is indirected through a
// package var so tests can stub the daemon call (asserting the lowered
// tool + args) without a running daemon.
var analyzeDaemonTool = requireDaemonTool

var (
	analyzeIndex      string
	analyzeFormat     string
	analyzeKind       string
	analyzeLimit      int
	analyzeCompact    bool
	analyzePathPrefix string
	analyzeArgs       []string
)

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Run the unified graph-analysis dispatcher (analyze) by kind",
	Long: `Runs the daemon's unified analyze tool — one dispatcher over every
structural / quality / security analyzer. Pick the analyzer with --kind; the
valid kinds are listed by 'gortex analyze kinds'.

Universal typed flags cover the common parameters: --format, --limit, --compact,
and --path-prefix. Kind-specific parameters ride on --arg key=value (repeatable),
using the same deterministic coercion as 'gortex call':

  gortex analyze --kind hotspots --arg threshold:=0.8 --limit 5
  gortex analyze --kind todos --arg tag=FIXME --arg has_assignee=true
  gortex analyze --kind coverage_gaps --path-prefix internal/auth/ --arg max_pct:=80

--arg coercion: true/false -> bool, an integer or float -> number, null -> null,
a value starting with [ or { -> parsed JSON, key:=<raw> forces a raw-JSON parse
of the right-hand side, key= -> the empty string, and anything else stays a
string. A --arg pair overrides the matching universal typed flag.

Requires a running daemon that tracks the repo.`,
	// A daemon-required / flag-validation error is self-explanatory; don't
	// bury it under the full usage dump.
	SilenceUsage: true,
	RunE:         runAnalyze,
}

// analyzeKindsCmd lists the valid analyze kinds straight from the in-process
// SSOT — no daemon needed.
var analyzeKindsCmd = &cobra.Command{
	Use:   "kinds",
	Short: "List the valid analyze kinds with a one-line description (no daemon needed)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		kinds := gortexmcp.AnalyzeKinds()

		// Width the kind column to the widest name so the descriptions
		// line up into a readable two-column reference listing.
		width := 0
		for _, k := range kinds {
			if len(k) > width {
				width = len(k)
			}
		}

		out := cmd.OutOrStdout()
		for _, k := range kinds {
			if desc := gortexmcp.AnalyzeKindDescription(k); desc != "" {
				fmt.Fprintf(out, "%-*s  %s\n", width, k, desc)
			} else {
				fmt.Fprintln(out, k)
			}
		}
		return nil
	},
}

func init() {
	analyzeCmd.PersistentFlags().StringVar(&analyzeIndex, "index", ".", "repository path the daemon must track")
	analyzeCmd.PersistentFlags().StringVar(&analyzeIndex, "repo", ".", "alias for --index")
	analyzeCmd.Flags().StringVar(&analyzeKind, "kind", "", "analysis kind (required); see 'gortex analyze kinds'")
	analyzeCmd.Flags().StringVar(&analyzeFormat, "format", "json", "output / wire format: json|gcx|toon|text")
	analyzeCmd.Flags().IntVar(&analyzeLimit, "limit", 0, "cap the number of rows returned (kind-dependent default)")
	analyzeCmd.Flags().BoolVar(&analyzeCompact, "compact", false, "one-line-per-result text output")
	analyzeCmd.Flags().StringVar(&analyzePathPrefix, "path-prefix", "", "scope to nodes under this file-path prefix (path_prefix)")
	analyzeCmd.Flags().StringArrayVar(&analyzeArgs, "arg", nil, "add one kind-specific key=value argument (repeatable); see help for coercion rules")

	analyzeCmd.AddCommand(analyzeKindsCmd)
	rootCmd.AddCommand(analyzeCmd)
}

func runAnalyze(cmd *cobra.Command, _ []string) error {
	if analyzeKind == "" {
		return fmt.Errorf("--kind is required; run `gortex analyze kinds` to list the valid kinds")
	}
	if !validAnalyzeKind(analyzeKind) {
		return unknownAnalyzeKindErr(analyzeKind)
	}

	// Start with the kind and the universal typed flags (only when the user
	// actually set them, so the daemon's kind-specific defaults hold).
	toolArgs := map[string]any{"kind": analyzeKind}
	if cmd.Flags().Changed("limit") {
		toolArgs["limit"] = analyzeLimit
	}
	if cmd.Flags().Changed("compact") {
		toolArgs["compact"] = analyzeCompact
	}
	if cmd.Flags().Changed("path-prefix") {
		toolArgs["path_prefix"] = analyzePathPrefix
	}

	// Overlay --arg key=value pairs on top — they win over the typed flags so a
	// user can always reach a parameter the universal flags don't cover.
	for _, kv := range analyzeArgs {
		key, val, err := coerceArg(kv)
		if err != nil {
			return err
		}
		toolArgs[key] = val
	}

	// Forward the chosen wire format. The executor pins format=json by default;
	// an explicit format here overrides it.
	if analyzeFormat != "" {
		toolArgs["format"] = analyzeFormat
	}

	raw, err := analyzeDaemonTool(analyzeIndex, "analyze", toolArgs)
	if err != nil {
		return err
	}

	switch analyzeFormat {
	case "gcx", "toon":
		// Compact wire formats are printed verbatim — re-indenting corrupts them.
		fmt.Fprintln(cmd.OutOrStdout(), strings.TrimRight(string(raw), "\n"))
		return nil
	default: // json | text
		return emitDaemonJSON(cmd, raw)
	}
}

// validAnalyzeKind reports whether kind is one of the canonical analyze kinds.
func validAnalyzeKind(kind string) bool {
	for _, k := range gortexmcp.AnalyzeKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// unknownAnalyzeKindErr builds the actionable error for an unknown --kind: it
// lists the valid kinds and points at `gortex analyze kinds`.
func unknownAnalyzeKindErr(kind string) error {
	return fmt.Errorf("unknown analyze kind %q — valid kinds: %s\nRun `gortex analyze kinds` to list them",
		kind, strings.Join(gortexmcp.AnalyzeKinds(), ", "))
}
