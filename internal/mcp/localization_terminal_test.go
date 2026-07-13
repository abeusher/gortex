package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/graph"
)

func TestHandleFacadeRejectsLocalizationBypassesWithoutClearingState(t *testing.T) {
	registry := newFacadeRegistry()
	calls := 0
	registry.capture(mcpgo.NewTool("explore"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		calls++
		return mcpgo.NewToolResultText("unexpected"), nil
	})
	server := &Server{facades: registry, localization: &localizationTerminalState{}}
	ctx := WithSessionID(context.Background(), "localize-validation")
	terminal := server.localizationFor(ctx)
	terminal.armForTask(newLocalizationCompletion(true, ""), "Locate Foo")

	requests := []struct {
		name string
		args map[string]any
	}{
		{name: "task top-level override", args: map[string]any{"operation": "task", "task": "Locate Bar", "localize": true}},
		{name: "task nested override", args: map[string]any{"operation": "task", "task": "Locate Bar", "options": map[string]any{"localize": true}}},
		{name: "empty localize", args: map[string]any{"operation": "localize", "task": ""}},
		{name: "nested localize task", args: map[string]any{"operation": "localize", "options": map[string]any{"task": "Locate Bar"}}},
		{name: "malformed different localize", args: map[string]any{"operation": "localize", "task": "Locate Bar", "options": "bad"}},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			req := mcpgo.CallToolRequest{}
			req.Params.Name = "explore"
			req.Params.Arguments = request.args
			result, err := server.handleFacade(ctx, "explore", req)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("handleFacade() = (%v, %v), want invalid argument result", result, err)
			}
			if blocked := terminal.block("search", "symbols", nil); blocked == nil {
				t.Fatal("invalid localization request cleared terminal state")
			}
		})
	}
	if calls != 0 {
		t.Fatalf("legacy explore calls = %d, want 0", calls)
	}
}

func TestCompleteEmptyLocalizationReplacesPriorContract(t *testing.T) {
	terminal := &localizationTerminalState{}
	server := &Server{localization: terminal}
	terminal.armForTask(newLocalizationCompletion(true, ""), "Locate A")

	result := server.completeEmptyLocalization(context.Background(), "Locate B", exploreDefaultBudgetTokens)
	if result == nil || result.IsError || len(result.Content) != 1 {
		t.Fatalf("empty localization result = %v, want one successful compact envelope", result)
	}
	content, ok := result.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("empty localization content type = %T, want TextContent", result.Content[0])
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(content.Text), &envelope); err != nil {
		t.Fatalf("decode empty localization envelope: %v", err)
	}
	for _, field := range []string{"files", "symbols", "evidence"} {
		items, ok := envelope[field].([]any)
		if !ok || len(items) != 0 {
			t.Fatalf("%s = %#v, want empty array", field, envelope[field])
		}
	}
	if blocked := terminal.beginLocalize("Locate B"); blocked == nil {
		t.Fatal("empty localization did not arm the new task contract")
	}
	if blocked := terminal.beginLocalize("Locate A"); blocked != nil {
		t.Fatalf("prior task still owns terminal state: %v", blocked)
	}
}

func TestLocalizationFacadeIsExplicit(t *testing.T) {
	registry := newFacadeRegistry()
	localize, ok := registry.operation("explore", "localize")
	if !ok {
		t.Fatal("explore(localize) is not registered")
	}
	if localize.Legacy != "explore" || localize.Fixed["localize"] != true {
		t.Fatalf("unexpected localize mapping: %#v", localize)
	}
	task, ok := registry.operation("explore", "task")
	if !ok {
		t.Fatal("explore(task) is not registered")
	}
	if _, terminal := task.Fixed["localize"]; terminal {
		t.Fatalf("ordinary explore(task) must remain non-terminal: %#v", task.Fixed)
	}
}

func TestLocalizationCompletionEnvelope(t *testing.T) {
	completion := newLocalizationCompletion(true, "")
	result := newLocalizationExploreResult(completion, []exploreTarget{{node: &graph.Node{
		ID: "repo/pkg/file.go::Run", Name: "Run", Kind: graph.KindFunction,
		FilePath: "pkg/file.go", StartLine: 12,
		QualName: "resolver.Run",
		Meta: map[string]any{
			"signature": "func Run()", "search_qual_name": "pkg.Run",
			"search_signature": "func pkg.Run()",
		},
	}, source: "func Run() {}"}}, 1600)
	text, ok := singleTextContent(result)
	if !ok {
		t.Fatalf("expected one text result: %#v", result)
	}
	var envelope localizationExploreEnvelope
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decode completion envelope: %v\n%s", err, text)
	}
	if envelope.Completion.State != localizationStateAnswerReady || envelope.Completion.RequiredAction != "respond" || envelope.Completion.AllowedToolCalls != 0 {
		t.Fatalf("unexpected completion: %#v", envelope.Completion)
	}
	if len(envelope.Files) != 1 || len(envelope.Symbols) != 1 || envelope.Symbols[0] != "repo/pkg/file.go::Run" || len(envelope.Evidence) != 1 {
		t.Fatalf("missing localization payload: %#v", envelope)
	}
	if envelope.Evidence[0].QualName != "pkg.Run" || envelope.Evidence[0].Signature != "func pkg.Run()" {
		t.Fatalf("normalized retrieval metadata not used: %#v", envelope.Evidence[0])
	}
	if strings.Contains(text, "RANKED LOCALIZATION") || strings.Contains(text, "## Likely targets") || len(text) > 2000 {
		t.Fatalf("localize envelope duplicated the legacy rendering or exceeded its compact budget (%d bytes): %s", len(text), text)
	}
}

func TestLocalizationTerminalStateBlocksOnlyNavigation(t *testing.T) {
	state := newLocalizationTerminalState()
	state.arm(newLocalizationCompletion(true, ""))
	for _, facade := range []string{"explore", "search", "read", "relations", "trace"} {
		blocked := state.block(facade, "anything", nil)
		if blocked == nil || !blocked.IsError {
			t.Fatalf("%s should be terminally blocked", facade)
		}
		text, _ := singleTextContent(blocked)
		if !strings.Contains(text, string(ErrCodeLocalizationComplete)) {
			t.Fatalf("%s returned the wrong error: %s", facade, text)
		}
	}
	for _, facade := range []string{"change", "edit", "refactor", "analyze", "workspace", "recall", "remember"} {
		if blocked := state.block(facade, "anything", nil); blocked != nil {
			t.Fatalf("%s must remain available after localization: %#v", facade, blocked)
		}
	}
}

func TestLocalizationNeedsExactlyOneRead(t *testing.T) {
	state := newLocalizationTerminalState()
	state.arm(newLocalizationCompletion(false, "repo/pkg/file.go::Run"))
	wrong := map[string]any{"target": map[string]any{"symbol": "repo/pkg/file.go::Other"}}
	if state.block("read", "source", wrong) == nil {
		t.Fatal("a different symbol must not consume the exact-read allowance")
	}
	exact := map[string]any{"target": map[string]any{"symbol": "repo/pkg/file.go::Run"}}
	if blocked := state.block("read", "source", exact); blocked != nil {
		t.Fatalf("the named exact read should be allowed: %#v", blocked)
	}
	if state.block("read", "source", exact) == nil {
		t.Fatal("the exact-read allowance must be consumed once")
	}
}

func TestLocalizationTerminalStateIsPerSession(t *testing.T) {
	server := &Server{
		localization: newLocalizationTerminalState(),
		sessions:     newSessionMap(),
	}
	ctxA := WithSessionID(context.Background(), "a")
	ctxB := WithSessionID(context.Background(), "b")
	server.localizationFor(ctxA).arm(newLocalizationCompletion(true, ""))
	if server.localizationFor(ctxA).block("search", "symbols", nil) == nil {
		t.Fatal("armed session should be blocked")
	}
	if blocked := server.localizationFor(ctxB).block("search", "symbols", nil); blocked != nil {
		t.Fatalf("separate session inherited terminality: %#v", blocked)
	}
	if blocked := server.localizationFor(context.Background()).block("search", "symbols", nil); blocked != nil {
		t.Fatalf("embedded default inherited daemon session state: %#v", blocked)
	}
}

func TestHandleFacadeTaskStartsFreshNonTerminalFlow(t *testing.T) {
	registry := newFacadeRegistry()
	called := false
	registry.capture(mcpgo.NewTool("explore"), func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		called = true
		if req.GetBool("localize", false) {
			t.Fatal("explore(task) must not inherit the localize fixed argument")
		}
		return mcpgo.NewToolResultText("ordinary diagnostic localization"), nil
	})
	server := &Server{
		facades:      registry,
		localization: newLocalizationTerminalState(),
		sessions:     newSessionMap(),
	}
	ctx := WithSessionID(context.Background(), "diagnosis")
	server.localizationFor(ctx).arm(newLocalizationCompletion(true, ""))
	req := mcpgo.CallToolRequest{}
	req.Params.Name = "explore"
	req.Params.Arguments = map[string]any{"operation": "task", "task": "diagnose the failure"}
	result, err := server.handleFacade(ctx, "explore", req)
	if err != nil || result == nil || result.IsError || !called {
		t.Fatalf("ordinary task flow did not dispatch: result=%#v err=%v called=%v", result, err, called)
	}
	if blocked := server.localizationFor(ctx).block("search", "symbols", nil); blocked != nil {
		t.Fatalf("explore(task) left terminal state armed: %#v", blocked)
	}
}

func TestHandleFacadeFailedTaskDoesNotClearTerminalState(t *testing.T) {
	registry := newFacadeRegistry()
	registry.capture(mcpgo.NewTool("explore"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultError("localization failed"), nil
	})
	server := &Server{
		facades:      registry,
		localization: newLocalizationTerminalState(),
		sessions:     newSessionMap(),
	}
	ctx := WithSessionID(context.Background(), "failed-diagnosis")
	server.localizationFor(ctx).arm(newLocalizationCompletion(true, ""))
	req := mcpgo.CallToolRequest{}
	req.Params.Name = "explore"
	req.Params.Arguments = map[string]any{"operation": "task", "task": "diagnose the failure"}
	result, err := server.handleFacade(ctx, "explore", req)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected failed task result: result=%#v err=%v", result, err)
	}
	if blocked := server.localizationFor(ctx).block("search", "symbols", nil); blocked == nil {
		t.Fatal("a failed task call must not clear an existing terminal state")
	}
}

func TestHandleFacadeExactReadCommitsOnlyOnSuccess(t *testing.T) {
	registry := newFacadeRegistry()
	calls := 0
	registry.capture(mcpgo.NewTool("get_symbol_source", mcpgo.WithString("id", mcpgo.Required())), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		calls++
		if calls == 1 {
			return mcpgo.NewToolResultError("transient read failure"), nil
		}
		return mcpgo.NewToolResultText("func Run() {}"), nil
	})
	server := &Server{facades: registry, localization: newLocalizationTerminalState(), sessions: newSessionMap()}
	ctx := WithSessionID(context.Background(), "exact-read")
	server.localizationFor(ctx).armForTask(newLocalizationCompletion(false, "repo/pkg/file.go::Run"), "locate Run")
	req := mcpgo.CallToolRequest{}
	req.Params.Name = "read"
	req.Params.Arguments = map[string]any{
		"operation": "source",
		"target":    map[string]any{"symbol": "repo/pkg/file.go::Run"},
	}
	first, err := server.handleFacade(ctx, "read", req)
	if err != nil || first == nil || !first.IsError {
		t.Fatalf("expected transient read failure: result=%#v err=%v", first, err)
	}
	second, err := server.handleFacade(ctx, "read", req)
	if err != nil || second == nil || second.IsError {
		t.Fatalf("retry should retain and consume the allowance on success: result=%#v err=%v", second, err)
	}
	third, err := server.handleFacade(ctx, "read", req)
	if err != nil || third == nil || !third.IsError || calls != 2 {
		t.Fatalf("successful exact read must make later navigation terminal: result=%#v err=%v calls=%d", third, err, calls)
	}
}

func TestHandleFacadeFailedDifferentLocalizePreservesTerminalState(t *testing.T) {
	registry := newFacadeRegistry()
	registry.capture(mcpgo.NewTool("explore"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return mcpgo.NewToolResultError("localization failed"), nil
	})
	server := &Server{facades: registry, localization: &localizationTerminalState{}}
	ctx := WithSessionID(context.Background(), "failed-different-localize")
	terminal := server.localizationFor(ctx)
	terminal.armForTask(newLocalizationCompletion(true, ""), "Locate Foo")

	req := mcpgo.CallToolRequest{}
	req.Params.Name = "explore"
	req.Params.Arguments = map[string]any{"operation": "localize", "task": "Locate Bar"}
	result, err := server.handleFacade(ctx, "explore", req)
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("failed localize = (%v, %v), want tool error", result, err)
	}
	if blocked := terminal.block("search", "symbols", nil); blocked == nil {
		t.Fatal("failed different localize cleared terminal state")
	}
}

func TestHandleFacadeExactReadPanicRestoresReservation(t *testing.T) {
	registry := newFacadeRegistry()
	calls := 0
	registry.capture(mcpgo.NewTool("get_symbol_source"), func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		calls++
		if calls == 1 {
			panic("legacy source panic")
		}
		return mcpgo.NewToolResultText("source"), nil
	})
	server := &Server{facades: registry, localization: &localizationTerminalState{}}
	ctx := WithSessionID(context.Background(), "exact-read-panic")
	terminal := server.localizationFor(ctx)
	const symbol = "repo/internal/file.go::Target"
	terminal.armForTask(newLocalizationCompletion(false, symbol), "Locate Target")

	args := map[string]any{
		"operation": "source",
		"target":    map[string]any{"symbol": symbol},
	}
	req := mcpgo.CallToolRequest{}
	req.Params.Name = "read"
	req.Params.Arguments = args
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = server.handleFacade(ctx, "read", req)
	}()
	if recovered == nil {
		t.Fatal("handleFacade() did not propagate legacy panic")
	}
	result, err := server.handleFacade(ctx, "read", req)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("exact read retry = (%v, %v), want success", result, err)
	}
	third, err := server.handleFacade(ctx, "read", req)
	if err != nil || third == nil || !third.IsError {
		t.Fatalf("third exact read = (%v, %v), want terminal block", third, err)
	}
	if calls != 2 {
		t.Fatalf("legacy source calls = %d, want 2", calls)
	}
}

func TestHandleFacadeLocalizeFingerprintPreservesCase(t *testing.T) {
	registry := newFacadeRegistry()
	var server *Server
	calls := 0
	registry.capture(mcpgo.NewTool("explore", mcpgo.WithString("task", mcpgo.Required())), func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		calls++
		server.localizationFor(ctx).armForTask(newLocalizationCompletion(true, ""), req.GetString("task", ""))
		return mcpgo.NewToolResultText("localized"), nil
	})
	server = &Server{facades: registry, localization: newLocalizationTerminalState(), sessions: newSessionMap()}
	ctx := WithSessionID(context.Background(), "repeat-localize")
	server.localizationFor(ctx).armForTask(newLocalizationCompletion(true, ""), "Locate Run Handler")

	req := mcpgo.CallToolRequest{}
	req.Params.Name = "explore"
	req.Params.Arguments = map[string]any{"operation": "localize", "task": "  Locate   Run Handler "}
	same, err := server.handleFacade(ctx, "explore", req)
	if err != nil || same == nil || !same.IsError || calls != 0 {
		t.Fatalf("same normalized localization must remain blocked: result=%#v err=%v calls=%d", same, err, calls)
	}

	req.Params.Arguments = map[string]any{"operation": "localize", "task": "Locate run Handler"}
	different, err := server.handleFacade(ctx, "explore", req)
	if err != nil || different == nil || different.IsError || calls != 1 {
		t.Fatalf("different localization task must start fresh: result=%#v err=%v calls=%d", different, err, calls)
	}
	if blocked := server.localizationFor(ctx).block("search", "symbols", nil); blocked == nil {
		t.Fatal("fresh localization result did not arm its own terminal contract")
	}
}

func TestExploreAnswerReadyUsesNormalizedRetrievalMetadata(t *testing.T) {
	node := &graph.Node{
		ID: "pkg/worker.go::run", Name: "run", Kind: graph.KindMethod,
		FilePath: "pkg/worker.go", QualName: "resolver.run",
		Meta: map[string]any{"search_qual_name": "BillingService.Reconcile", "search_signature": "func BillingService.Reconcile(invoice Invoice) error"},
	}
	task := "locate BillingService.Reconcile"
	if !exploreAnswerReady(task, []exploreTarget{{node: node, score: 1}}) {
		t.Fatal("normalized retrieval metadata should make the explicit localization answer-ready")
	}
	delete(node.Meta, "search_qual_name")
	delete(node.Meta, "search_signature")
	if exploreAnswerReady(task, []exploreTarget{{node: node, score: 1}}) {
		t.Fatal("resolver-only metadata must not accidentally satisfy the retrieval-specific query")
	}
}

func TestLocalizationEnvelopeOmitsOversizedSource(t *testing.T) {
	node := &graph.Node{ID: "pkg/huge.go::Huge", Name: "Huge", Kind: graph.KindFunction, FilePath: "pkg/huge.go"}
	result := newLocalizationExploreResult(newLocalizationCompletion(true, ""), []exploreTarget{{node: node, source: strings.Repeat("x", 32_000)}}, 1000)
	text, ok := singleTextContent(result)
	if !ok {
		t.Fatalf("expected compact text result: %#v", result)
	}
	var envelope localizationExploreEnvelope
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("decode compact envelope: %v", err)
	}
	if len(envelope.Evidence) != 1 || envelope.Evidence[0].Source != "" {
		t.Fatalf("oversized source leaked into compact envelope: %#v", envelope.Evidence)
	}
	if len(text) > 1500 {
		t.Fatalf("compact envelope exceeded size guard: %d bytes", len(text))
	}
}
