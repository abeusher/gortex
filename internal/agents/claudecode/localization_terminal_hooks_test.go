package claudecode

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRewriteGortexEventMatcherPreservesUserHook(t *testing.T) {
	hooks := map[string]any{
		"PreToolUse": []any{
			managedTestHookEntry("Read", "gortex hook", preToolUseStatusDeny),
			makeHookEntry("Bash", "/usr/bin/user-hook"),
		},
	}
	assert.Equal(t, 1, rewriteGortexEventMatcher(hooks, "PreToolUse", localizationPreToolUseMatcher))
	groups := hooks["PreToolUse"].([]any)
	assert.Equal(t, localizationPreToolUseMatcher, groups[0].(map[string]any)["matcher"])
	assert.Equal(t, "Bash", groups[1].(map[string]any)["matcher"],
		"terminal matcher healing must not rewrite user hooks")
}

func TestInstallHookWithModeHealsTerminalMatchersAndPreservesUserHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.local.json")
	t.Setenv("PATH", t.TempDir())
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				managedTestHookEntry("Read|Grep", "gortex hook", preToolUseStatusDeny),
				managedTestHookEntry("Read|Grep", "gortex hook", "my custom Gortex pre hook"),
				makeHookEntry("Bash", "/usr/bin/user-pre-hook"),
			},
			"PostToolUse": []any{
				managedTestHookEntry("Read|Grep|Glob", "gortex hook --mode=enrich", "Layering Gortex graph context onto tool output..."),
				managedTestHookEntry("CustomTool", "gortex hook", "my custom Gortex post hook"),
				makeHookEntry("Write", "/usr/bin/user-post-hook"),
			},
		},
	}
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(settingsPath, data, 0o644))

	_, err = InstallHookWithMode(io.Discard, settingsPath, HookModeDeny, agentsApplyOptsZero())
	require.NoError(t, err)
	hooks := readSettingsHooks(t, settingsPath)
	pre := hooks["PreToolUse"].([]any)
	post := hooks["PostToolUse"].([]any)
	require.Len(t, pre, 3)
	require.Len(t, post, 3)
	assert.Equal(t, localizationPreToolUseMatcher, pre[0].(map[string]any)["matcher"])
	assert.Equal(t, "Read|Grep", pre[1].(map[string]any)["matcher"])
	assert.Equal(t, "my custom Gortex pre hook", hookStatus(t, pre[1]))
	assert.Equal(t, "Bash", pre[2].(map[string]any)["matcher"])
	assert.Equal(t, localizationPostToolUseMatcher, post[0].(map[string]any)["matcher"])
	assert.Equal(t, "CustomTool", post[1].(map[string]any)["matcher"])
	assert.Equal(t, "my custom Gortex post hook", hookStatus(t, post[1]))
	assert.Equal(t, "Write", post[2].(map[string]any)["matcher"])
	assert.Equal(t, "/usr/bin/user-pre-hook", extractCmd(t, hooks, "PreToolUse", 2))
	assert.Equal(t, "/usr/bin/user-post-hook", extractCmd(t, hooks, "PostToolUse", 2))
}

func managedTestHookEntry(matcher, command, status string) map[string]any {
	entry := makeHookEntry(matcher, command)
	hook := entry["hooks"].([]any)[0].(map[string]any)
	hook["statusMessage"] = status
	return entry
}

func hookStatus(t *testing.T, rawGroup any) string {
	t.Helper()
	group := rawGroup.(map[string]any)
	entry := group["hooks"].([]any)[0].(map[string]any)
	status, _ := entry["statusMessage"].(string)
	return status
}

func TestDesiredPostToolUseMatcherIsModeSpecific(t *testing.T) {
	assert.Equal(t, localizationPostToolUseMatcher, desiredPostToolUseMatcher(HookModeDeny))
	enrich := desiredPostToolUseMatcher(HookModeEnrich)
	assert.Contains(t, enrich, CurrentPostToolUseMatcher)
	for _, tool := range []string{"explore", "search", "read", "relations", "trace", "analyze"} {
		assert.Contains(t, enrich, "mcp__gortex__"+tool)
		assert.Contains(t, enrich, "mcp__plugin_gortex_gortex__"+tool)
	}
}

func TestManagedGortexHookGroupRequiresHandlerLocalPureOwnership(t *testing.T) {
	crossPaired := map[string]any{
		"matcher": "Read|Grep",
		"hooks": []any{
			map[string]any{"type": "command", "command": "gortex hook", "statusMessage": "custom"},
			map[string]any{"type": "command", "command": "/usr/bin/user-hook", "statusMessage": preToolUseStatusDeny},
		},
	}
	if managedGortexHookGroup(crossPaired, "PreToolUse") {
		t.Fatal("command and managed status from different handlers must not establish ownership")
	}

	mixed := map[string]any{
		"matcher": "Read|Grep",
		"hooks": []any{
			map[string]any{"type": "command", "command": "gortex hook", "statusMessage": preToolUseStatusDeny},
			map[string]any{"type": "command", "command": "/usr/bin/user-hook", "statusMessage": "custom"},
		},
	}
	if managedGortexHookGroup(mixed, "PreToolUse") {
		t.Fatal("a mixed managed/user group must be preserved as user-owned")
	}
	hooks := map[string]any{"PreToolUse": []any{crossPaired, mixed}}
	if got := rewriteGortexEventMatcher(hooks, "PreToolUse", localizationPreToolUseMatcher); got != 0 {
		t.Fatalf("rewrote %d mixed/cross-paired groups", got)
	}
}

func TestDedupManagedGortexEntriesOnlyRemovesPureDuplicates(t *testing.T) {
	pureOne := managedTestHookEntry("Read", "gortex hook", preToolUseStatusDeny)
	pureTwo := managedTestHookEntry("Read|Grep", "gortex hook", preToolUseStatusEnrich)
	mixed := map[string]any{
		"matcher": "CustomTool",
		"hooks": []any{
			map[string]any{"type": "command", "command": "gortex hook", "statusMessage": preToolUseStatusDeny},
			map[string]any{"type": "command", "command": "/usr/bin/user-hook", "statusMessage": "custom"},
		},
	}
	hooks := map[string]any{"PreToolUse": []any{pureOne, mixed, pureTwo}}
	if got := dedupManagedGortexEntries(hooks, "PreToolUse"); got != 1 {
		t.Fatalf("removed = %d, want 1 pure duplicate", got)
	}
	groups := hooks["PreToolUse"].([]any)
	require.Len(t, groups, 2)
	assert.Equal(t, pureOne, groups[0])
	assert.Equal(t, mixed, groups[1], "mixed group must survive dedup")
}
