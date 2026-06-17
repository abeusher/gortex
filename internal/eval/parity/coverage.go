// Package parity computes cross-file dependency-graph coverage metrics used to
// measure — and lock in — the quality of Gortex's resolved graph per language.
package parity

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// LanguageCoverage is the per-language share of symbol-bearing source files that
// have at least one resolved cross-file dependent — something in another file
// that imports, calls, references, routes to, or otherwise depends on a symbol
// defined in the file.
type LanguageCoverage struct {
	Language     string  `json:"language"`
	SymbolFiles  int     `json:"symbol_files"`
	CoveredFiles int     `json:"covered_files"`
	Coverage     float64 `json:"coverage"`
}

// coverageSymbolKinds are the node kinds whose presence makes a file a
// symbol-bearing source file (the denominator of the coverage metric).
var coverageSymbolKinds = map[graph.NodeKind]bool{
	graph.KindFunction:   true,
	graph.KindMethod:     true,
	graph.KindType:       true,
	graph.KindInterface:  true,
	graph.KindVariable:   true,
	graph.KindConstant:   true,
	graph.KindField:      true,
	graph.KindEnumMember: true,
}

// dependencyEdgeKinds are the edge kinds that count as a cross-file dependent: a
// resolved edge of one of these kinds crossing a file boundary marks its target
// file as covered.
var dependencyEdgeKinds = map[graph.EdgeKind]bool{
	graph.EdgeImports:      true,
	graph.EdgeCalls:        true,
	graph.EdgeReferences:   true,
	graph.EdgeExtends:      true,
	graph.EdgeImplements:   true,
	graph.EdgeInstantiates: true,
	graph.EdgeTypedAs:      true,
	graph.EdgeReturns:      true,
	graph.EdgeOverrides:    true,
	graph.EdgeHandlesRoute: true,
}

// CoverageOf computes per-language resolved-cross-file-dependent coverage over
// the whole graph. Languages are returned sorted by name; a language with no
// symbol-bearing source file is omitted.
func CoverageOf(g graph.Store) []LanguageCoverage {
	nodes := make(map[string]*graph.Node)
	fileLang := make(map[string]string)
	symbolFiles := make(map[string]bool)

	for _, n := range g.AllNodes() {
		if n == nil {
			continue
		}
		nodes[n.ID] = n
		switch {
		case n.Kind == graph.KindFile:
			if path := nodeFile(n); path != "" && n.Language != "" {
				fileLang[path] = n.Language
			}
		case coverageSymbolKinds[n.Kind] && n.FilePath != "":
			symbolFiles[n.FilePath] = true
			if fileLang[n.FilePath] == "" && n.Language != "" {
				fileLang[n.FilePath] = n.Language
			}
		}
	}

	covered := make(map[string]bool)
	for _, e := range g.AllEdges() {
		if e == nil || !dependencyEdgeKinds[e.Kind] || graph.IsUnresolvedTarget(e.To) {
			continue
		}
		src, dst := nodes[e.From], nodes[e.To]
		if src == nil || dst == nil {
			continue
		}
		srcFile, dstFile := nodeFile(src), nodeFile(dst)
		if srcFile == "" || dstFile == "" || srcFile == dstFile {
			continue
		}
		covered[dstFile] = true
	}

	type acc struct{ sym, cov int }
	byLang := make(map[string]*acc)
	for f := range symbolFiles {
		a := byLang[fileLang[f]]
		if a == nil {
			a = &acc{}
			byLang[fileLang[f]] = a
		}
		a.sym++
		if covered[f] {
			a.cov++
		}
	}

	out := make([]LanguageCoverage, 0, len(byLang))
	for lang, a := range byLang {
		cov := 0.0
		if a.sym > 0 {
			cov = float64(a.cov) / float64(a.sym)
		}
		out = append(out, LanguageCoverage{Language: lang, SymbolFiles: a.sym, CoveredFiles: a.cov, Coverage: cov})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Language < out[j].Language })
	return out
}

// nodeFile returns the source-file path a node belongs to.
func nodeFile(n *graph.Node) string {
	if n.FilePath != "" {
		return n.FilePath
	}
	if n.Kind == graph.KindFile {
		return n.ID
	}
	return ""
}
