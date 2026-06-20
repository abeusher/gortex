package main

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

// memoryDaemonTool is the daemon-tool relay seam. It is indirected through a
// package var so tests can stub the daemon call (asserting the lowered
// tool + args) without a running daemon.
var memoryDaemonTool = requireDaemonTool

// Shared persistent flags for the memory group.
var (
	memoryIndex  string
	memoryFormat string
)

var memoryCmd = &cobra.Command{
	Use:   "memory",
	Short: "Session & durable memory — the Layer-2 note / memory verbs over the daemon",
	Long: `The memory verb group exposes the daemon's session-memory and
cross-session development-memory surface as CLI verbs:

  note      — save a per-session note (save_note)
  notes     — query session notes (query_notes)
  distill   — fold a session's notes into a digest (distill_session)
  store     — store a durable cross-session memory (store_memory)
  recall    — query the durable memory store (query_memories)
  surface   — proactively surface memories for a task / working set (surface_memories)

Session notes are scoped to a session and survive context compactions; durable
memories are workspace-wide and survive daemon restarts and team rotation.

Every subcommand is a thin shell over one MCP tool on the daemon that owns the
repo. Requires a running daemon that tracks the repo.`,
	// A daemon-required / flag-validation error from a subcommand is
	// self-explanatory; don't bury it under the full usage dump.
	SilenceUsage: true,
}

func init() {
	memoryCmd.PersistentFlags().StringVar(&memoryIndex, "index", ".", "repository path the daemon must track")
	memoryCmd.PersistentFlags().StringVar(&memoryIndex, "repo", ".", "alias for --index")
	memoryCmd.PersistentFlags().StringVar(&memoryFormat, "format", "text", "output format: text|json")

	memoryCmd.AddCommand(memoryNoteCmd)
	memoryCmd.AddCommand(memoryNotesCmd)
	memoryCmd.AddCommand(memoryDistillCmd)
	memoryCmd.AddCommand(memoryStoreCmd)
	memoryCmd.AddCommand(memoryRecallCmd)
	memoryCmd.AddCommand(memorySurfaceCmd)

	rootCmd.AddCommand(memoryCmd)
}

// runMemoryTool calls the daemon tool and renders the result. With --format
// json (or as the fallback for any shape) it pretty-prints the JSON; otherwise
// it runs the supplied text renderer. A nil renderer falls through to JSON.
func runMemoryTool(cmd *cobra.Command, tool string, args map[string]any, text func(*cobra.Command, json.RawMessage) error) error {
	raw, err := memoryDaemonTool(memoryIndex, tool, args)
	if err != nil {
		return err
	}
	if memoryFormat == "json" || text == nil {
		return emitDaemonJSON(cmd, raw)
	}
	return text(cmd, raw)
}

// --- memory note (save_note) -----------------------------------------------

var (
	memoryNoteBody       string
	memoryNoteSymbol     string
	memoryNoteFile       string
	memoryNoteTags       string
	memoryNoteLinks      string
	memoryNotePin        bool
	memoryNoteID         string
	memoryNoteNoAutolink bool
)

var memoryNoteCmd = &cobra.Command{
	Use:   "note",
	Short: "Save a per-session note (save_note)",
	Long: `Persists an agent-authored note for the current session, auto-linked to
symbols mentioned in --body. Pass --id to update an existing note.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("body") {
			toolArgs["body"] = memoryNoteBody
		}
		if cmd.Flags().Changed("symbol") {
			toolArgs["symbol_id"] = memoryNoteSymbol
		}
		if cmd.Flags().Changed("file") {
			toolArgs["file_path"] = memoryNoteFile
		}
		if cmd.Flags().Changed("tags") {
			toolArgs["tags"] = memoryNoteTags
		}
		if cmd.Flags().Changed("links") {
			toolArgs["links"] = memoryNoteLinks
		}
		if cmd.Flags().Changed("pin") {
			toolArgs["pinned"] = memoryNotePin
		}
		if cmd.Flags().Changed("id") {
			toolArgs["id"] = memoryNoteID
		}
		if cmd.Flags().Changed("no-autolink") {
			toolArgs["no_autolink"] = memoryNoteNoAutolink
		}
		return runMemoryTool(cmd, "save_note", toolArgs, nil)
	},
}

// --- memory notes (query_notes) --------------------------------------------

var (
	memoryNotesSymbol  string
	memoryNotesFile    string
	memoryNotesTag     string
	memoryNotesText    string
	memoryNotesSession string
	memoryNotesSince   string
	memoryNotesLimit   int
	memoryNotesPinned  bool
)

var memoryNotesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Query session notes (query_notes)",
	Long: `Search saved session notes by symbol, file, tag, free-text, session, or
recency. Pass --session all to query every session for the workspace.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("symbol") {
			toolArgs["symbol_id"] = memoryNotesSymbol
		}
		if cmd.Flags().Changed("file") {
			toolArgs["file_path"] = memoryNotesFile
		}
		if cmd.Flags().Changed("tag") {
			toolArgs["tag"] = memoryNotesTag
		}
		if cmd.Flags().Changed("text") {
			toolArgs["text"] = memoryNotesText
		}
		if cmd.Flags().Changed("session") {
			toolArgs["session_id"] = memoryNotesSession
		}
		if cmd.Flags().Changed("since") {
			toolArgs["since"] = memoryNotesSince
		}
		if cmd.Flags().Changed("limit") {
			toolArgs["limit"] = memoryNotesLimit
		}
		if cmd.Flags().Changed("pinned") {
			toolArgs["pinned_only"] = memoryNotesPinned
		}
		return runMemoryTool(cmd, "query_notes", toolArgs, nil)
	},
}

// --- memory distill (distill_session) --------------------------------------

var (
	memoryDistillSession    string
	memoryDistillMaxSymbols int
	memoryDistillMaxFiles   int
	memoryDistillMaxTags    int
	memoryDistillMaxRecent  int
)

var memoryDistillCmd = &cobra.Command{
	Use:   "distill",
	Short: "Fold a session's notes into a digest (distill_session)",
	Long: `Aggregates a session's saved notes into a digest: top symbols / files /
tags, pinned notes, recent excerpts, and a short summary. Pass --session all to
distill every session for the workspace.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("session") {
			toolArgs["session_id"] = memoryDistillSession
		}
		if cmd.Flags().Changed("max-symbols") {
			toolArgs["max_symbols"] = memoryDistillMaxSymbols
		}
		if cmd.Flags().Changed("max-files") {
			toolArgs["max_files"] = memoryDistillMaxFiles
		}
		if cmd.Flags().Changed("max-tags") {
			toolArgs["max_tags"] = memoryDistillMaxTags
		}
		if cmd.Flags().Changed("max-recent") {
			toolArgs["max_recent"] = memoryDistillMaxRecent
		}
		return runMemoryTool(cmd, "distill_session", toolArgs, nil)
	},
}

// --- memory store (store_memory) -------------------------------------------

var (
	memoryStoreBody       string
	memoryStoreTitle      string
	memoryStoreSymbols    string
	memoryStoreFiles      string
	memoryStoreTags       string
	memoryStoreKind       string
	memoryStoreSource     string
	memoryStoreImportance int
	memoryStoreConfidence float64
	memoryStorePin        bool
	memoryStoreSupersedes string
	memoryStoreScope      string
	memoryStoreID         string
	memoryStoreNoAutolink bool
)

var memoryStoreCmd = &cobra.Command{
	Use:   "store",
	Short: "Store a durable cross-session memory (store_memory)",
	Long: `Persists a cross-session development memory: an invariant, gotcha,
decision, convention, constraint, incident, or reference fact future agents in
this workspace should know. Anchor it with --symbols / --files for high-quality
surfacing. Pass --id to update; --supersedes to replace older memories.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("body") {
			toolArgs["body"] = memoryStoreBody
		}
		if cmd.Flags().Changed("title") {
			toolArgs["title"] = memoryStoreTitle
		}
		if cmd.Flags().Changed("symbols") {
			toolArgs["symbol_ids"] = memoryStoreSymbols
		}
		if cmd.Flags().Changed("files") {
			toolArgs["file_paths"] = memoryStoreFiles
		}
		if cmd.Flags().Changed("tags") {
			toolArgs["tags"] = memoryStoreTags
		}
		if cmd.Flags().Changed("kind") {
			toolArgs["kind"] = memoryStoreKind
		}
		if cmd.Flags().Changed("source") {
			toolArgs["source"] = memoryStoreSource
		}
		if cmd.Flags().Changed("importance") {
			toolArgs["importance"] = memoryStoreImportance
		}
		if cmd.Flags().Changed("confidence") {
			toolArgs["confidence"] = memoryStoreConfidence
		}
		if cmd.Flags().Changed("pin") {
			toolArgs["pinned"] = memoryStorePin
		}
		if cmd.Flags().Changed("supersedes") {
			toolArgs["supersedes"] = memoryStoreSupersedes
		}
		if cmd.Flags().Changed("scope") {
			toolArgs["scope"] = memoryStoreScope
		}
		if cmd.Flags().Changed("id") {
			toolArgs["id"] = memoryStoreID
		}
		if cmd.Flags().Changed("no-autolink") {
			toolArgs["no_autolink"] = memoryStoreNoAutolink
		}
		return runMemoryTool(cmd, "store_memory", toolArgs, nil)
	},
}

// --- memory recall (query_memories) ----------------------------------------

var (
	memoryRecallSymbol        string
	memoryRecallFile          string
	memoryRecallTag           string
	memoryRecallKind          string
	memoryRecallSource        string
	memoryRecallAuthor        string
	memoryRecallText          string
	memoryRecallSince         string
	memoryRecallMinImportance int
	memoryRecallPinned        bool
	memoryRecallSuperseded    bool
	memoryRecallLimit         int
	memoryRecallScope         string
)

var memoryRecallCmd = &cobra.Command{
	Use:   "recall",
	Short: "Query the durable memory store (query_memories)",
	Long: `Search the cross-session memory store by symbol, file, tag, kind, source,
author, free-text, recency, or importance. Workspace-wide; no session boundary.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("symbol") {
			toolArgs["symbol_id"] = memoryRecallSymbol
		}
		if cmd.Flags().Changed("file") {
			toolArgs["file_path"] = memoryRecallFile
		}
		if cmd.Flags().Changed("tag") {
			toolArgs["tag"] = memoryRecallTag
		}
		if cmd.Flags().Changed("kind") {
			toolArgs["kind"] = memoryRecallKind
		}
		if cmd.Flags().Changed("source") {
			toolArgs["source"] = memoryRecallSource
		}
		if cmd.Flags().Changed("author") {
			toolArgs["author"] = memoryRecallAuthor
		}
		if cmd.Flags().Changed("text") {
			toolArgs["text"] = memoryRecallText
		}
		if cmd.Flags().Changed("since") {
			toolArgs["since"] = memoryRecallSince
		}
		if cmd.Flags().Changed("min-importance") {
			toolArgs["min_importance"] = memoryRecallMinImportance
		}
		if cmd.Flags().Changed("pinned") {
			toolArgs["pinned_only"] = memoryRecallPinned
		}
		if cmd.Flags().Changed("include-superseded") {
			toolArgs["include_superseded"] = memoryRecallSuperseded
		}
		if cmd.Flags().Changed("limit") {
			toolArgs["limit"] = memoryRecallLimit
		}
		if cmd.Flags().Changed("scope") {
			toolArgs["scope"] = memoryRecallScope
		}
		return runMemoryTool(cmd, "query_memories", toolArgs, nil)
	},
}

// --- memory surface (surface_memories) -------------------------------------

var (
	memorySurfaceTask       string
	memorySurfaceSymbols    string
	memorySurfaceFiles      string
	memorySurfaceLimit      int
	memorySurfaceMinScore   float64
	memorySurfaceSuperseded bool
	memorySurfaceScope      string
)

var memorySurfaceCmd = &cobra.Command{
	Use:   "surface",
	Short: "Proactively surface memories for a task / working set (surface_memories)",
	Long: `Given a task description and/or anchor symbols / files, returns memories
ranked by symbol overlap, file overlap, keyword hits, importance, pinning,
recency, and confidence. The memory analogue of smart_context.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		toolArgs := map[string]any{}
		if cmd.Flags().Changed("task") {
			toolArgs["task"] = memorySurfaceTask
		}
		if cmd.Flags().Changed("symbols") {
			toolArgs["symbol_ids"] = memorySurfaceSymbols
		}
		if cmd.Flags().Changed("files") {
			toolArgs["file_paths"] = memorySurfaceFiles
		}
		if cmd.Flags().Changed("limit") {
			toolArgs["limit"] = memorySurfaceLimit
		}
		if cmd.Flags().Changed("min-score") {
			toolArgs["min_score"] = memorySurfaceMinScore
		}
		if cmd.Flags().Changed("include-superseded") {
			toolArgs["include_superseded"] = memorySurfaceSuperseded
		}
		if cmd.Flags().Changed("scope") {
			toolArgs["scope"] = memorySurfaceScope
		}
		return runMemoryTool(cmd, "surface_memories", toolArgs, nil)
	},
}

func init() {
	// note (save_note)
	memoryNoteCmd.Flags().StringVar(&memoryNoteBody, "body", "", "note text (auto-linked to mentioned symbols)")
	memoryNoteCmd.Flags().StringVar(&memoryNoteSymbol, "symbol", "", "primary symbol the note attaches to (symbol_id)")
	memoryNoteCmd.Flags().StringVar(&memoryNoteFile, "file", "", "primary file the note attaches to (file_path)")
	memoryNoteCmd.Flags().StringVar(&memoryNoteTags, "tags", "", "comma-separated labels (e.g. decision,bug,follow-up)")
	memoryNoteCmd.Flags().StringVar(&memoryNoteLinks, "links", "", "comma-separated symbol IDs to attach explicitly")
	memoryNoteCmd.Flags().BoolVar(&memoryNotePin, "pin", false, "pin the note (pinned)")
	memoryNoteCmd.Flags().StringVar(&memoryNoteID, "id", "", "existing note ID to update")
	memoryNoteCmd.Flags().BoolVar(&memoryNoteNoAutolink, "no-autolink", false, "skip the body→symbol auto-linker (no_autolink)")

	// notes (query_notes)
	memoryNotesCmd.Flags().StringVar(&memoryNotesSymbol, "symbol", "", "return notes attached/auto-linked to this symbol (symbol_id)")
	memoryNotesCmd.Flags().StringVar(&memoryNotesFile, "file", "", "return notes attached to this file path (file_path)")
	memoryNotesCmd.Flags().StringVar(&memoryNotesTag, "tag", "", "return notes carrying this tag")
	memoryNotesCmd.Flags().StringVar(&memoryNotesText, "text", "", "case-insensitive substring filter on the body")
	memoryNotesCmd.Flags().StringVar(&memoryNotesSession, "session", "", "limit to a session (e.g. 'all' for every session) (session_id)")
	memoryNotesCmd.Flags().StringVar(&memoryNotesSince, "since", "", "only notes updated at/after this RFC-3339 timestamp")
	memoryNotesCmd.Flags().IntVar(&memoryNotesLimit, "limit", 50, "cap the result set")
	memoryNotesCmd.Flags().BoolVar(&memoryNotesPinned, "pinned", false, "return only pinned notes (pinned_only)")

	// distill (distill_session)
	memoryDistillCmd.Flags().StringVar(&memoryDistillSession, "session", "", "session to distill (e.g. 'all') (session_id)")
	memoryDistillCmd.Flags().IntVar(&memoryDistillMaxSymbols, "max-symbols", 10, "cap on the top-symbols list (max_symbols)")
	memoryDistillCmd.Flags().IntVar(&memoryDistillMaxFiles, "max-files", 10, "cap on the top-files list (max_files)")
	memoryDistillCmd.Flags().IntVar(&memoryDistillMaxTags, "max-tags", 10, "cap on the top-tags list (max_tags)")
	memoryDistillCmd.Flags().IntVar(&memoryDistillMaxRecent, "max-recent", 8, "number of recent note excerpts (max_recent)")

	// store (store_memory)
	memoryStoreCmd.Flags().StringVar(&memoryStoreBody, "body", "", "memory text (auto-linked to mentioned symbols)")
	memoryStoreCmd.Flags().StringVar(&memoryStoreTitle, "title", "", "short caption used as the surfacing headline")
	memoryStoreCmd.Flags().StringVar(&memoryStoreSymbols, "symbols", "", "comma-separated primary symbol anchors (symbol_ids)")
	memoryStoreCmd.Flags().StringVar(&memoryStoreFiles, "files", "", "comma-separated primary file anchors (file_paths)")
	memoryStoreCmd.Flags().StringVar(&memoryStoreTags, "tags", "", "comma-separated labels (e.g. invariant,gotcha)")
	memoryStoreCmd.Flags().StringVar(&memoryStoreKind, "kind", "", "kind: invariant|constraint|convention|gotcha|decision|incident|reference")
	memoryStoreCmd.Flags().StringVar(&memoryStoreSource, "source", "", "source: manual|distilled|incident|review")
	memoryStoreCmd.Flags().IntVar(&memoryStoreImportance, "importance", 3, "1..5 operator-assigned weight")
	memoryStoreCmd.Flags().Float64Var(&memoryStoreConfidence, "confidence", 1.0, "0.0..1.0 — how sure this still holds")
	memoryStoreCmd.Flags().BoolVar(&memoryStorePin, "pin", false, "pin the memory (pinned)")
	memoryStoreCmd.Flags().StringVar(&memoryStoreSupersedes, "supersedes", "", "comma-separated memory IDs this replaces")
	memoryStoreCmd.Flags().StringVar(&memoryStoreScope, "scope", "", "scope: workspace (default) or global")
	memoryStoreCmd.Flags().StringVar(&memoryStoreID, "id", "", "existing memory ID to update")
	memoryStoreCmd.Flags().BoolVar(&memoryStoreNoAutolink, "no-autolink", false, "skip the body→symbol auto-linker (no_autolink)")

	// recall (query_memories)
	memoryRecallCmd.Flags().StringVar(&memoryRecallSymbol, "symbol", "", "return memories anchored/auto-linked to this symbol (symbol_id)")
	memoryRecallCmd.Flags().StringVar(&memoryRecallFile, "file", "", "return memories anchored to this file path (file_path)")
	memoryRecallCmd.Flags().StringVar(&memoryRecallTag, "tag", "", "return memories carrying this tag")
	memoryRecallCmd.Flags().StringVar(&memoryRecallKind, "kind", "", "filter by kind: invariant|constraint|convention|gotcha|decision|incident|reference")
	memoryRecallCmd.Flags().StringVar(&memoryRecallSource, "source", "", "filter by source: manual|distilled|incident|review")
	memoryRecallCmd.Flags().StringVar(&memoryRecallAuthor, "author", "", "filter by author agent")
	memoryRecallCmd.Flags().StringVar(&memoryRecallText, "text", "", "case-insensitive substring filter on body or title")
	memoryRecallCmd.Flags().StringVar(&memoryRecallSince, "since", "", "only memories updated at/after this RFC-3339 timestamp")
	memoryRecallCmd.Flags().IntVar(&memoryRecallMinImportance, "min-importance", 0, "only memories with importance >= this (1..5) (min_importance)")
	memoryRecallCmd.Flags().BoolVar(&memoryRecallPinned, "pinned", false, "return only pinned memories (pinned_only)")
	memoryRecallCmd.Flags().BoolVar(&memoryRecallSuperseded, "include-superseded", false, "include superseded memories (include_superseded)")
	memoryRecallCmd.Flags().IntVar(&memoryRecallLimit, "limit", 50, "cap the result set")
	memoryRecallCmd.Flags().StringVar(&memoryRecallScope, "scope", "", "scope: workspace (default), global, or both")

	// surface (surface_memories)
	memorySurfaceCmd.Flags().StringVar(&memorySurfaceTask, "task", "", "natural-language task description")
	memorySurfaceCmd.Flags().StringVar(&memorySurfaceSymbols, "symbols", "", "comma-separated anchor symbols (symbol_ids)")
	memorySurfaceCmd.Flags().StringVar(&memorySurfaceFiles, "files", "", "comma-separated anchor files (file_paths)")
	memorySurfaceCmd.Flags().IntVar(&memorySurfaceLimit, "limit", 10, "cap the surfaced set")
	memorySurfaceCmd.Flags().Float64Var(&memorySurfaceMinScore, "min-score", 0, "drop hits below this score (min_score)")
	memorySurfaceCmd.Flags().BoolVar(&memorySurfaceSuperseded, "include-superseded", false, "include superseded memories (include_superseded)")
	memorySurfaceCmd.Flags().StringVar(&memorySurfaceScope, "scope", "", "scope: workspace (default), global, or both")
}
