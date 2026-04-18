package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Gleam is a BEAM-family typed language with `pub fn` / `fn`
// functions, `pub type` / `type` sum types, and `import x/y`
// modules with optional `.{A, B}` unqualified imports and
// `as Alias` renaming.
var (
	gleamFuncRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?fn\s+(\w+)\s*\(`)
	gleamTypeRe   = regexp.MustCompile(`(?m)^\s*(?:pub\s+)?type\s+(\w+)\b`)
	gleamImportRe = regexp.MustCompile(`(?m)^\s*import\s+([\w/]+)`)
	gleamCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// GleamExtractor extracts Gleam source using regex.
type GleamExtractor struct{}

func NewGleamExtractor() *GleamExtractor { return &GleamExtractor{} }

func (e *GleamExtractor) Language() string     { return "gleam" }
func (e *GleamExtractor) Extensions() []string { return []string{".gleam"} }

func (e *GleamExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "gleam",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isGleamKeyword(name) {
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
			Language: "gleam",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range gleamFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range gleamTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range gleamImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range gleamCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isGleamKeyword(name) {
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

func isGleamKeyword(s string) bool {
	switch s {
	case "if", "else", "case", "let", "assert", "use",
		"fn", "type", "pub", "import", "as", "opaque",
		"const", "todo", "panic", "external", "try",
		"True", "False", "Nil", "Ok", "Error":
		return true
	}
	return false
}

var _ parser.Extractor = (*GleamExtractor)(nil)
