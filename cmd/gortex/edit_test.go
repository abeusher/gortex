package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// capturedEdit records the (tool, args) the edit verb lowered to.
type capturedEdit struct {
	repo string
	tool string
	args map[string]any
}

// runEdit drives the real command tree (rootCmd → edit → subcommand) with the
// given subcommand argv, stubbing the daemon seam so the call never leaves the
// process. It returns the captured call (or nil if none was made), the combined
// out/err buffer, and any error from RunE. A canned JSON result is returned from
// the stub so the renderer path runs end-to-end.
//
// argv is the edit-subcommand argv (e.g. "context", "internal/foo.go"); the
// helper prepends "edit" so cobra routes through the real edit parent — calling
// editCmd.Execute() directly would re-root at rootCmd and collide with the
// unrelated top-level `context` command.
func runEdit(t *testing.T, stdin io.Reader, argv ...string) (*capturedEdit, *bytes.Buffer, error) {
	t.Helper()
	resetEditFlags()

	orig := editDaemonTool
	t.Cleanup(func() { editDaemonTool = orig })

	var cap *capturedEdit
	editDaemonTool = func(repo, tool string, args map[string]any) (json.RawMessage, error) {
		cap = &capturedEdit{repo: repo, tool: tool, args: args}
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
	rootCmd.SetArgs(append([]string{"edit"}, argv...))
	err := rootCmd.Execute()
	return cap, buf, err
}

// resetEditFlags restores every edit-group flag global to its zero/default so
// tests don't leak state into each other (cobra persists parsed values on the
// shared command vars). It also clears each flag's cobra-internal Changed bit,
// which Execute() leaves set across runs — Changed() is load-bearing for the
// --old/--new "required" check, so a stale bit would mask a missing flag.
func resetEditFlags() {
	resetCobraFlags(editCmd)

	editIndex = "."
	editFormat = "text"

	editContextDetail, editContextCompress = "", false

	editChanges = nil
	editVerifyChangesInline, editVerifyChangesFile, editVerifyCompact = "", "", false

	editPlanIDs, editPlanDepth = "", 3

	editPreviewWEInline, editPreviewWEFile = "", ""
	editPreviewNoDiag, editPreviewInheritOver = false, false

	editSimulateStepsInline, editSimulateStepsFile = "", ""
	editSimulateKeep, editSimulateNoStop, editSimulateInheritOver = false, false, false

	editBatchInline, editBatchFile, editBatchDryRun, editBatchCompact = "", "", false, false

	editApplyOld, editApplyNew = "", ""
	editApplyReplaceAll, editApplyDryRun, editApplyAllowParse, editApplyExpected = false, false, false, 0

	editSymbolOld, editSymbolNew, editSymbolDryRun = "", "", false

	editRenameTo = ""

	editGuardsIDs, editGuardsCompact = "", false

	editTestsIDs, editTestsDepth = "", 3

	editContractSource, editContractLens = "", ""
	editContractRiskGate, editContractAck = false, false
	editContractBase, editContractWEInline, editContractWEFile = "", "", ""
	editContractSymbols, editContractRanges, editContractRangeFile = "", "", ""
	editContractPath, editContractStartLine, editContractEndLine = "", 0, 0

	editSafeDeleteApply, editSafeDeleteForce = false, false
	editSafeDeleteCascade, editSafeDeleteIntoTests, editSafeDeletePropagate = "", false, false
}

// resetCobraFlags clears the Changed bit on every flag of cmd and its
// subcommands, recursively. cobra sets Changed in place during Execute() and
// never clears it, so without this a flag set in one test stays "Changed" in
// the next — which would mask the --old/--new "required" Changed() check. The
// flag VALUES are restored by the explicit Go-variable resets above (the flag
// vars are package globals bound via *Var), so this only touches Changed; in
// particular it avoids re-Set-ing a StringArray default, which would append.
func resetCobraFlags(cmd *cobra.Command) {
	clear := func(f *pflag.Flag) { f.Changed = false }
	cmd.Flags().VisitAll(clear)
	cmd.PersistentFlags().VisitAll(clear)
	for _, c := range cmd.Commands() {
		resetCobraFlags(c)
	}
}

// --- per-subcommand lowering assertions ------------------------------------

func TestEditContext_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "context", "internal/foo.go", "--detail", "full", "--compress")
	require.NoError(t, err)
	require.NotNil(t, cap)
	require.Equal(t, "get_editing_context", cap.tool)
	require.Equal(t, "internal/foo.go", cap.args["path"])
	require.Equal(t, "full", cap.args["detail"])
	require.Equal(t, true, cap.args["compress_bodies"])
}

func TestEditContext_OmitsUnsetFlags(t *testing.T) {
	cap, _, err := runEdit(t, nil, "context", "internal/foo.go")
	require.NoError(t, err)
	require.Equal(t, "get_editing_context", cap.tool)
	require.NotContains(t, cap.args, "detail")
	require.NotContains(t, cap.args, "compress_bodies")
}

func TestEditVerify_ChangeSugarBuildsArray(t *testing.T) {
	cap, _, err := runEdit(t, nil, "verify",
		"--change", "pkg/a.go::A=sig1",
		"--change", "pkg/b.go::B=func(x int) error")
	require.NoError(t, err)
	require.Equal(t, "verify_change", cap.tool)

	changesJSON, ok := cap.args["changes"].(string)
	require.True(t, ok, "changes must be a JSON string")
	var got []map[string]string
	require.NoError(t, json.Unmarshal([]byte(changesJSON), &got))
	require.Equal(t, []map[string]string{
		{"symbol_id": "pkg/a.go::A", "new_signature": "sig1"},
		{"symbol_id": "pkg/b.go::B", "new_signature": "func(x int) error"},
	}, got)
}

func TestEditVerify_SplitsOnFirstEquals(t *testing.T) {
	cap, _, err := runEdit(t, nil, "verify", "--change", "id=func() (a=b, error)")
	require.NoError(t, err)
	changesJSON := cap.args["changes"].(string)
	var got []map[string]string
	require.NoError(t, json.Unmarshal([]byte(changesJSON), &got))
	require.Equal(t, "id", got[0]["symbol_id"])
	require.Equal(t, "func() (a=b, error)", got[0]["new_signature"])
}

func TestEditVerify_ChangesInlineJSON(t *testing.T) {
	cap, _, err := runEdit(t, nil, "verify",
		"--changes", `[{"symbol_id":"x","new_signature":"y"}]`, "--compact")
	require.NoError(t, err)
	require.Equal(t, `[{"symbol_id":"x","new_signature":"y"}]`, cap.args["changes"])
	require.Equal(t, true, cap.args["compact"])
}

func TestEditVerify_ChangesStdin(t *testing.T) {
	cap, _, err := runEdit(t, strings.NewReader(`[{"symbol_id":"s","new_signature":"sig"}]`),
		"verify", "--changes", "-")
	require.NoError(t, err)
	require.Equal(t, `[{"symbol_id":"s","new_signature":"sig"}]`, cap.args["changes"])
}

func TestEditVerify_RequiresChanges(t *testing.T) {
	_, _, err := runEdit(t, nil, "verify")
	require.Error(t, err)
}

func TestEditPlan_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "plan", "--ids", "a,b", "--depth", "5")
	require.NoError(t, err)
	require.Equal(t, "get_edit_plan", cap.tool)
	require.Equal(t, "a,b", cap.args["ids"])
	require.Equal(t, 5, cap.args["depth"])
}

func TestEditPlan_DefaultDepth(t *testing.T) {
	cap, _, err := runEdit(t, nil, "plan", "--ids", "a")
	require.NoError(t, err)
	require.Equal(t, 3, cap.args["depth"])
}

func TestEditPlan_RequiresIDs(t *testing.T) {
	_, _, err := runEdit(t, nil, "plan")
	require.Error(t, err)
}

func TestEditPreview_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "preview",
		"--workspace-edit", `{"changes":{}}`, "--no-diagnostics", "--inherit-overlay")
	require.NoError(t, err)
	require.Equal(t, "preview_edit", cap.tool)
	require.Equal(t, `{"changes":{}}`, cap.args["workspace_edit"])
	require.Equal(t, false, cap.args["diagnostics"])
	require.Equal(t, true, cap.args["inherit_overlay"])
}

func TestEditPreview_DiagnosticsDefaultOmitted(t *testing.T) {
	cap, _, err := runEdit(t, nil, "preview", "--workspace-edit", `{"changes":{}}`)
	require.NoError(t, err)
	// diagnostics defaults to true on the daemon — the CLI omits it unless
	// --no-diagnostics turns it off.
	require.NotContains(t, cap.args, "diagnostics")
}

func TestEditSimulate_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "simulate",
		"--steps", `[{"changes":{}}]`, "--keep", "--no-stop-on-error")
	require.NoError(t, err)
	require.Equal(t, "simulate_chain", cap.tool)
	require.Equal(t, `[{"changes":{}}]`, cap.args["steps"])
	require.Equal(t, true, cap.args["keep"])
	require.Equal(t, false, cap.args["stop_on_error"])
}

func TestEditBatch_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "batch",
		"--edits", `[{"id":"x","old_source":"a","new_source":"b"}]`, "--dry-run", "--compact")
	require.NoError(t, err)
	require.Equal(t, "batch_edit", cap.tool)
	require.Equal(t, `[{"id":"x","old_source":"a","new_source":"b"}]`, cap.args["edits"])
	require.Equal(t, true, cap.args["dry_run"])
	require.Equal(t, true, cap.args["compact"])
}

func TestEditBatch_EditsFromFile(t *testing.T) {
	path := writeTempJSON(t, `[{"id":"x","old_source":"a","new_source":"b"}]`)
	cap, _, err := runEdit(t, nil, "batch", "--edits-file", path)
	require.NoError(t, err)
	require.Equal(t, `[{"id":"x","old_source":"a","new_source":"b"}]`, cap.args["edits"])
}

func TestEditApply_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "apply", "internal/foo.go",
		"--old", "before", "--new", "after",
		"--replace-all", "--dry-run", "--allow-parse-errors", "--expected", "3")
	require.NoError(t, err)
	require.Equal(t, "edit_file", cap.tool)
	require.Equal(t, "internal/foo.go", cap.args["path"])
	require.Equal(t, "before", cap.args["old_string"])
	require.Equal(t, "after", cap.args["new_string"])
	require.Equal(t, true, cap.args["replace_all"])
	require.Equal(t, true, cap.args["dry_run"])
	require.Equal(t, true, cap.args["allow_parse_errors"])
	require.Equal(t, 3, cap.args["expected_occurrences"])
}

func TestEditApply_RequiresOldAndNew(t *testing.T) {
	_, _, err := runEdit(t, nil, "apply", "internal/foo.go", "--old", "x")
	require.Error(t, err)
}

func TestEditApply_EmptyNewIsAllowed(t *testing.T) {
	// An empty --new (deletion) is a legitimate edit — the Changed() check,
	// not a non-empty value, gates it.
	cap, _, err := runEdit(t, nil, "apply", "internal/foo.go", "--old", "x", "--new", "")
	require.NoError(t, err)
	require.Equal(t, "", cap.args["new_string"])
}

func TestEditSymbol_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "symbol", "pkg/a.go::Foo",
		"--old", "oldbody", "--new", "newbody", "--dry-run")
	require.NoError(t, err)
	require.Equal(t, "edit_symbol", cap.tool)
	require.Equal(t, "pkg/a.go::Foo", cap.args["id"])
	require.Equal(t, "oldbody", cap.args["old_source"])
	require.Equal(t, "newbody", cap.args["new_source"])
	require.Equal(t, true, cap.args["dry_run"])
}

func TestEditRename_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "rename", "pkg/a.go::Foo", "--to", "Bar")
	require.NoError(t, err)
	require.Equal(t, "rename_symbol", cap.tool)
	require.Equal(t, "pkg/a.go::Foo", cap.args["id"])
	require.Equal(t, "Bar", cap.args["new_name"])
}

func TestEditRename_RequiresTo(t *testing.T) {
	_, _, err := runEdit(t, nil, "rename", "pkg/a.go::Foo")
	require.Error(t, err)
}

func TestEditGuards_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "guards", "--ids", "a,b,c", "--compact")
	require.NoError(t, err)
	require.Equal(t, "check_guards", cap.tool)
	require.Equal(t, "a,b,c", cap.args["ids"])
	require.Equal(t, true, cap.args["compact"])
}

func TestEditTests_Lowers(t *testing.T) {
	cap, _, err := runEdit(t, nil, "tests", "--ids", "a,b", "--depth", "4")
	require.NoError(t, err)
	require.Equal(t, "get_test_targets", cap.tool)
	require.Equal(t, "a,b", cap.args["ids"])
	require.Equal(t, 4, cap.args["depth"])
}

func TestEditContract_SymbolsSource(t *testing.T) {
	cap, _, err := runEdit(t, nil, "contract",
		"--source", "symbols", "--symbols", "pkg/a.go::A,pkg/b.go::B",
		"--lens", "api", "--risk-gate")
	require.NoError(t, err)
	require.Equal(t, "change_contract", cap.tool)
	require.Equal(t, "symbols", cap.args["source"])
	require.Equal(t, "pkg/a.go::A,pkg/b.go::B", cap.args["symbols"])
	require.Equal(t, "api", cap.args["lens"])
	require.Equal(t, true, cap.args["risk_gate"])
}

func TestEditContract_RangesSingleFile(t *testing.T) {
	cap, _, err := runEdit(t, nil, "contract",
		"--source", "ranges", "--path", "internal/foo.go",
		"--start-line", "10", "--end-line", "20")
	require.NoError(t, err)
	require.Equal(t, "internal/foo.go", cap.args["path"])
	require.Equal(t, 10, cap.args["start_line"])
	require.Equal(t, 20, cap.args["end_line"])
}

func TestEditContract_EditSource(t *testing.T) {
	cap, _, err := runEdit(t, nil, "contract",
		"--source", "edit", "--workspace-edit", `{"changes":{}}`)
	require.NoError(t, err)
	require.Equal(t, `{"changes":{}}`, cap.args["workspace_edit"])
}

func TestEditContract_NoRequiredFlags(t *testing.T) {
	// change_contract has no required flags — an argument-free call (source=auto)
	// must lower cleanly.
	cap, _, err := runEdit(t, nil, "contract")
	require.NoError(t, err)
	require.Equal(t, "change_contract", cap.tool)
	require.NotContains(t, cap.args, "source")
}

func TestEditSafeDelete_DryRunByDefault(t *testing.T) {
	cap, buf, err := runEdit(t, nil, "safe-delete", "pkg/a.go::Foo")
	require.NoError(t, err)
	require.Equal(t, "safe_delete_symbol", cap.tool)
	require.Equal(t, "pkg/a.go::Foo", cap.args["id"])
	require.Equal(t, true, cap.args["dry_run"], "default must be a dry run")
	require.Contains(t, buf.String(), "dry run")
}

func TestEditSafeDelete_ApplyFlipsDryRun(t *testing.T) {
	cap, _, err := runEdit(t, nil, "safe-delete", "pkg/a.go::Foo", "--apply")
	require.NoError(t, err)
	require.Equal(t, false, cap.args["dry_run"], "--apply must commit (dry_run=false)")
}

func TestEditSafeDelete_CascadeAndPropagate(t *testing.T) {
	cap, _, err := runEdit(t, nil, "safe-delete", "pkg/a.go::Foo",
		"--cascade", "preview", "--cascade-into-tests", "--propagate", "--force")
	require.NoError(t, err)
	require.Equal(t, "preview", cap.args["cascade"])
	require.Equal(t, true, cap.args["cascade_into_tests"])
	require.Equal(t, true, cap.args["propagate"])
	require.Equal(t, true, cap.args["force"])
}

// TestEdit_FormatJSONRenders asserts --format json pretty-prints the daemon
// result through emitDaemonJSON.
func TestEdit_FormatJSONRenders(t *testing.T) {
	_, buf, err := runEdit(t, nil, "guards", "--ids", "a", "--format", "json")
	require.NoError(t, err)
	require.Contains(t, buf.String(), `"status": "ok"`)
}

// TestEditDaemonRequired asserts that, with no daemon and no stub, a subcommand
// returns the actionable daemon-required error (mirrors
// query_daemon_required_test.go / call_test.go).
func TestEditDaemonRequired(t *testing.T) {
	resetEditFlags()
	// No stub: the real editDaemonTool runs against a temp dir no daemon tracks.
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"edit", "plan", "--ids", "a", "--index", t.TempDir()})
	err := rootCmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "gortex track")
}
