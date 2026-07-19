package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func testEvidenceDigest() *localizationEvidenceDigest {
	return newLocalizationEvidenceDigest(localizationExploreEnvelope{
		Files:   []string{"repo/storage/disk.go", "repo/storage/cloud.go"},
		Symbols: []string{"repo/storage/disk.go::DiskStorage.Load", "repo/storage/cloud.go::CloudStorage.Load"},
		Evidence: []localizationEvidence{
			{Rank: 1, ID: "repo/storage/disk.go::DiskStorage.Load", Name: "Load", File: "repo/storage/disk.go", Line: 42, Signature: "func (s *DiskStorage) Load(key string) ([]byte, error)"},
			{Rank: 2, ID: "repo/storage/cloud.go::CloudStorage.Load", Name: "Load", File: "repo/storage/cloud.go", Line: 17},
		},
	})
}

// The core terminality guarantee: a navigation call after answer_ready gets
// the retained answer back as a SUCCESSFUL, idempotent replay — never a bare
// refusal that burns a model turn with no way forward.
func TestPostTerminalNavigationReplaysEvidenceAsSuccess(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")

	first, reserved := state.authorize("search", "symbols", nil)
	if reserved {
		t.Fatal("post-terminal navigation must not reserve a read")
	}
	if first == nil || first.IsError {
		t.Fatalf("post-terminal navigation = %+v, want successful replay", first)
	}
	text, ok := singleTextContent(first)
	if !ok {
		t.Fatal("replay result must carry text content")
	}
	if !strings.Contains(text, localizationReplayDirective) {
		t.Fatal("replay must carry the answer-now directive")
	}
	if !strings.Contains(text, "repo/storage/disk.go") || !strings.Contains(text, "FILES:") {
		t.Fatal("replay must carry the deterministic final response")
	}

	second, _ := state.authorize("relations", "usages", nil)
	firstText, _ := singleTextContent(first)
	secondText, _ := singleTextContent(second)
	if firstText != secondText {
		t.Fatal("post-terminal replay must be idempotent across calls and facades")
	}
}

// A repeat localize against a terminal contract is the non-adapted host's
// signature move; it must receive the evidence again, not error recovery.
func TestRepeatLocalizeAgainstTerminalContractReplaysEvidence(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")

	token, blocked := state.beginLocalize("find the storage load implementations again", false)
	if token != 0 {
		t.Fatal("repeat localize must not reserve the handler slot")
	}
	if blocked == nil || blocked.IsError {
		t.Fatalf("repeat localize = %+v, want successful replay", blocked)
	}
	text, _ := singleTextContent(blocked)
	if !strings.Contains(text, "final_response") && !strings.Contains(text, "FILES:") {
		t.Fatal("repeat localize replay must carry the final response")
	}
}

// The evidence is stashed when the refinement contract is armed, so the
// promotion to answer_ready after the one permitted read replays it.
func TestRefinementPromotionRetainsDigestForReplay(t *testing.T) {
	state := &localizationTerminalState{}
	candidate := "repo/storage/disk.go::DiskStorage.Load"
	state.armRefinementForTask("find the storage load implementations", candidate, []string{candidate}, testEvidenceDigest())

	args := map[string]any{"target": map[string]any{"symbol": candidate}}
	if blocked, reserved := state.authorize("read", "source", args); blocked != nil || !reserved {
		t.Fatalf("permitted refinement read = (%v, %v), want reservation", blocked, reserved)
	}
	state.finishReservedRead(true)

	replay, reserved := state.authorize("search", "symbols", nil)
	if reserved || replay == nil || replay.IsError {
		t.Fatalf("post-promotion navigation = (%+v, %v), want successful replay", replay, reserved)
	}
	text, _ := singleTextContent(replay)
	if !strings.Contains(text, "repo/storage/cloud.go") {
		t.Fatal("promotion must replay the digest stashed at refinement arm time")
	}
}

// Digest lifecycle: an inactive commit clears it, and answer_ready without a
// digest (legacy contracts) keeps the original error semantics.
func TestDigestLifecycleAndLegacyFallback(t *testing.T) {
	state := &localizationTerminalState{}
	completion := newLocalizationCompletion(true, "")
	completion.digest = testEvidenceDigest()
	state.armForTask(completion, "find the storage load implementations")
	state.keepOpenForTask("new unrelated work")
	if blocked, _ := state.authorize("search", "symbols", nil); blocked != nil {
		t.Fatalf("inactive state must not block, got %+v", blocked)
	}
	if state.digest != nil {
		t.Fatal("keepOpenForTask must clear the retained digest")
	}

	legacy := &localizationTerminalState{}
	legacy.armForTask(newLocalizationCompletion(true, ""), "task without digest")
	blocked, _ := legacy.authorize("search", "symbols", nil)
	if blocked == nil || !blocked.IsError {
		t.Fatal("answer_ready without a digest must keep the error contract")
	}
}

// The retained digest is bounded regardless of envelope size: evidence rows
// shed from the tail, the files/symbols skeleton always survives.
func TestDigestByteCapShedsEvidenceTail(t *testing.T) {
	envelope := localizationExploreEnvelope{
		Files:   []string{"repo/big/file.go"},
		Symbols: []string{"repo/big/file.go::Sym"},
	}
	for i := 0; i < 400; i++ {
		envelope.Evidence = append(envelope.Evidence, localizationEvidence{
			Rank: i + 1,
			ID:   fmt.Sprintf("repo/big/file.go::Sym%03d", i),
			Name: strings.Repeat("n", 40),
			File: "repo/big/file.go",
			Line: i,
		})
	}
	digest := newLocalizationEvidenceDigest(envelope)
	encoded, err := json.Marshal(digest)
	if err != nil {
		t.Fatalf("marshal digest: %v", err)
	}
	if len(encoded) > localizationDigestMaxBytes {
		t.Fatalf("digest = %d bytes, want <= %d", len(encoded), localizationDigestMaxBytes)
	}
	if len(digest.Files) != 1 || len(digest.Symbols) != 1 {
		t.Fatal("files/symbols skeleton must survive the cap")
	}
	if len(digest.Evidence) == 0 {
		t.Fatal("cap should shed the tail, not everything")
	}
	if !strings.Contains(digest.FinalResponse, "FILES:") {
		t.Fatal("final response must be rebuilt after shedding")
	}
}
