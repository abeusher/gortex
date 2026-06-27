package languages

import (
	"regexp"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// objcSelectorRe matches an Objective-C @selector(...) literal, capturing the
// full selector including its colons (`doThing:`, `setX:y:`).
var objcSelectorRe = regexp.MustCompile(`@selector\s*\(\s*([A-Za-z_][A-Za-z0-9_:]*)\s*\)`)

// captureObjCSelectors records each @selector(sel) literal whose selector names
// a method declared in the same file as a function-as-value reference, so the
// targeted method is reachable through the selector even though no direct call
// edge exists. Objective-C is regex-extracted, so this is a focused scan rather
// than the tree-walking captureFnValueCandidates pass; it mirrors the Swift
// #selector handling (recv hint <self>, resolved against same-file methods).
func captureObjCSelectors(src []byte, methodRanges []objcSpan, filePath string, result *parser.ExtractionResult) {
	funcs := map[string]bool{}
	for _, n := range result.Nodes {
		if n == nil || n.FilePath != filePath {
			continue
		}
		if n.Kind == graph.KindMethod || n.Kind == graph.KindFunction {
			funcs[n.Name] = true
		}
	}
	var cands []FnValueCandidate
	seen := map[string]bool{}
	for _, m := range objcSelectorRe.FindAllSubmatchIndex(src, -1) {
		sel := string(src[m[2]:m[3]])
		if sel == "" || !funcs[sel] {
			continue
		}
		line := lineAt(src, m[0])
		fromID := objcEnclosing(methodRanges, line)
		if fromID == "" {
			continue
		}
		key := fromID + "\x00" + sel
		if seen[key] {
			continue
		}
		seen[key] = true
		cands = append(cands, FnValueCandidate{
			FromID: fromID, Name: sel, FilePath: filePath, Line: line,
			Form: "selector", RecvHint: "<self>", Lang: "objc",
		})
	}
	EmitFnValueCandidates(result, cands)
}
