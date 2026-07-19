package mcp

import (
	"encoding/json"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// Terminal evidence retention.
//
// The localize handler builds a byte-budgeted evidence envelope once and
// retains a compact projection for host-side fallback and diagnostics. A
// post-terminal tool call does not replay that projection: the original
// localization response already supplied it, and repeating it consumed turns
// and tokens while encouraging further navigation.

const (
	// localizationDigestMaxBytes bounds retained session state independently of
	// the original envelope budget.
	localizationDigestMaxBytes = 4096
	// localizationReplayEvidenceLimit prevents a broad localization envelope
	// from becoming an exhaustive, implicitly endorsed answer during replay.
	// Five keeps the promoted structural/literal candidates reserved by the
	// envelope builder while bounding repeat-turn cost.
	localizationReplayEvidenceLimit = 5
	// This canonical envelope is deliberately carried in MCP _meta. Adapting
	// hosts may render its ordered evidence deterministically without exposing
	// retained rows to model-visible text or structuredContent.
	localizationHostMetaKey = "gortex/localization"
)

// localizationEvidenceDigest is the compact, session-retained projection of
// an answer envelope: ranked candidate evidence without source bodies.
type localizationEvidenceDigest struct {
	Files    []string                `json:"files,omitempty"`
	Symbols  []string                `json:"symbols,omitempty"`
	Evidence []localizationDigestRow `json:"evidence,omitempty"`
}

type localizationDigestRow struct {
	Rank      int      `json:"rank,omitempty"`
	ID        string   `json:"id,omitempty"`
	Name      string   `json:"name,omitempty"`
	QualName  string   `json:"qual_name,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	File      string   `json:"file,omitempty"`
	Line      int      `json:"line,omitempty"`
	Signature string   `json:"signature,omitempty"`
	Callers   []string `json:"callers,omitempty"`
	Callees   []string `json:"callees,omitempty"`
}

// newLocalizationEvidenceDigest retains only concrete ranked evidence rows.
// Files and Symbols are rebuilt from those rows, so an item that was shed by
// the replay limit or byte budget cannot survive as an unsupported answer
// candidate. The upstream ordering already reserves the strongest direct,
// exact, literal, and promoted structural targets before lower-ranked fan-out.
func newLocalizationEvidenceDigest(envelope localizationExploreEnvelope) *localizationEvidenceDigest {
	digest := &localizationEvidenceDigest{}
	seen := make(map[string]struct{}, localizationReplayEvidenceLimit)
	for _, row := range envelope.Evidence {
		if len(digest.Evidence) >= localizationReplayEvidenceLimit {
			break
		}
		if row.ID == "" || row.File == "" {
			continue
		}
		if _, exists := seen[row.ID]; exists {
			continue
		}
		seen[row.ID] = struct{}{}
		digest.Evidence = append(digest.Evidence, localizationDigestRow{
			Rank:      row.Rank,
			ID:        row.ID,
			Name:      row.Name,
			QualName:  row.QualName,
			Kind:      row.Kind,
			File:      row.File,
			Line:      row.Line,
			Signature: row.Signature,
			Callers:   append([]string(nil), row.Callers...),
			Callees:   append([]string(nil), row.Callees...),
		})
	}
	for {
		rebuildLocalizationDigestSkeleton(digest)
		encoded, err := json.Marshal(digest)
		if err == nil && len(encoded) <= localizationDigestMaxBytes {
			return digest
		}
		if len(digest.Evidence) == 0 {
			return digest
		}
		last := len(digest.Evidence) - 1
		if shedLocalizationDigestRowOptionalFields(&digest.Evidence[last]) {
			continue
		}
		if last == 0 {
			// ID and file are the irreducible replay contract. They are bounded by
			// filesystem and symbol extraction limits in production, so retain the
			// mandatory row rather than returning an empty terminal replay.
			return digest
		}
		digest.Evidence = digest.Evidence[:last]
	}
}

func shedLocalizationDigestRowOptionalFields(row *localizationDigestRow) bool {
	if row == nil {
		return false
	}
	if len(row.Callers) > 0 || len(row.Callees) > 0 {
		row.Callers = nil
		row.Callees = nil
		return true
	}
	if row.Signature != "" {
		row.Signature = ""
		return true
	}
	if row.QualName != "" {
		row.QualName = ""
		return true
	}
	if row.Name != "" || row.Kind != "" {
		row.Name = ""
		row.Kind = ""
		return true
	}
	return false
}

func rebuildLocalizationDigestSkeleton(digest *localizationEvidenceDigest) {
	digest.Files = digest.Files[:0]
	digest.Symbols = digest.Symbols[:0]
	seenFiles := make(map[string]struct{}, len(digest.Evidence))
	seenSymbols := make(map[string]struct{}, len(digest.Evidence))
	for _, row := range digest.Evidence {
		if _, exists := seenFiles[row.File]; !exists {
			seenFiles[row.File] = struct{}{}
			digest.Files = append(digest.Files, row.File)
		}
		if _, exists := seenSymbols[row.ID]; !exists {
			seenSymbols[row.ID] = struct{}{}
			digest.Symbols = append(digest.Symbols, row.ID)
		}
	}
}

const localizationAnswerReadyNotice = "Localization is complete. Do not call another tool. " +
	"Answer the user now in your own words using the evidence already returned."

// localizationHostEnvelope stores each retained row exactly once. Hosts render
// the ordered rows with fallback_format; no prewritten answer or duplicate row
// string crosses the wire.
type localizationHostEnvelope struct {
	Version        int                         `json:"version"`
	FallbackFormat string                      `json:"fallback_format"`
	Evidence       *localizationEvidenceDigest `json:"evidence"`
}

func attachLocalizationHostEnvelope(result *mcpgo.CallToolResult, digest *localizationEvidenceDigest) *mcpgo.CallToolResult {
	if result == nil || digest == nil {
		return result
	}
	if result.Meta == nil {
		result.Meta = &mcpgo.Meta{}
	}
	if result.Meta.AdditionalFields == nil {
		result.Meta.AdditionalFields = make(map[string]any)
	}
	result.Meta.AdditionalFields[localizationHostMetaKey] = localizationHostEnvelope{
		Version:        1,
		FallbackFormat: "{file}:{line} — {id} ({signature})",
		Evidence:       digest,
	}
	return result
}

// localizationAnswerReadyResult is deliberately successful, answer-neutral,
// and constant-size. An error invites tool-recovery loops; replaying retained
// evidence invites more analysis. The completion contract is sufficient for
// hosts, while the imperative text reliably steers non-adapted models to a
// final answer without supplying a prewritten one.
func localizationAnswerReadyResult(completion localizationCompletion) *mcpgo.CallToolResult {
	result := mcpgo.NewToolResultText(localizationAnswerReadyNotice)
	result.StructuredContent = map[string]any{
		"completion": completion,
		"terminal":   true,
	}
	return attachLocalizationHostEnvelope(result, completion.digest)
}
