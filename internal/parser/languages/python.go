package languages

import (
	"strings"
	"unicode"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

const (
	pyQFunction = `(function_definition
		name: (identifier) @func.name) @func.def`

	pyQClass = `(class_definition
		name: (identifier) @class.name) @class.def`

	pyQImport = `(import_statement
		name: (dotted_name) @import.name) @import.def`

	pyQImportFrom = `(import_from_statement
		module_name: (dotted_name) @import.module) @import.def`

	pyQCall = `(call
		function: (identifier) @call.name) @call.expr`

	pyQCallAttr = `(call
		function: (attribute
			object: (_) @call.receiver
			attribute: (identifier) @call.method)) @call.expr`

	pyQAssignment = `(assignment
		left: (identifier) @var.name) @var.def`

	// Tier 0: typed assignment — x: Type = expr
	pyQTypedAssignment = `(assignment
		left: (identifier) @tvar.name
		type: (type (identifier) @tvar.type)) @tvar.def`

	// Tier 1: untyped assignment — x = expr (for constructor inference)
	pyQUntypedAssignment = `(assignment
		left: (identifier) @uvar.name
		right: (call
			function: (identifier) @uvar.callee)) @uvar.def`

	pyQClassMethod = `(class_definition
		name: (identifier) @class.name
		body: (block
			(function_definition
				name: (identifier) @method.name) @method.def))`
)

// PythonExtractor extracts Python source files.
type PythonExtractor struct {
	lang *sitter.Language
}

func NewPythonExtractor() *PythonExtractor {
	return &PythonExtractor{lang: python.GetLanguage()}
}

func (e *PythonExtractor) Language() string     { return "python" }
func (e *PythonExtractor) Extensions() []string { return []string{".py"} }

func (e *PythonExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	tree, err := parser.ParseFile(src, e.lang)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	result := &parser.ExtractionResult{}

	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: int(root.EndPoint().Row) + 1,
		Language: "python",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)
	methodLines := make(map[int]bool) // track lines already extracted as methods

	// Class methods — extract before functions so we can skip them.
	matches, _ := parser.RunQuery(pyQClassMethod, e.lang, root, src)
	for _, m := range matches {
		className := m.Captures["class.name"].Text
		methodName := m.Captures["method.name"].Text
		def := m.Captures["method.def"]

		id := filePath + "::" + className + "." + methodName
		if seen[id] {
			continue
		}
		seen[id] = true
		methodLines[def.StartLine] = true

		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: methodName,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "python", Meta: map[string]any{
				"receiver":  className,
				"signature": "def " + methodName + "(...)",
			},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
		typeID := filePath + "::" + className
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: typeID, Kind: graph.EdgeMemberOf, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Functions (top-level only — skip lines already extracted as methods).
	matches, _ = parser.RunQuery(pyQFunction, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["func.name"].Text
		def := m.Captures["func.def"]
		if methodLines[def.StartLine] {
			continue
		}
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true

		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindFunction, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "python", Meta: map[string]any{"signature": "def " + name + "(...)"},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Classes.
	matches, _ = parser.RunQuery(pyQClass, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["class.name"].Text
		def := m.Captures["class.def"]
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindType, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "python",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Imports.
	matches, _ = parser.RunQuery(pyQImport, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["import.name"]
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + name.Text,
			Kind: graph.EdgeImports, FilePath: filePath, Line: name.StartLine + 1,
		})
	}
	matches, _ = parser.RunQuery(pyQImportFrom, e.lang, root, src)
	for _, m := range matches {
		mod := m.Captures["import.module"]
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + mod.Text,
			Kind: graph.EdgeImports, FilePath: filePath, Line: mod.StartLine + 1,
		})
	}

	// Build type environment before extracting calls.
	tenv := e.buildTypeEnv(root, src)

	// Call sites.
	funcRanges := buildFuncRanges(result)

	matches, _ = parser.RunQuery(pyQCall, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["call.name"].Text
		expr := m.Captures["call.expr"]
		callerID := findEnclosingFunc(funcRanges, expr.StartLine+1)
		if callerID == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::" + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: expr.StartLine + 1,
		})
	}

	matches, _ = parser.RunQuery(pyQCallAttr, e.lang, root, src)
	for _, m := range matches {
		method := m.Captures["call.method"].Text
		receiverText := m.Captures["call.receiver"].Text
		expr := m.Captures["call.expr"]
		callerID := findEnclosingFunc(funcRanges, expr.StartLine+1)
		if callerID == "" {
			continue
		}
		edge := &graph.Edge{
			From: callerID, To: "unresolved::*." + method,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: expr.StartLine + 1,
		}
		if recvType, ok := tenv[receiverText]; ok {
			edge.Meta = map[string]any{"receiver_type": recvType}
		}
		result.Edges = append(result.Edges, edge)
	}

	// Module-level variables (simple assignments at top level).
	matches, _ = parser.RunQuery(pyQAssignment, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["var.name"].Text
		def := m.Captures["var.def"]
		// Only top-level: parent is module.
		if def.Node != nil && def.Node.Parent() != nil && def.Node.Parent().Type() == "module" {
			id := filePath + "::" + name
			if seen[id] || strings.HasPrefix(name, "_") {
				continue
			}
			seen[id] = true
			result.Nodes = append(result.Nodes, &graph.Node{
				ID: id, Kind: graph.KindVariable, Name: name,
				FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
				Language: "python",
			})
			result.Edges = append(result.Edges, &graph.Edge{
				From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
			})
		}
	}

	return result, nil
}

// buildTypeEnv scans Python assignments for type annotations (Tier 0)
// and class-constructor calls (Tier 1) to build a variable->type map.
func (e *PythonExtractor) buildTypeEnv(root *sitter.Node, src []byte) typeEnv {
	tenv := make(typeEnv)

	// Tier 0: explicit type hints — x: Type = expr
	matches, _ := parser.RunQuery(pyQTypedAssignment, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["tvar.name"].Text
		typeName := normalizePyTypeName(m.Captures["tvar.type"].Text)
		if typeName != "" {
			tenv[name] = typeName
		}
	}

	// Tier 1: constructor calls — x = ClassName(...)
	// Convention: class names start with an uppercase letter.
	matches, _ = parser.RunQuery(pyQUntypedAssignment, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["uvar.name"].Text
		if _, exists := tenv[name]; exists {
			continue // Tier 0 already resolved
		}
		callee := m.Captures["uvar.callee"].Text
		if callee != "" && unicode.IsUpper(rune(callee[0])) {
			tenv[name] = callee
		}
	}

	return tenv
}

// normalizePyTypeName strips Optional[], List[], etc. and skips builtins.
func normalizePyTypeName(t string) string {
	t = strings.TrimSpace(t)
	// Strip Optional[...], List[...], etc.
	if idx := strings.Index(t, "["); idx > 0 {
		t = t[:idx]
	}
	switch t {
	case "int", "float", "str", "bool", "bytes", "None", "list", "dict", "set", "tuple", "object":
		return ""
	}
	if t == "" || (t[0] >= 'a' && t[0] <= 'z') {
		return ""
	}
	return t
}
