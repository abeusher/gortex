package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// MATLAB / Octave. Keyword-delimited — every block terminates with
// `end`. The canonical function heads are
//   function out = name(args)
//   function [out1, out2] = name(args)
//   function name(args)
//   function name
// Classes are `classdef NAME` ... `end`. Packages can be pulled in via
// `import pkg.subpkg.Symbol`. We skip `.m` so the Objective-C
// extractor keeps priority on that extension; orchestrator registration
// handles the ordering.
var (
	matlabFuncRe   = regexp.MustCompile(`(?m)^\s*function\s+(?:\[[^\]]*\]\s*=\s*|\w+\s*=\s*)?(\w+)`)
	matlabClassRe  = regexp.MustCompile(`(?m)^\s*classdef\s+(\w+)`)
	matlabImportRe = regexp.MustCompile(`(?m)^\s*import\s+(\w+(?:\.\w+)*)`)
	matlabCallRe   = regexp.MustCompile(`\b([a-zA-Z_]\w*)\s*\(`)
)

// MatlabExtractor extracts MATLAB/Octave source using regex.
type MatlabExtractor struct{}

func NewMatlabExtractor() *MatlabExtractor { return &MatlabExtractor{} }

func (e *MatlabExtractor) Language() string     { return "matlab" }
func (e *MatlabExtractor) Extensions() []string { return []string{".mlx"} }

func (e *MatlabExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	lines := strings.Split(string(src), "\n")
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: len(lines),
		Language: "matlab",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	add := func(name string, kind graph.NodeKind, start, end int) {
		if name == "" || isMatlabKeyword(name) {
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
			Language: "matlab",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: start,
		})
	}

	for _, m := range matlabFuncRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end")
		add(name, graph.KindFunction, line, end)
	}
	for _, m := range matlabClassRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		end := findKeywordBlockEnd(lines, line, "end")
		add(name, graph.KindType, line, end)
	}

	for _, m := range matlabImportRe.FindAllSubmatchIndex(src, -1) {
		mod := string(src[m[2]:m[3]])
		line := lineAt(src, m[0])
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod,
			Kind: graph.EdgeImports, FilePath: filePath, Line: line,
		})
	}

	funcRanges := buildFuncRanges(result)
	for _, m := range matlabCallRe.FindAllSubmatchIndex(src, -1) {
		name := string(src[m[2]:m[3]])
		if isMatlabKeyword(name) {
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

func isMatlabKeyword(s string) bool {
	switch s {
	case "if", "elseif", "else", "end", "for", "while", "switch", "case",
		"otherwise", "break", "continue", "return", "function", "classdef",
		"properties", "methods", "events", "enumeration", "global",
		"persistent", "try", "catch", "parfor", "spmd", "import":
		return true
	}
	return false
}

var _ parser.Extractor = (*MatlabExtractor)(nil)
