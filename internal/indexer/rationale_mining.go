package indexer

import "regexp"

var (
	// adrMarkerRe matches Architecture-Decision-Record / decision-doc markers:
	// front-matter keys (implemented_by, supersedes), an ADR id, or a Decision
	// heading. A chunk carrying one is high-confidence rationale.
	adrMarkerRe = regexp.MustCompile(`(?i)(implemented[_ ]by|supersed(es|ed[_ ]by)|\badr-\d+\b|#+\s*decision\b|decision record)`)
	// rfc2119Re matches the normative keywords (upper-case per the RFC) that
	// mark a requirement statement.
	rfc2119Re = regexp.MustCompile(`\b(MUST NOT|SHALL NOT|MUST|SHALL|SHOULD|REQUIRED)\b`)
	// ticketRe matches a JIRA-style issue id (ABC-123).
	ticketRe = regexp.MustCompile(`\b[A-Z]{2,}-\d+\b`)
)

// mineDocSignal classifies a content chunk's text for structured rationale
// signals, returning the strongest: "adr" (a decision record), "rfc2119" (a
// normative requirement), "ticket" (an issue reference), or "lexical" (a plain
// name match). The signal rides on the EdgeMotivates edge so a consumer can
// trust-weight a curated decision above an incidental mention.
//
// This is the deterministic mining layer, which runs in the index pass with no
// LLM. The budget-gated LLM rationale pass (default off, never inside init) is a
// future enhancement that fires only on structured-signal-but-unresolved chunks.
func mineDocSignal(text string) string {
	switch {
	case text == "":
		return "lexical"
	case adrMarkerRe.MatchString(text):
		return "adr"
	case rfc2119Re.MatchString(text):
		return "rfc2119"
	case ticketRe.MatchString(text):
		return "ticket"
	default:
		return "lexical"
	}
}
