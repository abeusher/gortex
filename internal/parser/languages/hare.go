package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Hare uses `fn NAME(args) ret = { ... };` for functions and
// `type NAME = struct { ... };` / `= union { ... };` / `= enum`
// for type definitions. Imports use `use X;` or `use X::Y;`.
var (
	hareFuncRe   = regexp.MustCompile(`(?m)^\s*(?:export\s+)?fn\s+(\w+)\s*\(`)
	hareTypeRe   = regexp.MustCompile(`(?m)^\s*(?:export\s+)?type\s+(\w+)\s*=\s*(?:struct|union|enum)`)
	hareUseRe    = regexp.MustCompile(`(?m)^\s*use\s+([\w:]+)\s*;`)
	hareCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// HareExtractor extracts Hare source using regex.
type HareExtractor struct{}

func NewHareExtractor() *HareExtractor { return &HareExtractor{} }

func (e *HareExtractor) Language() string     { return "hare" }
func (e *HareExtractor) Extensions() []string { return []string{".ha"} }

func (e *HareExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "hare",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isHareKeyword(name) {
			return
		}
		id := filePath + "::" + name
		if seen[id] {
			return
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: kind, Name: name,
			FilePath: filePath, StartLine: start, EndLine: end,
			Language: "hare",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range hareFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range hareTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range hareUseRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range hareCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isHareKeyword(name) {
			continue
		}
		line := lineAt(src, m[0])
		callerID := findEnclosingFunc(funcRanges, line)
		if callerID == "" || strings.HasSuffix(callerID, "::"+name) {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func isHareKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "switch", "match", "case", "break", "continue",
		"return", "defer", "yield", "abort", "assert",
		"fn", "type", "struct", "union", "enum", "const", "let",
		"use", "export", "static", "nullable", "const_fn",
		"true", "false", "null", "void", "as", "is":
		return true
	}
	return false
}

var _ parser.Extractor = (*HareExtractor)(nil)
