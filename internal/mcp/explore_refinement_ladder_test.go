package mcp

import (
	"encoding/json"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// The single permitted refinement read must follow the answer-draft ladder,
// not raw rank: a generic rank-one type that shares no vocabulary with the
// query must not consume the read while an aligned callable sits lower in
// the same envelope.
func TestRefinementLadderPrefersAlignedCallableOverGenericHead(t *testing.T) {
	task := "replace trailing context lines in the printed output"
	genericHead := exploreTarget{node: &graph.Node{
		ID: "repo/printer/builder.go::OutputBuilder", Kind: graph.KindType, Name: "OutputBuilder", FilePath: "repo/printer/builder.go",
	}, score: 1.0}
	alignedFn := exploreTarget{node: &graph.Node{
		ID: "repo/printer/util.go::replace_with_context", Kind: graph.KindFunction, Name: "replace_with_context", FilePath: "repo/printer/util.go",
	}, score: 0.7}

	got := explorePreferredRefinementSymbol(task, []exploreTarget{genericHead, alignedFn})
	if got != alignedFn.node.ID {
		t.Fatalf("ladder picked %q, want the aligned callable", got)
	}

	literalDistractor := exploreTarget{node: &graph.Node{
		ID: "repo/errors.go::NoMatchFoundException", Kind: graph.KindType, Name: "NoMatchFoundException", FilePath: "repo/errors.go",
	}, score: 0.4, sourceLiteral: true, source: `const locale = "ku"`}
	got = explorePreferredRefinementSymbol(task, []exploreTarget{genericHead, alignedFn, literalDistractor})
	if got != alignedFn.node.ID {
		t.Fatalf("ladder picked unrelated literal %q, want aligned callable %q", got, alignedFn.node.ID)
	}

	alignedLiteral := exploreTarget{node: &graph.Node{
		ID: "repo/printer/literal.go::replace_context", Kind: graph.KindFunction, Name: "replace_context", FilePath: "repo/printer/literal.go",
	}, score: 0.4, sourceLiteral: true}
	got = explorePreferredRefinementSymbol(task, []exploreTarget{genericHead, alignedFn, alignedLiteral})
	if got != alignedLiteral.node.ID {
		t.Fatalf("ladder picked %q, want semantically tied source-literal target", got)
	}

	unrelated := exploreTarget{node: &graph.Node{
		ID: "repo/other/thing.go::Widget", Kind: graph.KindType, Name: "Widget", FilePath: "repo/other/thing.go",
	}, score: 1.0}
	got = explorePreferredRefinementSymbol(task, []exploreTarget{unrelated})
	if got != unrelated.node.ID {
		t.Fatalf("ladder picked %q, want the raw head when nothing aligns", got)
	}
}

func TestRefinementLadderPrefersLexicallyAlignedDefaultOwnerOverFieldConsumer(t *testing.T) {
	task := "the rotating handler uses the wrong default file permission in its constructor"
	consumer := exploreTarget{node: &graph.Node{
		ID: "repo/Handler.php::StreamHandler.write", Kind: graph.KindMethod,
		Name: "write", QualName: "StreamHandler.write", FilePath: "repo/Handler.php",
	}, source: `chmod($url, $this->filePermission);`}
	owner := exploreTarget{node: &graph.Node{
		ID: "repo/RotatingFileHandler.php::RotatingFileHandler.__construct", Kind: graph.KindMethod,
		Name: "__construct", QualName: "RotatingFileHandler.__construct", FilePath: "repo/RotatingFileHandler.php",
	}, source: `function __construct($filename, $maxFiles = 0, $level = Logger::DEBUG, $bubble = true, $filePermission = null) {}`}

	got := explorePreferredRefinementSymbol(task, []exploreTarget{consumer, owner})
	if got != owner.node.ID {
		t.Fatalf("refinement selected field consumer %q, want default-setting owner %q", got, owner.node.ID)
	}
}

func TestRefinementLadderPrefersCallableOverEquallyAlignedContainer(t *testing.T) {
	task := "replace trailing context lines"
	container := exploreTarget{node: &graph.Node{
		ID: "repo/printer/util.go::ReplacementContext", Kind: graph.KindType,
		Name: "ReplacementContext", FilePath: "repo/printer/util.go",
	}, sourceLiteral: true}
	callable := exploreTarget{node: &graph.Node{
		ID: "repo/printer/util.go::replace_context", Kind: graph.KindFunction,
		Name: "replace_context", FilePath: "repo/printer/util.go",
	}}

	got := explorePreferredRefinementSymbol(task, []exploreTarget{container, callable})
	if got != callable.node.ID {
		t.Fatalf("refinement selected enclosing container %q, want callable %q", got, callable.node.ID)
	}
}

func TestRefinementLadderKeepsVerifiedLiteralWhenCallableHasNoAlignment(t *testing.T) {
	literalOwner := exploreTarget{node: &graph.Node{
		ID: "repo/registry.go::LocaleRegistry", Kind: graph.KindType,
		Name: "LocaleRegistry", FilePath: "repo/registry.go",
	}, sourceLiteral: true}
	unrelatedCallable := exploreTarget{node: &graph.Node{
		ID: "repo/worker.go::Run", Kind: graph.KindFunction,
		Name: "Run", FilePath: "repo/worker.go",
	}}

	got := explorePreferredRefinementSymbol(`"xy"`, []exploreTarget{unrelatedCallable, literalOwner})
	if got != literalOwner.node.ID {
		t.Fatalf("refinement selected zero-alignment callable %q, want verified literal owner %q", got, literalOwner.node.ID)
	}
}

func TestLocalizationBuilderLeadsWithRequiredSymbolAndItsFileUnderTightBudget(t *testing.T) {
	task := "replace trailing context lines in the printed output"
	head := exploreTarget{node: &graph.Node{
		ID: "repo/printer/builder.go::OutputBuilder", Kind: graph.KindType,
		Name: "OutputBuilder", FilePath: "repo/printer/builder.go",
	}}
	preferred := exploreTarget{node: &graph.Node{
		ID: "repo/printer/util.go::replace_with_context", Kind: graph.KindFunction,
		Name: "replace_with_context", FilePath: "repo/printer/util.go",
	}}
	completion := newLocalizationRefinementCompletion(preferred.node.ID)
	const budget = 256
	result, returnedSymbols, digest := buildLocalizationExploreResultForTask(
		completion, task, []exploreTarget{head, preferred}, budget,
	)
	if result == nil || result.IsError {
		t.Fatalf("builder result = %#v, want successful localization envelope", result)
	}
	body, ok := singleTextContent(result)
	if !ok {
		t.Fatalf("builder result has no text content: %#v", result)
	}
	if len(body) > budget*localizationEnvelopeBytesPerToken {
		t.Fatalf("envelope bytes = %d, want <= %d", len(body), budget*localizationEnvelopeBytesPerToken)
	}
	var envelope localizationExploreEnvelope
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode localization envelope: %v", err)
	}
	if len(envelope.Evidence) < 2 || len(envelope.Files) < 2 || len(envelope.Symbols) < 2 {
		t.Fatalf("tight envelope lost required/primary evidence: %#v", envelope)
	}
	if envelope.Completion.RequiredAction != completion.RequiredAction || envelope.Evidence[0].ID != preferred.node.ID ||
		envelope.Files[0] != preferred.node.FilePath || envelope.Symbols[0] != preferred.node.ID {
		t.Fatalf("required action and leading evidence diverged: completion=%#v envelope=%#v", completion, envelope)
	}
	if envelope.Evidence[1].ID != head.node.ID || envelope.Files[1] != head.node.FilePath || envelope.Symbols[1] != head.node.ID {
		t.Fatalf("original retrieval head was not retained immediately after refinement target: %#v", envelope)
	}
	if len(returnedSymbols) < 2 || returnedSymbols[0] != preferred.node.ID || returnedSymbols[1] != head.node.ID {
		t.Fatalf("authorization symbols diverged from envelope: %#v", returnedSymbols)
	}
	if digest == nil || len(digest.Evidence) < 2 || digest.Evidence[0].ID != preferred.node.ID || digest.Evidence[1].ID != head.node.ID {
		t.Fatalf("terminal digest diverged from serialized evidence: %#v", digest)
	}
}
