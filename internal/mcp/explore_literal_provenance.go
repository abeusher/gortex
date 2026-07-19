package mcp

import (
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Terminal-confidence provenance.
//
// A quoted-literal match may make localization terminal, but terminal
// confidence must be supported by evidence the agent can actually see: if
// the envelope sheds the matching source body under its byte budget, the
// session is told "answer now" while holding no visible trace of the very
// evidence that justified stopping. Literal-driven terminality therefore
// requires the serialized envelope to contain the literal; otherwise the
// contract downgrades to the ordinary bounded refinement read.

// exploreAnswerReadyViaLiteralOnly reports whether a terminal verdict rests
// solely on the exact-content rung: with the head's literal flags stripped,
// the same targets would not be terminal. Callers invoke it only after
// exploreAnswerReady returned true.
func exploreAnswerReadyViaLiteralOnly(task string, targets []exploreTarget) bool {
	if len(targets) == 0 || !targets[0].exactContent {
		return false
	}
	stripped := append([]exploreTarget(nil), targets...)
	stripped[0].exactContent = false
	stripped[0].exactContentAmbiguous = false
	return !exploreAnswerReady(task, stripped)
}

// exploreResultCitesTaskLiteral reports whether the serialized localization
// result visibly contains one of the task's quoted literals. Case-exact for
// double-quoted content evidence; a task without quoted literals passes
// vacuously (the gate only arms on literal-driven terminality).
func exploreResultCitesTaskLiteral(result *mcpgo.CallToolResult, task string) bool {
	literals := exploreQuotedRecallTerms(task)
	if len(literals) == 0 {
		return true
	}
	if result == nil || len(result.Content) == 0 {
		return false
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		return false
	}
	for _, literal := range literals {
		if literal != "" && strings.Contains(text.Text, literal) {
			return true
		}
	}
	return false
}
