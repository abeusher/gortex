package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Jinja2 wraps control flow in `{% ... %}` and expressions in
// `{{ ... }}`. The extractor captures `{% block name %}` and
// `{% macro name(args) %}` as function nodes, and the family of
// `extends`, `include`, `import`, `from ... import ...` tags as
// import edges. Block and macro bodies are closed by matching
// `{% endblock %}` / `{% endmacro %}` which we find by keyword scan.
var (
	jinjaBlockRe     = regexp.MustCompile(`(?m)\{%\s*block\s+([A-Za-z_][\w]*)`)
	jinjaMacroRe     = regexp.MustCompile(`(?m)\{%\s*macro\s+([A-Za-z_][\w]*)\s*\(`)
	jinjaExtendsRe   = regexp.MustCompile(`(?m)\{%\s*extends\s+['"]([^'"]+)['"]`)
	jinjaIncludeRe   = regexp.MustCompile(`(?m)\{%\s*include\s+['"]([^'"]+)['"]`)
	jinjaImportRe    = regexp.MustCompile(`(?m)\{%\s*import\s+['"]([^'"]+)['"]`)
	jinjaFromImportRe = regexp.MustCompile(`(?m)\{%\s*from\s+['"]([^'"]+)['"]\s+import`)
)

// JinjaExtractor extracts Jinja2 templates using regex.
type JinjaExtractor struct{}

func NewJinjaExtractor() *JinjaExtractor { return &JinjaExtractor{} }

func (e *JinjaExtractor) Language() string     { return "jinja" }
func (e *JinjaExtractor) Extensions() []string { return []string{".jinja", ".jinja2", ".j2"} }

func (e *JinjaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "jinja",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" {
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
			Language: "jinja",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range jinjaBlockRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "{% endblock"))
	}
	for _, m := range jinjaMacroRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "{% endmacro"))
	}

	for _, re := range []*regexp.Regexp{jinjaExtendsRe, jinjaIncludeRe, jinjaImportRe, jinjaFromImportRe} {
		for _, m := range re.FindAllSubmatchIndex(src, -1) {
			mod := string(src[m[2]:m[3]])
			line := lineAt(src, m[0])
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: "unresolved::import::" + mod,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return result, nil
}

var _ parser.Extractor = (*JinjaExtractor)(nil)
