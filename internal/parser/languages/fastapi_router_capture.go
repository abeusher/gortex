package languages

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// captureFastAPIRouterRefs emits a placeholder reference to the router named
// in an `include_router(api_router, ...)` mount. The Python extractor's call
// loop drops module-level calls (no enclosing function), so the router
// argument is otherwise invisible to the graph. The edge is tagged
// `via=fastapi.router` and attributed to the enclosing function — or the file
// node for the usual module-level mount — so the FastAPI resolver can bind a
// `*_router` defined by directory convention. Only a bare-identifier first
// argument is captured; a dotted `pkg.router` is left to the import resolver.
// Runs at the tail of Extract.
func captureFastAPIRouterRefs(result *parser.ExtractionResult, root *sitter.Node, filePath string, src []byte) {
	if root == nil || result == nil {
		return
	}
	seen := map[string]bool{}
	expressWalk(root, func(c *sitter.Node) {
		if c.Type() != "call" {
			return
		}
		if pyCalleeLeaf(c.ChildByFieldName("function"), src) != "include_router" {
			return
		}
		router := firstIdentifierArg(c, src)
		if router == "" {
			return
		}
		line := int(c.StartPoint().Row) + 1
		from := pyEnclosingNodeID(result.Nodes, line, filePath)
		key := from + "\x00" + router
		if seen[key] {
			return
		}
		seen[key] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From: from, To: "unresolved::" + router, Kind: graph.EdgeReferences,
			FilePath: filePath, Line: line, Origin: graph.OriginASTInferred,
			Meta: map[string]any{"via": "fastapi.router", "router_name": router},
		})
	})
}

// pyCalleeLeaf returns the leaf name of a Python call's function node — the
// bare identifier, or the attribute leaf of `app.include_router`.
func pyCalleeLeaf(fn *sitter.Node, src []byte) string {
	if fn == nil {
		return ""
	}
	switch fn.Type() {
	case "identifier":
		return fn.Content(src)
	case "attribute":
		if a := fn.ChildByFieldName("attribute"); a != nil {
			return a.Content(src)
		}
	}
	return ""
}

// pyEnclosingNodeID returns the ID of the innermost function/method node whose
// line range contains line, or the file node (filePath) when none does.
func pyEnclosingNodeID(nodes []*graph.Node, line int, filePath string) string {
	best := ""
	bestSpan := 1 << 30
	for _, n := range nodes {
		if n == nil || (n.Kind != graph.KindFunction && n.Kind != graph.KindMethod) {
			continue
		}
		if n.StartLine <= line && line <= n.EndLine {
			if span := n.EndLine - n.StartLine; span < bestSpan {
				bestSpan = span
				best = n.ID
			}
		}
	}
	if best == "" {
		return filePath
	}
	return best
}
