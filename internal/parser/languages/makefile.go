package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Makefile is target-and-recipe structured: a target line `NAME:` is
// followed by tab-indented recipe lines. We model targets and
// `define NAME ... endef` blocks as function nodes, variable
// assignments as variables, and `include` / `-include` / `sinclude`
// directives as imports.
var (
	makeTargetRe  = regexp.MustCompile(`(?m)^([A-Za-z_][\w./-]*(?:\.[A-Za-z_][\w./-]*)?)\s*:(?:[^=]|$)`)
	makeDefineRe  = regexp.MustCompile(`(?m)^define\s+([A-Za-z_]\w*)`)
	makeIncludeRe = regexp.MustCompile(`(?m)^(?:-include|sinclude|include)\s+(.+)$`)
	makeVarRe     = regexp.MustCompile(`(?m)^([A-Za-z_][\w]*)\s*(?::=|\?=|\+=|=)\s*`)
)

// MakefileExtractor extracts Makefile source using regex.
type MakefileExtractor struct{}

func NewMakefileExtractor() *MakefileExtractor { return &MakefileExtractor{} }

func (e *MakefileExtractor) Language() string { return "makefile" }
func (e *MakefileExtractor) Extensions() []string {
	return []string{".mk", ".make", "Makefile", "GNUmakefile", "makefile"}
}

func (e *MakefileExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "makefile",
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
			Language: "makefile",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	// Collect target lines to compute end = line before next top-level
	// definition (targets have no explicit terminator; indented tab
	// lines are the recipe).
	type topHit struct {
		name string
		line int
		kind graph.NodeKind
	}
	var tops []topHit
	for _, m := range makeTargetRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isMakeDirective(name) {
			continue
		}
		line := lineAt(src, m[0])
		tops = append(tops, topHit{name: name, line: line, kind: graph.KindFunction})
	}
	for _, m := range makeVarRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isMakeDirective(name) {
			continue
		}
		line := lineAt(src, m[0])
		tops = append(tops, topHit{name: name, line: line, kind: graph.KindVariable})
	}
	// Sort-by-line so end-of-range computation is monotonic.
	for i := 0; i < len(tops); i++ {
		for j := i + 1; j < len(tops); j++ {
			if tops[j].line < tops[i].line {
				tops[i], tops[j] = tops[j], tops[i]
			}
		}
	}
	for i, t := range tops {
		endLine := len(lines)
		if i+1 < len(tops) {
			endLine = tops[i+1].line - 1
			if endLine < t.line {
				endLine = t.line
			}
		}
		add(t.name, t.kind, t.line, endLine)
	}

	for _, m := range makeDefineRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		add(name, graph.KindFunction, line, findKeywordBlockEnd(lines, line, "endef"))
	}

	for _, m := range makeIncludeRe.FindAllSubmatchIndex(src, -1) {
		arg := strings.TrimSpace(string(src[m[2]:m[3]]))
		line := lineAt(src, m[0])
		// `include a.mk b.mk` may list several files.
		for _, f := range strings.Fields(arg) {
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: "unresolved::import::" + f,
				Kind: graph.EdgeImports, FilePath: filePath, Line: line,
			})
		}
	}

	return result, nil
}

// isMakeDirective filters out reserved-word collisions that the
// variable regex would otherwise catch.
func isMakeDirective(s string) bool {
	switch s {
	case "ifeq", "ifneq", "ifdef", "ifndef", "else", "endif",
		"define", "endef", "include", "sinclude", "export",
		"unexport", "override", "vpath", "VPATH":
		return true
	}
	return false
}

var _ parser.Extractor = (*MakefileExtractor)(nil)
