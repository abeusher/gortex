package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Odin uses `name :: proc(args) { ... }` for procedures and
// `Name :: struct { ... }` for types. Imports use `import "path"`
// with an optional alias prefix.
var (
	odinProcRe        = regexp.MustCompile(`(?m)^\s*(\w+)\s*::\s*proc\b`)
	odinTypeRe        = regexp.MustCompile(`(?m)^\s*(\w+)\s*::\s*(struct|enum|union)\b`)
	odinImportRe      = regexp.MustCompile(`(?m)^\s*import\s+(?:(\w+)\s+)?"([^"]+)"`)
	odinForeignImport = regexp.MustCompile(`(?m)^\s*foreign\s+import\s+\w+\s+"([^"]+)"`)
	odinPackageRe     = regexp.MustCompile(`(?m)^\s*package\s+(\w+)`)
	odinCallRe        = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// OdinExtractor extracts Odin source using regex.
type OdinExtractor struct{}

func NewOdinExtractor() *OdinExtractor { return &OdinExtractor{} }

func (e *OdinExtractor) Language() string     { return "odin" }
func (e *OdinExtractor) Extensions() []string { return []string{".odin"} }

func (e *OdinExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "odin",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isOdinKeyword(name) {
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
			Language: "odin",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range odinPackageRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, line)
	}
	for _, m := range odinProcRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range odinTypeRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}

	for _, m := range odinImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[4]:m[5]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}
	for _, m := range odinForeignImport.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range odinCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isOdinKeyword(name) {
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

func isOdinKeyword(s string) bool {
	switch s {
	case "if", "else", "for", "switch", "case", "break", "continue",
		"return", "defer", "when", "in", "not_in", "do",
		"proc", "struct", "enum", "union", "bit_set", "map", "distinct",
		"package", "import", "foreign", "using", "where",
		"true", "false", "nil", "or_else", "or_return":
		return true
	}
	return false
}

var _ parser.Extractor = (*OdinExtractor)(nil)
