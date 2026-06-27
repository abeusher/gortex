package languages

import (
	"regexp"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Expo Modules cross-language support. An Expo native module is declared
// in Swift or Kotlin with a DSL inside `definition() -> ModuleDefinition`:
// `Name("Foo")` sets the JS module name and `Function("bar") { ... }` /
// `AsyncFunction("baz") { ... }` declare the JS-callable methods, while
// `Property("p")`, `Events("e")`, and `Constants { ... }` declare the other
// member kinds. On the JS side they are consumed via
// `requireNativeModule('Foo').bar(...)` — which the JS/TS extractor already
// emits as an rn-native placeholder, so the Expo bridge synthesizer can land
// it on these synthetic nodes.

var (
	expoNameRe        = regexp.MustCompile(`\bName\s*\(\s*"([^"]+)"`)
	expoMemberRe      = regexp.MustCompile(`\b(Function|AsyncFunction|Property|Events)\s*(?:<[^(]*>)?\s*\(\s*"([^"]+)"`)
	expoConstantsRe   = regexp.MustCompile(`\bConstants\s*[({]`)
	expoModuleClassRe = regexp.MustCompile(`\bclass\s+(\w+)\s*(?:\([^)]*\))?\s*:\s*Module\b`)
)

// expoExport is one Expo DSL member declaration (function, property, events,
// or constants) attributed to a module.
type expoExport struct {
	module string
	method string
	async  bool
	off    int
	kind   string
}

// extractExpoModules scans Swift/Kotlin source for the Expo module DSL and
// returns each member declaration attributed to the most recent preceding
// Name("..."). A definition() body with no Name() falls back to its enclosing
// `class XxxModule : Module` (the "Module" suffix stripped) rather than being
// dropped. Returns nil when the file is not an Expo module (no
// ModuleDefinition marker).
func extractExpoModules(src []byte) []expoExport {
	s := string(src)
	if !strings.Contains(s, "ModuleDefinition") {
		return nil
	}
	type marker struct {
		off   int
		kind  string
		name  string
		async bool
	}
	var markers []marker
	for _, m := range expoNameRe.FindAllStringSubmatchIndex(s, -1) {
		markers = append(markers, marker{off: m[0], kind: "name", name: s[m[2]:m[3]]})
	}
	for _, m := range expoMemberRe.FindAllStringSubmatchIndex(s, -1) {
		kw := s[m[2]:m[3]]
		kind := "function"
		switch kw {
		case "Property":
			kind = "property"
		case "Events":
			kind = "events"
		}
		markers = append(markers, marker{off: m[0], kind: kind, name: s[m[4]:m[5]], async: kw == "AsyncFunction"})
	}
	for _, m := range expoConstantsRe.FindAllStringIndex(s, -1) {
		markers = append(markers, marker{off: m[0], kind: "constants", name: "Constants"})
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].off < markers[j].off })

	// Module classes back the no-Name() fallback.
	type classDecl struct {
		off  int
		name string
	}
	var classes []classDecl
	for _, m := range expoModuleClassRe.FindAllStringSubmatchIndex(s, -1) {
		classes = append(classes, classDecl{off: m[0], name: s[m[2]:m[3]]})
	}
	enclosingModule := func(off int) string {
		name, best := "", -1
		for _, cd := range classes {
			if cd.off <= off && cd.off > best {
				best, name = cd.off, cd.name
			}
		}
		if name == "" {
			return ""
		}
		if trimmed := strings.TrimSuffix(name, "Module"); trimmed != "" {
			return trimmed
		}
		return name
	}

	curModule := ""
	var out []expoExport
	for _, mk := range markers {
		if mk.kind == "name" {
			curModule = mk.name
			continue
		}
		module := curModule
		if module == "" {
			module = enclosingModule(mk.off)
		}
		if module == "" {
			continue
		}
		out = append(out, expoExport{module: module, method: mk.name, async: mk.async, off: mk.off, kind: mk.kind})
	}
	return out
}

// emitExpoModuleNodes materialises a synthetic node per Expo member
// declaration, carrying expo_module + expo_method + expo_kind so the Expo
// bridge synthesizer can pair a JS requireNativeModule('<module>').<member>
// access to it. A Property becomes a field node; every other member kind a
// method node.
func emitExpoModuleNodes(src []byte, filePath, language, fileID string, result *parser.ExtractionResult, seen map[string]bool) {
	for _, ex := range extractExpoModules(src) {
		id := filePath + "::expo:" + ex.module + ":" + ex.method
		if seen[id] {
			continue
		}
		seen[id] = true
		line := lineAt(src, ex.off)
		nodeKind := graph.KindMethod
		if ex.kind == "property" {
			nodeKind = graph.KindField
		}
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: nodeKind, Name: ex.method,
			FilePath: filePath, StartLine: line, EndLine: line,
			Language: language,
			Meta:     map[string]any{"expo_module": ex.module, "expo_method": ex.method, "expo_async": ex.async, "expo_kind": ex.kind},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: line,
		})
	}
}
