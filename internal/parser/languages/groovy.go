package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Groovy is a JVM language with Java-like aggregates plus `def`
// dynamic functions and `trait`. Gradle build scripts are also
// Groovy; we share the extractor.
var (
	groovyClassRe     = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:public\s+|private\s+|protected\s+|abstract\s+|final\s+|static\s+)*class\s+(\w+)`)
	groovyInterfaceRe = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:public\s+|private\s+|protected\s+)*interface\s+(\w+)`)
	groovyEnumRe      = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+)*enum\s+(\w+)`)
	groovyTraitRe     = regexp.MustCompile(`(?m)^\s*(?:public\s+|private\s+|protected\s+)*trait\s+(\w+)`)
	groovyDefRe       = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:public\s+|private\s+|protected\s+|static\s+|final\s+)*def\s+(\w+)\s*\(`)
	groovyTypedFuncRe = regexp.MustCompile(`(?m)^\s*(?:@\w+(?:\([^)]*\))?\s+)*(?:public\s+|private\s+|protected\s+|static\s+|final\s+)+[\w\[\]\.<>,\s\?]+?\s+(\w+)\s*\([^)]*\)\s*\{`)
	groovyImportRe    = regexp.MustCompile(`(?m)^\s*import\s+(?:static\s+)?([\w.\*]+)`)
)

// GroovyExtractor extracts Groovy / Gradle source using regex.
type GroovyExtractor struct{}

func NewGroovyExtractor() *GroovyExtractor { return &GroovyExtractor{} }

func (e *GroovyExtractor) Language() string     { return "groovy" }
func (e *GroovyExtractor) Extensions() []string { return []string{".groovy", ".gvy", ".gy", ".gradle"} }

func (e *GroovyExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "groovy",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || groovyIsKeyword(name) {
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
			Language: "groovy",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range groovyClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range groovyInterfaceRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindInterface, line, findBlockEnd(lines, line))
	}
	for _, m := range groovyEnumRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range groovyTraitRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindType, line, findBlockEnd(lines, line))
	}
	for _, m := range groovyDefRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}
	for _, m := range groovyTypedFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findBlockEnd(lines, line))
	}

	for _, m := range groovyImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	return result, nil
}

func groovyIsKeyword(s string) bool {
	switch s {
	case "if", "else", "while", "for", "do", "switch", "case", "default",
		"return", "break", "continue", "class", "interface", "enum",
		"trait", "def", "import", "package", "public", "private",
		"protected", "static", "final", "abstract", "void", "int", "long",
		"short", "byte", "float", "double", "boolean", "char", "String",
		"true", "false", "null", "this", "super", "new", "try", "catch",
		"finally", "throw", "throws", "synchronized":
		return true
	}
	return false
}

var _ parser.Extractor = (*GroovyExtractor)(nil)
