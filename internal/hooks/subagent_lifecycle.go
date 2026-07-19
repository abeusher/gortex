package hooks

import "time"

// runSubagentStart establishes an agent-scoped turn. Claude subagents do not
// receive UserPromptSubmit, so this lifecycle hook is their authoritative turn
// boundary. Missing identity fields fail open and emit no hook output.
func runSubagentStart(data []byte) {
	started := time.Now()
	_ = beginLocalizationSubagentFromHook(data)
	logHookEffectivenessUnknown("SubagentStart", false, 0, time.Since(started))
}

// runSubagentStop removes the exact agent namespace. A delayed PostToolUse may
// still finish afterward, but its old token cannot match a future reused
// agent_id, whose SubagentStart always rotates the namespace first.
func runSubagentStop(data []byte) {
	started := time.Now()
	_ = endLocalizationSubagentFromHook(data)
	logHookEffectivenessUnknown("SubagentStop", false, 0, time.Since(started))
}
