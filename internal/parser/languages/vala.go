package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Vala is a C#-flavoured language that compiles to C. It uses
// namespace blocks, `using X;` imports, and has familiar class /
// interface / struct / enum aggregates.
var (
	valaClassRe     = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|internal\s+|protected\s+|abstract\s+|sealed\s+|static\s+)*class\s+(\w+)`)
	valaInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|internal\s+)*interface\s+(\w+)`)
	valaStructRe    = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|internal\s+)*struct\s+(\w+)`)
	valaEnumRe      = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|internal\s+)*enum\s+(\w+)`)
	valaNamespaceRe = regexp.MustCompile(`(?m)^\s*namespace\s+([\w.]+)`)
	valaFuncRe      = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+|internal\s+|static\s+|virtual\s+|override\s+|async\s+|signal\s+|abstract\s+)+[\w\[\]\?\*\.<>,\s]+?\s+(\w+)\s*\([^)]*\)\s*(?:throws\s+[\w.,\s]+)?\s*\{`)
	valaUsingRe     = regexp.MustCompile(`(?m)^\s*using\s+([\w.]+)\s*;`)
)

// ValaExtractor extracts Vala source using regex.
type ValaExtractor struct{}

func NewValaExtractor() *ValaExtractor { return &ValaExtractor{} }

func (e *ValaExtractor) Language() string     { return "vala" }
func (e *ValaExtractor) Extensions() []string { return []string{".vala", ".vapi"} }

func (e *ValaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "vala",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || valaIsKeyword(name) {
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
			Language: "vala",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range valaNamespaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range valaClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range valaInterfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range valaStructRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range valaEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range valaFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range valaUsingRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func valaIsKeyword(s string) bool {
	switch s {
	case "if", "else", "while", "for", "foreach", "do", "switch", "case",
		"default", "return", "break", "continue", "class", "interface",
		"struct", "enum", "namespace", "using", "public", "private",
		"protected", "internal", "static", "virtual", "override", "abstract",
		"sealed", "async", "signal", "void", "int", "uint", "long", "bool",
		"string", "char", "float", "double", "true", "false", "null", "this",
		"new", "delete", "throws", "try", "catch", "finally", "throw":
		return true
	}
	return false
}

var _ parser.Extractor = (*ValaExtractor)(nil)
