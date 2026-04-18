package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// ReScript (OCaml-derived) uses `let`, `type`, and `module`.
// Functions are `let NAME = (args) => body`; we detect the arrow
// to separate them from plain variables. Imports use `open` and
// `include`.
var (
	rescriptFuncRe     = regexp.MustCompile(`(?m)^\s*let\s+(?:rec\s+)?(\w+)\s*(?::[^=]+)?=\s*\(`)
	rescriptLetRe      = regexp.MustCompile(`(?m)^\s*let\s+(?:rec\s+)?(\w+)\s*(?::[^=]+)?=\s*\S`)
	rescriptTypeRe     = regexp.MustCompile(`(?m)^\s*type\s+(\w+)`)
	rescriptModuleRe   = regexp.MustCompile(`(?m)^\s*module\s+(\w+)`)
	rescriptOpenRe     = regexp.MustCompile(`(?m)^\s*(?:open|include)\s+([\w.]+)`)
	rescriptCallRe     = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// ReScriptExtractor extracts ReScript source using regex.
type ReScriptExtractor struct{}

func NewReScriptExtractor() *ReScriptExtractor { return &ReScriptExtractor{} }

func (e *ReScriptExtractor) Language() string     { return "rescript" }
func (e *ReScriptExtractor) Extensions() []string { return []string{".res", ".resi"} }

func (e *ReScriptExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "rescript",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isReScriptKeyword(name) {
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
			Language: "rescript",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range rescriptFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range rescriptTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range rescriptModuleRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	// plain `let` bindings that aren't already captured as functions
	for _, m := range rescriptLetRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindVariable, line, line)
	}

	for _, m := range rescriptOpenRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range rescriptCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isReScriptKeyword(name) {
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

func isReScriptKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "while", "switch", "match", "when",
		"return", "break", "continue",
		"let", "rec", "type", "module", "and", "as", "open", "include",
		"external", "mutable", "private", "of", "fun", "try", "catch",
		"true", "false", "lazy", "exception", "assert", "in", "to", "downto":
		return true
	}
	return false
}

var _ parser.Extractor = (*ReScriptExtractor)(nil)
