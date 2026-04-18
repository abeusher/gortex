package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// The D language is C-like and brace-delimited. It has the full
// ML-ish aggregate set (struct/class/interface/enum/union/template),
// plus a `module` statement that names the unit (not a symbol).
var (
	dFuncRe      = regexp.MustCompile(`(?m)^\s*(?:(?:public|private|protected|package|static|final|override|pure|nothrow|extern(?:\([^)]*\))?|export|pragma\([^)]*\))\s+)*(?:[\w\[\]\*\.!\(\)]+)\s+(\w+)\s*(?:\([^)]*\))?\s*(?:\([^)]*\))?\s*\{`)
	dStructRe    = regexp.MustCompile(`(?m)^\s*struct\s+(\w+)`)
	dClassRe     = regexp.MustCompile(`(?m)^\s*(?:abstract\s+|final\s+)*class\s+(\w+)`)
	dInterfaceRe = regexp.MustCompile(`(?m)^\s*interface\s+(\w+)`)
	dEnumRe      = regexp.MustCompile(`(?m)^\s*enum\s+(\w+)`)
	dUnionRe     = regexp.MustCompile(`(?m)^\s*union\s+(\w+)`)
	dTemplateRe  = regexp.MustCompile(`(?m)^\s*template\s+(\w+)\s*\(`)
	dImportRe    = regexp.MustCompile(`(?m)^\s*(?:static\s+|public\s+|private\s+)?import\s+([\w.]+)`)
)

// DExtractor extracts D-language source using regex.
type DExtractor struct{}

func NewDExtractor() *DExtractor { return &DExtractor{} }

func (e *DExtractor) Language() string     { return "d" }
func (e *DExtractor) Extensions() []string { return []string{".d", ".di"} }

func (e *DExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "d",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || dIsKeyword(name) {
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
			Language: "d",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range dStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range dClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range dInterfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range dEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range dUnionRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range dTemplateRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range dFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range dImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func dIsKeyword(s string) bool {
	switch s {
	case "if", "else", "while", "for", "foreach", "do", "switch", "case",
		"default", "return", "break", "continue", "struct", "class",
		"interface", "enum", "union", "template", "import", "module",
		"public", "private", "protected", "package", "static", "final",
		"override", "pure", "nothrow", "extern", "export", "pragma",
		"void", "int", "uint", "long", "ulong", "short", "ushort",
		"byte", "ubyte", "float", "double", "real", "bool", "char",
		"wchar", "dchar", "string", "auto", "const", "immutable",
		"shared", "ref", "in", "out", "inout", "true", "false", "null":
		return true
	}
	return false
}

var _ parser.Extractor = (*DExtractor)(nil)
