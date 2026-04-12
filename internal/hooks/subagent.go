package hooks

import (
	"strings"
)

// enrichTask produces a condensed graph-orientation briefing for a Task
// (subagent) spawn. Claude Code's `Task` tool receives PreToolUse
// additionalContext before the subagent begins, so this is the hook point
// for "subagent start".
//
// The briefing combines:
//   - repo orientation (graph_stats)
//   - task-relevant symbols (smart_context over description + prompt)
//   - recently-modified symbols from this session (get_symbol_history)
//
// Returns an empty result when the bridge is unreachable or when there is no
// meaningful task text to derive context from. The hook must degrade silently
// and never block subagent spawning.
func enrichTask(toolInput map[string]any, port int) enrichResult {
	description, _ := toolInput["description"].(string)
	prompt, _ := toolInput["prompt"].(string)

	task := strings.TrimSpace(description + "\n" + prompt)
	if task == "" {
		return enrichResult{}
	}
	// Cap the task text we send to the bridge — full prompts can be huge.
	const maxTaskLen = 2000
	if len(task) > maxTaskLen {
		task = task[:maxTaskLen]
	}

	stats := callBridgeTool(port, "graph_stats", nil)
	if stats == "" {
		// Bridge unreachable — silent.
		return enrichResult{}
	}

	var sb strings.Builder
	sb.WriteString("[Gortex] Subagent briefing — prefer graph tools over Read/Grep:\n\n")

	if summary := renderStatsSummary(stats); summary != "" {
		sb.WriteString("**Index:** ")
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}

	if ctx := renderTaskContext(port, task); ctx != "" {
		sb.WriteString("### Relevant Symbols (from `smart_context`)\n\n")
		sb.WriteString(ctx)
		sb.WriteString("\n")
	}

	if churn := renderSymbolHistory(port); churn != "" {
		sb.WriteString("### Recently Modified (this session)\n\n")
		sb.WriteString(churn)
		sb.WriteString("\n")
	}

	sb.WriteString("_Start with `smart_context` / `get_editing_context` / `get_symbol_source` — avoid re-exploring via Read/Grep._\n")

	return enrichResult{context: sb.String()}
}

// renderTaskContext calls smart_context with the subagent task text and
// returns a compacted body. Falls back to empty on any error.
func renderTaskContext(port int, task string) string {
	raw := callBridgeTool(port, "smart_context", map[string]any{
		"task":    task,
		"compact": true,
	})
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return cappedLines(raw, 12)
}
