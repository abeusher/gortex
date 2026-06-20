package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capturedMemory records the (tool, args) the memory verb lowered to.
type capturedMemory struct {
	repo string
	tool string
	args map[string]any
}

// runMemory drives the real command tree (rootCmd → memory → subcommand) with
// the given subcommand argv, stubbing the daemon seam so the call never leaves
// the process. It returns the captured call (or nil if none was made), the
// combined out/err buffer, and any error from RunE. A canned JSON result is
// returned from the stub so the renderer path runs end-to-end.
func runMemory(t *testing.T, stdin io.Reader, argv ...string) (*capturedMemory, *bytes.Buffer, error) {
	t.Helper()
	resetMemoryFlags()

	orig := memoryDaemonTool
	t.Cleanup(func() { memoryDaemonTool = orig })

	var cap *capturedMemory
	memoryDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedMemory{repo: repo, tool: tool, args: args}
		return json.RawMessage(`{"status":"ok"}`), nil
	}

	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	if stdin != nil {
		rootCmd.SetIn(stdin)
	} else {
		rootCmd.SetIn(strings.NewReader(""))
	}
	rootCmd.SetArgs(append([]string{"memory"}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

// resetMemoryFlags restores every memory-group flag global to its zero/default
// so tests don't leak state into each other, and clears each flag's cobra
// Changed bit (load-bearing: only-Changed flags are forwarded to the daemon).
func resetMemoryFlags() {
	resetCobraFlags(memoryCmd)

	memoryIndex = "."
	memoryFormat = "text"

	memoryNoteBody, memoryNoteSymbol, memoryNoteFile = "", "", ""
	memoryNoteTags, memoryNoteLinks = "", ""
	memoryNotePin, memoryNoteID, memoryNoteNoAutolink = false, "", false

	memoryNotesSymbol, memoryNotesFile, memoryNotesTag, memoryNotesText = "", "", "", ""
	memoryNotesSession, memoryNotesSince = "", ""
	memoryNotesLimit, memoryNotesPinned = 50, false

	memoryDistillSession = ""
	memoryDistillMaxSymbols, memoryDistillMaxFiles = 10, 10
	memoryDistillMaxTags, memoryDistillMaxRecent = 10, 8

	memoryStoreBody, memoryStoreTitle, memoryStoreSymbols, memoryStoreFiles = "", "", "", ""
	memoryStoreTags, memoryStoreKind, memoryStoreSource = "", "", ""
	memoryStoreImportance, memoryStoreConfidence = 3, 1.0
	memoryStorePin, memoryStoreSupersedes, memoryStoreScope = false, "", ""
	memoryStoreID, memoryStoreNoAutolink = "", false

	memoryRecallSymbol, memoryRecallFile, memoryRecallTag, memoryRecallKind = "", "", "", ""
	memoryRecallSource, memoryRecallAuthor, memoryRecallText, memoryRecallSince = "", "", "", ""
	memoryRecallMinImportance = 0
	memoryRecallPinned, memoryRecallSuperseded = false, false
	memoryRecallLimit, memoryRecallScope = 50, ""

	memorySurfaceTask, memorySurfaceSymbols, memorySurfaceFiles = "", "", ""
	memorySurfaceLimit, memorySurfaceMinScore = 10, 0
	memorySurfaceSuperseded, memorySurfaceScope = false, ""
}

// --- note (save_note) ------------------------------------------------------

func TestMemoryNote_Lowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "note",
		"--body", "x", "--symbol", "pkg/a.go::Foo", "--file", "pkg/a.go",
		"--tags", "decision,bug", "--links", "pkg/b.go::Bar", "--pin",
		"--no-autolink")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "save_note", cap.tool)
	require.Equal(t, "x", cap.args["body"])
	require.Equal(t, "pkg/a.go::Foo", cap.args["symbol_id"])
	require.Equal(t, "pkg/a.go", cap.args["file_path"])
	require.Equal(t, "decision,bug", cap.args["tags"])
	require.Equal(t, "pkg/b.go::Bar", cap.args["links"])
	require.Equal(t, true, cap.args["pinned"])
	require.Equal(t, true, cap.args["no_autolink"])
}

func TestMemoryNote_BodyAndPin(t *testing.T) {
	cap, _, err := runMemory(t, nil, "note", "--body", "x", "--pin")
	require.NoError(t, err)
	require.Equal(t, "save_note", cap.tool)
	require.Equal(t, "x", cap.args["body"])
	require.Equal(t, true, cap.args["pinned"])
	// Only the two set flags must be forwarded.
	require.NotContains(t, cap.args, "symbol_id")
	require.NotContains(t, cap.args, "file_path")
	require.NotContains(t, cap.args, "tags")
	require.NotContains(t, cap.args, "no_autolink")
}

func TestMemoryNote_UpdateByID(t *testing.T) {
	cap, _, err := runMemory(t, nil, "note", "--id", "n1", "--body", "y")
	require.NoError(t, err)
	require.Equal(t, "n1", cap.args["id"])
	require.Equal(t, "y", cap.args["body"])
}

// --- notes (query_notes) ---------------------------------------------------

func TestMemoryNotes_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runMemory(t, nil, "notes", "--tag", "decision")
	require.NoError(t, err)
	require.Equal(t, "query_notes", cap.tool)
	require.Equal(t, "decision", cap.args["tag"])
	// Nothing else — unset flags hold the daemon's own defaults.
	require.NotContains(t, cap.args, "symbol_id")
	require.NotContains(t, cap.args, "file_path")
	require.NotContains(t, cap.args, "text")
	require.NotContains(t, cap.args, "session_id")
	require.NotContains(t, cap.args, "since")
	require.NotContains(t, cap.args, "limit")
	require.NotContains(t, cap.args, "pinned_only")
}

func TestMemoryNotes_FullLowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "notes",
		"--symbol", "pkg/a.go::Foo", "--file", "pkg/a.go", "--tag", "bug",
		"--text", "panic", "--session", "all", "--since", "2026-01-01T00:00:00Z",
		"--limit", "25", "--pinned")
	require.NoError(t, err)
	require.Equal(t, "pkg/a.go::Foo", cap.args["symbol_id"])
	require.Equal(t, "pkg/a.go", cap.args["file_path"])
	require.Equal(t, "bug", cap.args["tag"])
	require.Equal(t, "panic", cap.args["text"])
	require.Equal(t, "all", cap.args["session_id"])
	require.Equal(t, "2026-01-01T00:00:00Z", cap.args["since"])
	require.Equal(t, 25, cap.args["limit"])
	require.Equal(t, true, cap.args["pinned_only"])
}

// --- distill (distill_session) ---------------------------------------------

func TestMemoryDistill_Lowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "distill",
		"--session", "all", "--max-symbols", "5", "--max-files", "6",
		"--max-tags", "7", "--max-recent", "8")
	require.NoError(t, err)
	require.Equal(t, "distill_session", cap.tool)
	require.Equal(t, "all", cap.args["session_id"])
	require.Equal(t, 5, cap.args["max_symbols"])
	require.Equal(t, 6, cap.args["max_files"])
	require.Equal(t, 7, cap.args["max_tags"])
	require.Equal(t, 8, cap.args["max_recent"])
}

func TestMemoryDistill_OmitsUnsetNumerics(t *testing.T) {
	cap, _, err := runMemory(t, nil, "distill")
	require.NoError(t, err)
	require.Equal(t, "distill_session", cap.tool)
	require.NotContains(t, cap.args, "session_id")
	require.NotContains(t, cap.args, "max_symbols")
	require.NotContains(t, cap.args, "max_files")
	require.NotContains(t, cap.args, "max_tags")
	require.NotContains(t, cap.args, "max_recent")
}

// --- store (store_memory) --------------------------------------------------

func TestMemoryStore_KindImportanceSymbols(t *testing.T) {
	cap, _, err := runMemory(t, nil, "store",
		"--kind", "decision", "--importance", "5", "--symbols", "a,b")
	require.NoError(t, err)
	require.Equal(t, "store_memory", cap.tool)
	require.Equal(t, "decision", cap.args["kind"])
	require.Equal(t, 5, cap.args["importance"])
	require.Equal(t, "a,b", cap.args["symbol_ids"])
	// Numeric/bool defaults stay unset.
	require.NotContains(t, cap.args, "confidence")
	require.NotContains(t, cap.args, "pinned")
	require.NotContains(t, cap.args, "title")
}

func TestMemoryStore_FullLowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "store",
		"--body", "Bar must hold the lock", "--title", "lock invariant",
		"--symbols", "pkg/a.go::Bar", "--files", "pkg/a.go", "--tags", "invariant",
		"--kind", "invariant", "--source", "manual",
		"--importance", "5", "--confidence", "0.9", "--pin",
		"--supersedes", "m0", "--scope", "global", "--no-autolink")
	require.NoError(t, err)
	require.Equal(t, "Bar must hold the lock", cap.args["body"])
	require.Equal(t, "lock invariant", cap.args["title"])
	require.Equal(t, "pkg/a.go::Bar", cap.args["symbol_ids"])
	require.Equal(t, "pkg/a.go", cap.args["file_paths"])
	require.Equal(t, "invariant", cap.args["tags"])
	require.Equal(t, "invariant", cap.args["kind"])
	require.Equal(t, "manual", cap.args["source"])
	require.Equal(t, 5, cap.args["importance"])
	require.Equal(t, 0.9, cap.args["confidence"])
	require.Equal(t, true, cap.args["pinned"])
	require.Equal(t, "m0", cap.args["supersedes"])
	require.Equal(t, "global", cap.args["scope"])
	require.Equal(t, true, cap.args["no_autolink"])
}

func TestMemoryStore_UpdateByID(t *testing.T) {
	cap, _, err := runMemory(t, nil, "store", "--id", "m1", "--body", "corrected")
	require.NoError(t, err)
	require.Equal(t, "m1", cap.args["id"])
	require.Equal(t, "corrected", cap.args["body"])
	require.NotContains(t, cap.args, "kind")
	require.NotContains(t, cap.args, "importance")
}

// --- recall (query_memories) -----------------------------------------------

func TestMemoryRecall_OnlyChangedFlagsSent(t *testing.T) {
	cap, _, err := runMemory(t, nil, "recall", "--tag", "gotcha")
	require.NoError(t, err)
	require.Equal(t, "query_memories", cap.tool)
	require.Equal(t, "gotcha", cap.args["tag"])
	require.NotContains(t, cap.args, "symbol_id")
	require.NotContains(t, cap.args, "file_path")
	require.NotContains(t, cap.args, "kind")
	require.NotContains(t, cap.args, "source")
	require.NotContains(t, cap.args, "author")
	require.NotContains(t, cap.args, "text")
	require.NotContains(t, cap.args, "since")
	require.NotContains(t, cap.args, "min_importance")
	require.NotContains(t, cap.args, "pinned_only")
	require.NotContains(t, cap.args, "include_superseded")
	require.NotContains(t, cap.args, "limit")
	require.NotContains(t, cap.args, "scope")
}

func TestMemoryRecall_FullLowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "recall",
		"--symbol", "pkg/a.go::Bar", "--file", "pkg/a.go", "--tag", "invariant",
		"--kind", "invariant", "--source", "review", "--author", "claude",
		"--text", "lock", "--since", "2026-01-01T00:00:00Z",
		"--min-importance", "4", "--pinned", "--include-superseded",
		"--limit", "10", "--scope", "both")
	require.NoError(t, err)
	require.Equal(t, "pkg/a.go::Bar", cap.args["symbol_id"])
	require.Equal(t, "pkg/a.go", cap.args["file_path"])
	require.Equal(t, "invariant", cap.args["tag"])
	require.Equal(t, "invariant", cap.args["kind"])
	require.Equal(t, "review", cap.args["source"])
	require.Equal(t, "claude", cap.args["author"])
	require.Equal(t, "lock", cap.args["text"])
	require.Equal(t, "2026-01-01T00:00:00Z", cap.args["since"])
	require.Equal(t, 4, cap.args["min_importance"])
	require.Equal(t, true, cap.args["pinned_only"])
	require.Equal(t, true, cap.args["include_superseded"])
	require.Equal(t, 10, cap.args["limit"])
	require.Equal(t, "both", cap.args["scope"])
}

// --- surface (surface_memories) --------------------------------------------

func TestMemorySurface_Lowers(t *testing.T) {
	cap, _, err := runMemory(t, nil, "surface",
		"--task", "fix the lock", "--symbols", "a,b", "--files", "pkg/a.go",
		"--limit", "5", "--min-score", "0.5", "--include-superseded",
		"--scope", "workspace")
	require.NoError(t, err)
	require.Equal(t, "surface_memories", cap.tool)
	require.Equal(t, "fix the lock", cap.args["task"])
	require.Equal(t, "a,b", cap.args["symbol_ids"])
	require.Equal(t, "pkg/a.go", cap.args["file_paths"])
	require.Equal(t, 5, cap.args["limit"])
	require.Equal(t, 0.5, cap.args["min_score"])
	require.Equal(t, true, cap.args["include_superseded"])
	require.Equal(t, "workspace", cap.args["scope"])
}

func TestMemorySurface_OnlyTask(t *testing.T) {
	cap, _, err := runMemory(t, nil, "surface", "--task", "x")
	require.NoError(t, err)
	require.Equal(t, "x", cap.args["task"])
	require.NotContains(t, cap.args, "symbol_ids")
	require.NotContains(t, cap.args, "file_paths")
	require.NotContains(t, cap.args, "limit")
	require.NotContains(t, cap.args, "min_score")
	require.NotContains(t, cap.args, "include_superseded")
	require.NotContains(t, cap.args, "scope")
}

// --- render & daemon-required ----------------------------------------------

// TestMemory_FormatJSONRenders asserts --format json pretty-prints the daemon
// result through emitDaemonJSON.
func TestMemory_FormatJSONRenders(t *testing.T) {
	_, buf, err := runMemory(t, nil, "recall", "--tag", "x", "--format", "json")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"status": "ok"`)
}

// TestMemoryDaemonRequired asserts that, with no daemon and no stub, a
// subcommand returns the actionable daemon-required error.
func TestMemoryDaemonRequired(t *testing.T) {
	resetMemoryFlags()
	// No stub: the real memoryDaemonTool runs against a temp dir no daemon tracks.
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"memory", "recall", "--tag", "x", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
