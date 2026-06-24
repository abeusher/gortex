package languages

import (
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// captureSwiftUIRoles classifies SwiftUI types: a struct/class conforming to
// `View` is a component; an `@main` struct conforming to `App` is the app
// entry point. The role is stamped on the type node's Meta["swiftui_role"],
// and an app entry additionally carries Meta["entry_point"]=true so the
// dead-code and process analyzers treat it as a root. Runs at the tail of
// Extract so the type nodes already exist.
func captureSwiftUIRoles(result *parser.ExtractionResult, root *sitter.Node, filePath string, src []byte) {
	if root == nil || result == nil {
		return
	}
	swiftUIWalk(root, func(n *sitter.Node) {
		if n.Type() != "class_declaration" {
			return
		}
		name := swiftUITypeName(n, src)
		if name == "" {
			return
		}
		conf := swiftUIConformances(n, src)
		role := ""
		switch {
		case swiftUIHasMainAttr(n, src) && conf["App"]:
			role = "app_entry"
		case conf["View"]:
			role = "component"
		}
		if role == "" {
			return
		}
		nd := findSwiftUITypeNode(result.Nodes, name, int(n.StartPoint().Row)+1)
		if nd == nil {
			return
		}
		if nd.Meta == nil {
			nd.Meta = map[string]any{}
		}
		nd.Meta["swiftui_role"] = role
		if role == "app_entry" {
			nd.Meta["entry_point"] = true
		}
	})
}

// swiftUITypeName returns the declared name of a class_declaration — its first
// direct type_identifier child (the modifiers / inheritance type_identifiers
// are nested deeper, not direct children).
func swiftUITypeName(decl *sitter.Node, src []byte) string {
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		if c := decl.NamedChild(i); c != nil && c.Type() == "type_identifier" {
			return c.Content(src)
		}
	}
	return ""
}

// swiftUIConformances returns the set of protocol / base-type names in a
// declaration's inheritance clause.
func swiftUIConformances(decl *sitter.Node, src []byte) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		c := decl.NamedChild(i)
		if c == nil || c.Type() != "inheritance_specifier" {
			continue
		}
		swiftUIWalk(c, func(t *sitter.Node) {
			if t.Type() == "type_identifier" {
				out[t.Content(src)] = true
			}
		})
	}
	return out
}

// swiftUIHasMainAttr reports whether a declaration carries the `@main` attribute.
func swiftUIHasMainAttr(decl *sitter.Node, src []byte) bool {
	for i := 0; i < int(decl.NamedChildCount()); i++ {
		c := decl.NamedChild(i)
		if c == nil || c.Type() != "modifiers" {
			continue
		}
		found := false
		swiftUIWalk(c, func(t *sitter.Node) {
			if t.Type() == "type_identifier" && t.Content(src) == "main" {
				found = true
			}
		})
		if found {
			return true
		}
	}
	return false
}

// findSwiftUITypeNode returns the type node for name, preferring the one whose
// start line matches the declaration.
func findSwiftUITypeNode(nodes []*graph.Node, name string, line int) *graph.Node {
	var byName *graph.Node
	for _, n := range nodes {
		if n == nil || n.Kind != graph.KindType || n.Name != name {
			continue
		}
		if n.StartLine == line {
			return n
		}
		if byName == nil {
			byName = n
		}
	}
	return byName
}

// swiftUIWalk visits n and all its named descendants.
func swiftUIWalk(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for i := 0; i < int(n.NamedChildCount()); i++ {
		swiftUIWalk(n.NamedChild(i), fn)
	}
}
