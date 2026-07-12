package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search"
	"github.com/zzet/gortex/internal/search/rerank"
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

// TestFacadeExploreDemotesRepeatedDataLeafNames reproduces the reported
// localization failure through the public facade: many unrelated declarations
// named `client` used to consume the whole result head, excluding the callable
// definition that explains the area. One best data-leaf match may remain, but
// repeated same-name leaves must not crowd out a differently-named code target.
func TestFacadeExploreDemotesRepeatedDataLeafNames(t *testing.T) {
	g := graph.New()
	bm := search.NewBM25()
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("pkg/service%d.go::client", i)
		n := &graph.Node{
			ID: id, Name: "client", Kind: graph.KindVariable,
			FilePath: fmt.Sprintf("pkg/service%d.go", i), Language: "go",
			Meta: map[string]any{"signature": "var client *Transport"},
		}
		g.AddNode(n)
		bm.Add(id, n.Name, n.FilePath, "trace client coordinated transport")
	}
	relevant := &graph.Node{
		ID: "pkg/coordinator.go::TransportCoordinator", Name: "TransportCoordinator",
		Kind: graph.KindFunction, FilePath: "pkg/coordinator.go", Language: "go",
		Meta: map[string]any{"signature": "func TransportCoordinator()"},
	}
	g.AddNode(relevant)
	// It matches the whole task, but its longer prose and non-literal symbol
	// name rank below the short exact-name `client` declarations. The concept
	// over-fetch window must retain it so name diversification can promote it.
	relevantText := strings.Repeat("architecture routing plumbing lifecycle ", 40) + "trace client coordinated transport coordinator"
	bm.Add(relevant.ID, relevant.Name, relevant.FilePath, relevantText)

	eng := query.NewEngine(g)
	eng.SetSearch(bm)
	srv := NewServer(eng, g, nil, nil, zap.NewNop(), nil)
	task := "trace how the client is coordinated"
	searchQuery := shapeExploreQuery(task)
	const maxSymbols = 6
	queryClass := rerank.ClassifyQuery(searchQuery)
	raw := eng.SearchSymbolsRanked(searchQuery, exploreCandidateFetchLimit(maxSymbols, queryClass), query.QueryOptions{}, srv.buildRerankContext(context.Background(), searchQuery))
	rawHead := make([]string, 0, 6)
	relevantRetrieved := false
	for _, candidate := range raw {
		if candidate != nil && candidate.Node != nil && candidate.Node.ID == relevant.ID {
			relevantRetrieved = true
		}
		if len(rawHead) == 6 {
			continue
		}
		if candidate == nil || candidate.Node == nil || !exploreLocalizableKind(candidate.Node.Kind) || !exploreCodeDefinitionKind(candidate.Node.Kind) {
			continue
		}
		rawHead = append(rawHead, candidate.Node.ID)
	}
	if len(rawHead) != 6 {
		t.Fatalf("fixture produced only %d raw localization candidates: %v", len(rawHead), rawHead)
	}
	if !relevantRetrieved {
		rawIDs := make([]string, 0, len(raw))
		for _, candidate := range raw {
			if candidate != nil && candidate.Node != nil {
				rawIDs = append(rawIDs, candidate.Node.ID)
			}
		}
		t.Fatalf("fixture's relevant callable fell outside the concept-query over-fetch window: query=%q raw=%v", searchQuery, rawIDs)
	}
	for _, id := range rawHead {
		if id == relevant.ID {
			t.Fatalf("fixture no longer reproduces crowd-out before explore diversification: %v", rawHead)
		}
	}
	req := mcpgo.CallToolRequest{}
	// Omit operation deliberately: the facade default must route to task.
	req.Params.Arguments = map[string]any{
		"task": task, "options": map[string]any{"max_symbols": maxSymbols},
	}
	result, err := srv.handleFacade(context.Background(), "explore", req)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.IsError || len(result.Content) == 0 {
		t.Fatalf("explore failed: %#v", result)
	}
	text, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("unexpected explore result content: %#v", result.Content[0])
	}
	out := text.Text
	if strings.Contains(out, "get_symbol_source") || strings.Contains(out, "batch_symbols") || strings.Contains(out, "search_text") || strings.Contains(out, "find_files") {
		t.Fatalf("explore emitted unavailable legacy follow-up guidance:\n%s", out)
	}
	relevantAt := strings.Index(out, "TransportCoordinator")
	if relevantAt < 0 {
		t.Fatalf("callable target was crowded out by repeated data leaves:\n%s", out)
	}
	firstClient := strings.Index(out, ". client  variable")
	if firstClient < 0 {
		t.Fatalf("fixture did not surface its best literal data-leaf match:\n%s", out)
	}
	secondClient := strings.Index(out[firstClient+1:], ". client  variable")
	if secondClient >= 0 && firstClient+1+secondClient < relevantAt {
		t.Fatalf("repeated generic data leaves still rank ahead of the callable target:\n%s", out)
	}
}

func TestDemoteRepeatedExploreDataNamesIsConceptOnlyAndStable(t *testing.T) {
	leaf1 := &rerank.Candidate{Node: &graph.Node{ID: "a", Name: "client", Kind: graph.KindVariable}}
	leaf2 := &rerank.Candidate{Node: &graph.Node{ID: "b", Name: "client", Kind: graph.KindField}}
	callable := &rerank.Candidate{Node: &graph.Node{ID: "c", Name: "ClientCoordinator", Kind: graph.KindFunction}}

	concept := demoteRepeatedExploreDataNames([]*rerank.Candidate{leaf1, leaf2, callable}, rerank.QueryClassConcept)
	if concept[0] != leaf1 || concept[1] != callable || concept[2] != leaf2 {
		t.Fatalf("concept diversification is not stable/bounded: %#v", concept)
	}
	symbol := demoteRepeatedExploreDataNames([]*rerank.Candidate{leaf1, leaf2, callable}, rerank.QueryClassSymbol)
	if symbol[0] != leaf1 || symbol[1] != leaf2 || symbol[2] != callable {
		t.Fatalf("identifier lookup order changed: %#v", symbol)
	}
	if got := exploreCandidateFetchLimit(6, rerank.QueryClassConcept); got != 48 {
		t.Fatalf("concept fetch limit=%d want 48", got)
	}
	if got := exploreCandidateFetchLimit(6, rerank.QueryClassSymbol); got != 24 {
		t.Fatalf("symbol fetch limit=%d want 24", got)
	}
}
