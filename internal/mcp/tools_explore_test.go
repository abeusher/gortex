package mcp

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestExploreLocalizableKind(t *testing.T) {
	// Real edit targets are localizable; structural noise is not.
	localizable := []graph.NodeKind{
		graph.KindFunction, graph.KindMethod, graph.KindType,
		graph.KindInterface, graph.KindField, graph.KindConstant,
		graph.KindVariable,
	}
	for _, k := range localizable {
		if !exploreLocalizableKind(k) {
			t.Errorf("kind %q should be localizable", k)
		}
	}
	noise := []graph.NodeKind{
		graph.KindParam, graph.KindLocal, graph.KindClosure,
		graph.KindGenericParam, graph.KindImport, graph.KindFile,
	}
	for _, k := range noise {
		if exploreLocalizableKind(k) {
			t.Errorf("kind %q should be filtered out", k)
		}
	}
}

func exploreTestTargets() []exploreTarget {
	fn := &graph.Node{Name: "DoWithRetry", Kind: graph.KindFunction,
		FilePath: "retry.go", StartLine: 11, EndLine: 20, Language: "go"}
	helper := &graph.Node{Name: "Backoff", Kind: graph.KindFunction,
		FilePath: "retry.go", StartLine: 6, EndLine: 8, Language: "go"}
	caller := &graph.Node{Name: "Fetch", Kind: graph.KindFunction,
		FilePath: "client.go", StartLine: 4, EndLine: 6, Language: "go"}
	return []exploreTarget{
		{node: fn, score: 0.9, callers: []*graph.Node{caller}, callees: []*graph.Node{helper},
			source: "func DoWithRetry(max int) error {\n\treturn nil\n}"},
		{node: helper, score: 0.5, callers: []*graph.Node{fn},
			source: "func Backoff(n int) int {\n\treturn n\n}"},
	}
}

func TestRenderExploreShape(t *testing.T) {
	out := (&Server{}).renderExplore("the retry backoff never fires on 429", exploreTestTargets(), 9000)

	// Ranked targets, with citeable path:line locations.
	for _, want := range []string{
		"EXPLORE — the retry backoff never fires on 429",
		"## Likely targets",
		"1. DoWithRetry  function  ·  retry.go:11-20",
		"2. Backoff  function  ·  retry.go:6-8",
		"^ callers: Fetch (client.go:4-6)",
		"v calls:   Backoff (retry.go:6-8)",
		"func DoWithRetry(max int) error", // full body for hot target
		"## Files to change",
		"- retry.go  ·  Backoff, DoWithRetry",
		"— Completeness: 2 candidate symbol(s) across 1 file(s)",
		"FILES / SYMBOLS / EVIDENCE",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestRenderExploreBudgetTruncation(t *testing.T) {
	// A budget below the body cost forces demotion to signatures, but every
	// candidate's LOCATION must still be listed (file-hit/symbol-hit never
	// depend on the budget) and the truncation must be reported honestly.
	out := (&Server{}).renderExplore("task", exploreTestTargets(), exploreMinBudgetTokens)
	for _, want := range []string{
		"1. DoWithRetry  function  ·  retry.go:11-20",
		"2. Backoff  function  ·  retry.go:6-8",
		"— Completeness:",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("truncated render missing %q", want)
		}
	}
}

func TestExploreHelpers(t *testing.T) {
	if got := clampInt(5, 1, 3); got != 3 {
		t.Errorf("clampInt hi: got %d", got)
	}
	if got := clampInt(0, 2, 10); got != 2 {
		t.Errorf("clampInt lo: got %d", got)
	}
	if got := truncateOneLine("a\n b\tc", 100); got != "a b c" {
		t.Errorf("truncateOneLine collapse: %q", got)
	}
	if got := truncateOneLine(strings.Repeat("x", 10), 4); got != "xxxx…" {
		t.Errorf("truncateOneLine cap: %q", got)
	}
	if got := firstLines("1\n2\n3\n4", 2); got != "1\n2" {
		t.Errorf("firstLines: %q", got)
	}
	if got := dedupStrings([]string{"b", "a", "b"}); strings.Join(got, ",") != "a,b" {
		t.Errorf("dedupStrings: %v", got)
	}
}
