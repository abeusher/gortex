package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func momentumTextOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestMomentumNoteFiresOnceAtThreshold(t *testing.T) {
	s := &Server{session: &sessionState{}}
	ctx := WithSessionID(context.Background(), "sess_momentum")

	for i := 1; i < momentumReadThreshold; i++ {
		res := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultText("ok"))
		if strings.Contains(momentumTextOf(res), "Session note") {
			t.Fatalf("note fired early at read %d", i)
		}
	}
	res := s.maybeAttachMomentumNote(ctx, "search_symbols", mcp.NewToolResultText("ok"))
	if !strings.Contains(momentumTextOf(res), "Session note") {
		t.Fatalf("note did not fire at threshold read %d", momentumReadThreshold)
	}
	// One-shot: never again in the same session.
	res = s.maybeAttachMomentumNote(ctx, "read_file", mcp.NewToolResultText("ok"))
	if strings.Contains(momentumTextOf(res), "Session note") {
		t.Fatal("note fired twice in one session")
	}
}

func TestMomentumNoteIgnoresNonReadAndErrors(t *testing.T) {
	s := &Server{}
	ctx := WithSessionID(context.Background(), "sess_momentum2")
	for i := 0; i < momentumReadThreshold*2; i++ {
		// Edit tools never count.
		res := s.maybeAttachMomentumNote(ctx, "edit_file", mcp.NewToolResultText("ok"))
		if strings.Contains(momentumTextOf(res), "Session note") {
			t.Fatal("note fired for a non-read tool")
		}
		// Error results never count and are never decorated.
		errRes := s.maybeAttachMomentumNote(ctx, "get_symbol_source", mcp.NewToolResultError("boom"))
		if strings.Contains(momentumTextOf(errRes), "Session note") {
			t.Fatal("note fired on an error result")
		}
	}
}
