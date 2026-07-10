package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// momentumReadThreshold is the per-session graph-read count after which the
// one-shot momentum note is attached. Matches the documented "one explore /
// smart_context call replaces 5-10 exploration calls" shape: past that many
// granular reads the session has out-read the one-shot phase and is usually
// sitting on enough evidence to act.
const momentumReadThreshold = 6

// momentumReadTools is the set of read/navigate tools that count toward the
// momentum note. Edit and verify tools never count — the note is about
// evidence-gathering loops, not about acting.
var momentumReadTools = map[string]bool{
	"explore": true, "search_symbols": true, "search_text": true,
	"get_symbol_source": true, "batch_symbols": true, "get_file_summary": true,
	"get_editing_context": true, "read_file": true, "find_usages": true,
	"get_callers": true, "get_call_chain": true, "find_implementations": true,
	"get_dependencies": true, "get_dependents": true, "find_files": true,
}

// momentumNote is attached ONCE per session, to the response of the
// threshold-crossing read call. Generic turn-economy guidance for any
// budgeted agent: everything already returned is real and citeable, so a
// conclusion can usually be written before fetching more.
func momentumNote(n int) string {
	return fmt.Sprintf(
		"(Session note: graph read #%d. Every location returned so far is real and citeable — "+
			"if you are localizing or answering a question, consider writing your conclusion from "+
			"what you already hold and fetching only what it still lacks. This note appears once.)", n)
}

// maybeAttachMomentumNote counts this session's read-tool calls and, exactly
// once per session when the count crosses the threshold, appends the
// momentum note to the response. Nil-safe pass-through for non-read tools,
// error results, and sessionless contexts.
func (s *Server) maybeAttachMomentumNote(ctx context.Context, toolName string, res *mcp.CallToolResult) *mcp.CallToolResult {
	if res == nil || res.IsError || !momentumReadTools[toolName] {
		return res
	}
	sess := s.sessionFor(ctx)
	if sess == nil {
		return res
	}
	sess.mu.Lock()
	sess.momentumReads++
	fire := !sess.momentumNudged && sess.momentumReads >= momentumReadThreshold
	if fire {
		sess.momentumNudged = true
	}
	n := sess.momentumReads
	sess.mu.Unlock()
	if !fire {
		return res
	}
	res.Content = append(res.Content, mcp.NewTextContent(momentumNote(n)))
	return res
}
