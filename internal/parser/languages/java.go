package languages

import (
	"fmt"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

const (
	javaQClass = `(class_declaration
		name: (identifier) @class.name) @class.def`

	javaQInterface = `(interface_declaration
		name: (identifier) @iface.name) @iface.def`

	javaQMethod = `(method_declaration
		name: (identifier) @method.name) @method.def`

	javaQClassMethod = `(class_declaration
		name: (identifier) @class.name
		body: (class_body
			(method_declaration
				name: (identifier) @method.name) @method.def))`

	javaQConstructor = `(constructor_declaration
		name: (identifier) @ctor.name) @ctor.def`

	javaQClassConstructor = `(class_declaration
		name: (identifier) @class.name
		body: (class_body
			(constructor_declaration
				name: (identifier) @ctor.name) @ctor.def))`

	javaQImport = `(import_declaration
		(scoped_identifier) @import.path) @import.def`

	javaQCall = `(method_invocation
		name: (identifier) @call.name) @call.expr`

	javaQCallMember = `(method_invocation
		object: (_) @call.receiver
		name: (identifier) @call.method) @call.expr`

	javaQClassField = `(class_declaration
		name: (identifier) @class.name
		body: (class_body
			(field_declaration
				declarator: (variable_declarator
					name: (identifier) @field.name)) @field.def))`

	javaQIfaceMethod = `(interface_declaration
		name: (identifier) @iface.name
		body: (interface_body
			(method_declaration
				name: (identifier) @iface.method.name)))`

	// Tier 0: explicit local variable declarations — Type varName = ...
	javaQLocalVar = `(local_variable_declaration
		type: (_) @lvar.type
		declarator: (variable_declarator
			name: (identifier) @lvar.name)) @lvar.def`

	// Tier 0: explicit field declarations — Type fieldName = ...
	javaQFieldVar = `(field_declaration
		type: (_) @fvar.type
		declarator: (variable_declarator
			name: (identifier) @fvar.name)) @fvar.def`
)

// JavaExtractor extracts Java source files.
type JavaExtractor struct {
	lang *sitter.Language
}

func NewJavaExtractor() *JavaExtractor {
	return &JavaExtractor{lang: java.GetLanguage()}
}

func (e *JavaExtractor) Language() string     { return "java" }
func (e *JavaExtractor) Extensions() []string { return []string{".java"} }

func (e *JavaExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
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
		Language: "java",
	}
	result.Nodes = append(result.Nodes, fileNode)

	seen := make(map[string]bool)

	// Classes.
	matches, _ := parser.RunQuery(javaQClass, e.lang, root, src)
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
			Language: "java",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Interfaces.
	matches, _ = parser.RunQuery(javaQInterface, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["iface.name"].Text
		def := m.Captures["iface.def"]
		id := filePath + "::" + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindInterface, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Extract interface method names into Meta["methods"] for IMPLEMENTS inference.
	ifaceMethodMatches, _ := parser.RunQuery(javaQIfaceMethod, e.lang, root, src)
	ifaceMethods := make(map[string][]string)
	for _, m := range ifaceMethodMatches {
		ifaceName := m.Captures["iface.name"].Text
		methodName := m.Captures["iface.method.name"].Text
		ifaceMethods[ifaceName] = append(ifaceMethods[ifaceName], methodName)
	}
	for _, n := range result.Nodes {
		if n.Kind == graph.KindInterface {
			name := n.Name
			if methods, ok := ifaceMethods[name]; ok {
				if n.Meta == nil {
					n.Meta = make(map[string]any)
				}
				n.Meta["methods"] = methods
			}
		}
	}

	// Methods (with class membership).
	matches, _ = parser.RunQuery(javaQClassMethod, e.lang, root, src)
	for _, m := range matches {
		className := m.Captures["class.name"].Text
		name := m.Captures["method.name"].Text
		def := m.Captures["method.def"]
		id := filePath + "::" + className + "." + name
		if seen[id] {
			// Methods can share names (overloads), disambiguate by line.
			id = filePath + "::" + className + "." + name + "_L" + fmt.Sprint(def.StartLine+1)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		// Mark line so fallback query skips this method.
		seen[filePath+"::_method_L"+fmt.Sprint(def.StartLine+1)] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
			Meta:     map[string]any{"receiver": className},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
		// MemberOf edge to containing class.
		classID := filePath + "::" + className
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: classID, Kind: graph.EdgeMemberOf, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Fallback: methods not inside a class declaration.
	// Track lines already covered by class-scoped methods to avoid duplicates.
	matches, _ = parser.RunQuery(javaQMethod, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["method.name"].Text
		def := m.Captures["method.def"]
		lineKey := filePath + "::_method_L" + fmt.Sprint(def.StartLine+1)
		if seen[lineKey] {
			continue
		}
		id := filePath + "::" + name
		if seen[id] {
			id = filePath + "::" + name + "_L" + fmt.Sprint(def.StartLine+1)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Constructors (with class membership).
	matches, _ = parser.RunQuery(javaQClassConstructor, e.lang, root, src)
	for _, m := range matches {
		className := m.Captures["class.name"].Text
		def := m.Captures["ctor.def"]
		id := filePath + "::" + className + ".<init>"
		if seen[id] {
			// Multiple constructors (overloads), disambiguate by line.
			id = filePath + "::" + className + ".<init>_L" + fmt.Sprint(def.StartLine+1)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		// Mark line so fallback query skips this constructor.
		seen[filePath+"::_ctor_L"+fmt.Sprint(def.StartLine+1)] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: className + ".<init>",
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
			Meta:     map[string]any{"receiver": className},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
		// MemberOf edge to containing class.
		classID := filePath + "::" + className
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: classID, Kind: graph.EdgeMemberOf, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Fallback: constructors not matched by class-scoped query.
	matches, _ = parser.RunQuery(javaQConstructor, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["ctor.name"].Text
		def := m.Captures["ctor.def"]
		lineKey := filePath + "::_ctor_L" + fmt.Sprint(def.StartLine+1)
		if seen[lineKey] {
			continue
		}
		id := filePath + "::" + name + ".<init>"
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindMethod, Name: name + ".<init>",
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Fields (with class membership).
	matches, _ = parser.RunQuery(javaQClassField, e.lang, root, src)
	for _, m := range matches {
		className := m.Captures["class.name"].Text
		name := m.Captures["field.name"].Text
		def := m.Captures["field.def"]
		id := filePath + "::" + className + "." + name
		if seen[id] {
			continue
		}
		seen[id] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: id, Kind: graph.KindVariable, Name: name,
			FilePath: filePath, StartLine: def.StartLine + 1, EndLine: def.EndLine + 1,
			Language: "java",
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: id, Kind: graph.EdgeDefines, FilePath: filePath, Line: def.StartLine + 1,
		})
		classID := filePath + "::" + className
		result.Edges = append(result.Edges, &graph.Edge{
			From: id, To: classID, Kind: graph.EdgeMemberOf, FilePath: filePath, Line: def.StartLine + 1,
		})
	}

	// Imports.
	matches, _ = parser.RunQuery(javaQImport, e.lang, root, src)
	for _, m := range matches {
		path := m.Captures["import.path"]
		importPath := strings.ReplaceAll(path.Text, ".", "/")
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: "unresolved::import::" + importPath,
			Kind: graph.EdgeImports, FilePath: filePath, Line: path.StartLine + 1,
		})
	}

	// Build type environment for receiver type inference.
	tenv := e.buildTypeEnv(root, src)

	// Call sites (with type env).
	e.extractCalls(root, src, filePath, result, tenv)

	return result, nil
}

// extractCalls extracts plain and member method calls with type-env-aware receiver resolution.
func (e *JavaExtractor) extractCalls(root *sitter.Node, src []byte, filePath string, result *parser.ExtractionResult, tenv typeEnv) {
	funcRanges := buildFuncRanges(result)

	// Plain method calls (no receiver): methodName(...)
	matches, _ := parser.RunQuery(javaQCall, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["call.name"].Text
		expr := m.Captures["call.expr"]
		callerID := findEnclosingFunc(funcRanges, expr.StartLine+1)
		if callerID == "" {
			continue
		}
		result.Edges = append(result.Edges, &graph.Edge{
			From: callerID, To: "unresolved::*." + name,
			Kind: graph.EdgeCalls, FilePath: filePath, Line: expr.StartLine + 1,
		})
	}

	// Member method calls: receiver.methodName(...)
	matches, _ = parser.RunQuery(javaQCallMember, e.lang, root, src)
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
}

// buildTypeEnv scans Java variable declarations to build a variable-to-type map.
// Tier 0: explicit type declarations (Type varName = ...)
// Tier 1: new expressions (var inferred from object_creation_expression)
func (e *JavaExtractor) buildTypeEnv(root *sitter.Node, src []byte) typeEnv {
	tenv := make(typeEnv)

	// Tier 0: local variable declarations — User user = ...
	matches, _ := parser.RunQuery(javaQLocalVar, e.lang, root, src)
	for _, m := range matches {
		name := m.Captures["lvar.name"].Text
		typeName := normalizeJavaTypeName(m.Captures["lvar.type"].Text)
		if typeName != "" {
			tenv[name] = typeName
		}
	}

	// Tier 0: field declarations — private User user = ...
	fieldMatches, _ := parser.RunQuery(javaQFieldVar, e.lang, root, src)
	for _, m := range fieldMatches {
		name := m.Captures["fvar.name"].Text
		if _, exists := tenv[name]; exists {
			continue
		}
		typeName := normalizeJavaTypeName(m.Captures["fvar.type"].Text)
		if typeName != "" {
			tenv[name] = typeName
		}
	}

	// Tier 1: for local vars where explicit type didn't yield a name
	// (e.g., Java 10+ var keyword), try to infer from new expression.
	for _, m := range matches {
		name := m.Captures["lvar.name"].Text
		if _, exists := tenv[name]; exists {
			continue // already have explicit type
		}
		defNode := m.Captures["lvar.def"].Node
		if defNode == nil {
			continue
		}
		walkNodes(defNode, func(n *sitter.Node) {
			if n.Type() == "object_creation_expression" {
				typeName := inferTypeFromJavaNewExpr(n, src)
				if typeName != "" {
					tenv[name] = typeName
				}
			}
		})
	}

	return tenv
}

// normalizeJavaTypeName strips generics and array markers from a Java type name.
// "User" -> "User", "List<User>" -> "List", "User[]" -> "User"
func normalizeJavaTypeName(t string) string {
	t = strings.TrimSpace(t)
	// Remove array suffix.
	t = strings.TrimSuffix(t, "[]")
	// Remove generics.
	if idx := strings.Index(t, "<"); idx > 0 {
		t = t[:idx]
	}
	// Skip Java primitives and common non-class types.
	switch t {
	case "int", "long", "short", "byte", "float", "double", "boolean", "char", "void", "var", "String":
		return ""
	}
	if t == "" || (t[0] >= 'a' && t[0] <= 'z') {
		return "" // skip lowercase type names (primitives)
	}
	return t
}

// inferTypeFromJavaNewExpr extracts the class name from an object_creation_expression node.
// new User(...) -> "User", new ArrayList<String>() -> "ArrayList"
func inferTypeFromJavaNewExpr(node *sitter.Node, src []byte) string {
	for i := 0; i < int(node.NamedChildCount()); i++ {
		child := node.NamedChild(i)
		if child.Type() == "type_identifier" {
			name := child.Content(src)
			if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
				return name
			}
		}
	}
	return ""
}
