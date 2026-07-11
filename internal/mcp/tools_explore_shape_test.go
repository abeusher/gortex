package mcp

import (
	"strings"
	"testing"
)

// TestShapeExploreQuery_DistilsReport locks the report-distillation behavior:
// a pasted issue (a title, then a body of repro commands, template prompts and
// an environment table) is reduced to its retrieval signal — the lead weighted,
// the boilerplate dropped — so the defect description is no longer out-weighed
// by command-line flags and log lines. The fixture is synthetic; nothing here
// is drawn from any real project or task corpus.
func TestShapeExploreQuery_DistilsReport(t *testing.T) {
	issue := "Retry backoff never fires on throttled responses\n\n" +
		"Please tick this box to confirm you searched existing issues.\n\n" +
		"What version are you running?\n3.2.1\n" +
		"How did you install it?\npackage manager\n\n" +
		"Describe the bug.\n" +
		"The exponential backoff scheduler skips its delay when the server returns a throttle status, " +
		"so the client hammers the endpoint instead of waiting.\n\n" +
		"```\n$ mytool run --retries 5 --backoff exponential\nWARN: retry storm detected\n```\n"
	got := shapeExploreQuery(issue)

	if strings.Contains(got, "```") || strings.Contains(got, "mytool run") || strings.Contains(got, "--backoff") {
		t.Errorf("fenced repro command leaked into query: %q", got)
	}
	if strings.Contains(got, "What version are you running") {
		t.Errorf("issue-template prompt (ends in ?) leaked: %q", got)
	}
	if strings.Contains(got, "3.2.1") || strings.Contains(got, "package manager") {
		t.Errorf("short env-answer line leaked: %q", got)
	}
	// The lead is present and weighted (appears at least twice).
	if strings.Count(got, "Retry backoff never fires on throttled responses") < 2 {
		t.Errorf("lead not present/weighted: %q", got)
	}
	// The defect description survives.
	if !strings.Contains(got, "exponential backoff scheduler skips its delay") {
		t.Errorf("defect description dropped: %q", got)
	}
}

// TestShapeExploreQuery_PassThrough locks that a short focused query — the
// already-good case — is returned byte-for-byte unchanged.
func TestShapeExploreQuery_PassThrough(t *testing.T) {
	for _, q := range []string{
		"the retry backoff never triggers on a throttled response",
		"where is the websocket upgrade handled",
		"", // empty stays empty
		"a single overly long line with no newline at all that is definitely more than three hundred characters long so it crosses the size threshold but has no lead/body structure to distil because there is exactly one line and nothing resembling a report body here at all so it must pass through",
	} {
		if got := shapeExploreQuery(q); got != q {
			t.Errorf("focused query altered:\n in:  %q\n out: %q", q, got)
		}
	}
}

// TestShapeReportBody_Bounds locks the rune bound so a runaway single
// paragraph cannot re-drown the weighted lead.
func TestShapeReportBody_Bounds(t *testing.T) {
	long := strings.Repeat("alpha beta gamma delta ", 200) // one long line, >> bound
	got := shapeReportBody(long)
	if n := len([]rune(got)); n > shapeBodyMaxRunes {
		t.Fatalf("body bound not applied: %d > %d", n, shapeBodyMaxRunes)
	}
}
