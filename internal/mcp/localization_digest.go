package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Terminal evidence retention.
//
// The localize handler builds a byte-budgeted evidence envelope once and
// used to throw it away after serialization: the per-session terminal state
// kept only the completion contract. A host that navigated past answer_ready
// then received bare refusals — each one a burned model turn with no way
// forward — and a session could exhaust its turn budget holding the correct
// answer it was never shown again. The digest is the retained, replayable
// core of that envelope plus a deterministic final response the host can
// emit verbatim.

// localizationDigestMaxBytes bounds the retained digest independently of the
// original envelope budget so session state stays small no matter how large
// the localize response was.
const localizationDigestMaxBytes = 4096

// localizationEvidenceDigest is the compact, session-retained projection of
// an answer envelope: enough to answer from, never the full source bodies.
type localizationEvidenceDigest struct {
	Files         []string                `json:"files,omitempty"`
	Symbols       []string                `json:"symbols,omitempty"`
	Evidence      []localizationDigestRow `json:"evidence,omitempty"`
	FinalResponse string                  `json:"final_response,omitempty"`
}

type localizationDigestRow struct {
	Rank      int    `json:"rank,omitempty"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Signature string `json:"signature,omitempty"`
}

// newLocalizationEvidenceDigest projects the serialized envelope into the
// retained digest. Evidence rows drop their Source bodies and are shed from
// the tail until the digest fits localizationDigestMaxBytes, mirroring the
// envelope's own shed-expansion-first budgeting; Files and Symbols are always
// retained (they are the answer's skeleton and already bounded upstream).
func newLocalizationEvidenceDigest(envelope localizationExploreEnvelope) *localizationEvidenceDigest {
	digest := &localizationEvidenceDigest{
		Files:   append([]string(nil), envelope.Files...),
		Symbols: append([]string(nil), envelope.Symbols...),
	}
	for _, row := range envelope.Evidence {
		digest.Evidence = append(digest.Evidence, localizationDigestRow{
			Rank:      row.Rank,
			ID:        row.ID,
			Name:      row.Name,
			File:      row.File,
			Line:      row.Line,
			Signature: row.Signature,
		})
	}
	digest.FinalResponse = buildLocalizationFinalResponse(digest)
	for len(digest.Evidence) > 0 {
		encoded, err := json.Marshal(digest)
		if err == nil && len(encoded) <= localizationDigestMaxBytes {
			return digest
		}
		digest.Evidence = digest.Evidence[:len(digest.Evidence)-1]
		digest.FinalResponse = buildLocalizationFinalResponse(digest)
	}
	return digest
}

// buildLocalizationFinalResponse renders the deterministic, ready-to-emit
// answer text: the FILES / SYMBOLS / EVIDENCE sections a localization-only
// caller is expected to produce. A host may return it verbatim when it does
// not want to spend an inference turn on synthesis.
func buildLocalizationFinalResponse(digest *localizationEvidenceDigest) string {
	var b strings.Builder
	b.WriteString("FILES:\n")
	for _, file := range digest.Files {
		fmt.Fprintf(&b, "- %s\n", file)
	}
	b.WriteString("SYMBOLS:\n")
	for _, symbol := range digest.Symbols {
		fmt.Fprintf(&b, "- %s\n", symbol)
	}
	if len(digest.Evidence) > 0 {
		b.WriteString("EVIDENCE:\n")
		for _, row := range digest.Evidence {
			fmt.Fprintf(&b, "- %s:%d — %s", row.File, row.Line, row.Name)
			if row.Signature != "" {
				fmt.Fprintf(&b, " (%s)", row.Signature)
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// localizationReplayDirective is the actionable half of a post-terminal
// replay: the same "answer NOW" instruction the momentum escalation uses,
// phrased for the terminal case.
const localizationReplayDirective = "You already hold the localization answer — respond NOW using the evidence " +
	"below: name the files and symbols, citing the locations already returned. Further Gortex navigation " +
	"returns this same evidence; a confident answer beats an exhausted turn budget with no answer."

// answerReadyDirective returns the answer-now note a post-terminal READ
// result carries: reads execute after answer_ready (starving them produced
// empty finals), but every such result reminds the host it already holds
// the answer and repeats the deterministic final response.
func (s *localizationTerminalState) answerReadyDirective() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != localizationStateAnswerReady || s.digest == nil {
		return "", false
	}
	return localizationReplayDirective + "\n\n" + s.digest.FinalResponse, true
}

// localizationEvidenceReplayResult is the successful, idempotent response to
// any post-terminal navigation call. It is deliberately NOT an error: error
// results push non-adapted hosts into error-recovery loops and count as
// failed calls, while a success result is actionable on the very next model
// turn. Every subsequent call returns the byte-identical payload — the
// refused call's facade/operation is deliberately absent, because the answer
// does not depend on which navigation was attempted.
func localizationEvidenceReplayResult(completion localizationCompletion, digest *localizationEvidenceDigest) *mcpgo.CallToolResult {
	payload := map[string]any{
		"completion":      completion,
		"replay":          true,
		"directive":       localizationReplayDirective,
		"evidence_digest": digest,
		"final_response":  digest.FinalResponse,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return mcpgo.NewToolResultText(localizationReplayDirective + "\n\n" + digest.FinalResponse)
	}
	result := mcpgo.NewToolResultText(localizationReplayDirective + "\n\n" + digest.FinalResponse + "\n\n" + string(body))
	result.StructuredContent = json.RawMessage(body)
	return result
}
