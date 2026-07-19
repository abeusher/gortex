package mcp

import (
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/graph"
)

func TestAnswerReadyViaLiteralOnly(t *testing.T) {
	task := `unsupported locale "ku"`
	head := exploreTarget{node: &graph.Node{
		ID: "repo/locale/registry.go::registerLocale", Kind: graph.KindFunction,
		Name: "registerLocale", FilePath: "repo/locale/registry.go",
	}, exactContent: true}
	if !exploreAnswerReady(task, []exploreTarget{head}) {
		t.Fatal("fixture must be terminal via the literal rung")
	}
	if !exploreAnswerReadyViaLiteralOnly(task, []exploreTarget{head}) {
		t.Fatal("terminality resting on exactContent must report literal-only")
	}

	anchorTask := "repo/locale/registry.go"
	anchorHead := exploreTarget{node: head.node, exactContent: true}
	if !exploreAnswerReady(anchorTask, []exploreTarget{anchorHead}) {
		t.Fatal("fixture must be terminal via the path anchor")
	}
	if exploreAnswerReadyViaLiteralOnly(anchorTask, []exploreTarget{anchorHead}) {
		t.Fatal("anchor-driven terminality must not report literal-only")
	}
}

func TestResultCitesTaskLiteral(t *testing.T) {
	task := `find the handler for "connection reset by peer"`
	with := mcpgo.NewToolResultText(`{"evidence":[{"source":"return errors.New(\"connection reset by peer\")"}]}`)
	if !exploreResultCitesTaskLiteral(with, task) {
		t.Fatal("envelope containing the literal must pass")
	}
	without := mcpgo.NewToolResultText(`{"evidence":[{"name":"handleReset"}]}`)
	if exploreResultCitesTaskLiteral(without, task) {
		t.Fatal("envelope without the literal must fail the provenance gate")
	}
	if !exploreResultCitesTaskLiteral(without, "no quoted literal here") {
		t.Fatal("tasks without literals pass vacuously")
	}
}
