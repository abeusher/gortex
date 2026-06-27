package contracts

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Extractor analyses source files and produces Contract values.
type Extractor interface {
	// Extract scans the source of a single file and returns any contracts found.
	// nodes and edges provide graph context so the extractor can resolve the
	// nearest enclosing symbol for each match.
	Extract(filePath string, src []byte, nodes []*graph.Node, edges []*graph.Edge) []Contract

	// SupportedLanguages returns the set of languages this extractor handles.
	SupportedLanguages() []string
}

// TreeAwareExtractor is an optional capability extractors implement
// when they want to consume the file's tree-sitter parse tree (for
// AST-based handler-body enrichment) instead of regex over source
// bytes. The orchestrator type-asserts and dispatches to ExtractWithTree
// when both the extractor and the parse tree are available.
//
// Phase 1 of spec-contract-extraction.md ships only HTTPExtractor as
// tree-aware. Other extractors fall back to Extract() unchanged.
type TreeAwareExtractor interface {
	Extractor
	ExtractWithTree(
		filePath string,
		src []byte,
		nodes []*graph.Node,
		edges []*graph.Edge,
		tree *parser.ParseTree,
	) []Contract
}

// StoreAwareExtractor is an optional capability an extractor implements when it
// can resolve constant / composite endpoint arguments graph-wide using the
// graph's constant store. The orchestrator type-asserts and dispatches to
// ExtractWithStore — in preference to ExtractWithTree / Extract — passing the
// store and the repo prefix so the extractor can scope graph-wide const
// lookups. A nil store is allowed (const dereference is then disabled and the
// extractor behaves like its tree-aware path). Extractors that don't implement
// it fall back to ExtractWithTree / Extract unchanged.
type StoreAwareExtractor interface {
	Extractor
	ExtractWithStore(
		filePath string,
		src []byte,
		nodes []*graph.Node,
		edges []*graph.Edge,
		tree *parser.ParseTree,
		store EndpointConstStore,
		repoPrefix string,
	) []Contract
}
