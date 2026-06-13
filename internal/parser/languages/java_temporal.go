package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
)

// javaTemporalStartWorkflowName returns the workflow TYPE name a Temporal
// Java workflow-stub creation starts, or "". It recognises the two stub
// factory shapes:
//
//	client.newWorkflowStub(OrderWorkflow.class, options)   // typed   → "OrderWorkflow"
//	client.newUntypedWorkflowStub("OrderWorkflow")         // untyped → "OrderWorkflow"
//
// The stub's @WorkflowMethod call actually triggers the start, but the
// type (the class literal / string) is the canonical workflow name, which
// the resolver cross-resolves to the registered workflow — whose
// implementation may live in a Go repo. A `Foo.class` argument is reduced
// to its simple name ("Foo"), matching the Java SDK's default workflow
// type and the name a Go RegisterWorkflow would use.
func javaTemporalStartWorkflowName(callNode *sitter.Node, method string, src []byte) string {
	switch method {
	case "newWorkflowStub", "newUntypedWorkflowStub":
	default:
		return ""
	}
	if callNode == nil {
		return ""
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return ""
	}
	var first *sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c != nil {
			first = c
			break
		}
	}
	if first == nil {
		return ""
	}
	text := first.Content(src)
	// `OrderWorkflow.class` / `com.example.OrderWorkflow.class` — robust to
	// the grammar representing the class literal as a class_literal or a
	// field_access by matching the trailing `.class`.
	if strings.HasSuffix(text, ".class") {
		return javaSimpleTypeName(strings.TrimSuffix(text, ".class"))
	}
	// `"OrderWorkflow"` — an untyped stub names the workflow by string.
	if first.Type() == "string_literal" {
		return strings.Trim(text, `"`)
	}
	return ""
}

// javaSimpleTypeName returns the trailing identifier of a possibly
// qualified Java type name (`com.example.Foo` → `Foo`).
func javaSimpleTypeName(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// javaTemporalSignalQuery recognises an outbound signal-send / query-call
// on an untyped Temporal WorkflowStub and returns its kind ("signal" /
// "query") and the signal/query name (the first positional argument, a
// string literal). The call shapes are:
//
//	stub.signal("signalName", arg)              // WorkflowStub.signal
//	stub.query("queryType", ResultClass, arg)   // WorkflowStub.query
//
// "signal" / "query" are ordinary method names, so the caller gates the
// match on the receiver's inferred type being WorkflowStub to stay
// precise. Returns ("", "") when the method is not signal/query or the
// name is not a string literal.
func javaTemporalSignalQuery(callNode *sitter.Node, method string, src []byte) (kind, name string) {
	switch method {
	case "signal":
		kind = "signal"
	case "query":
		kind = "query"
	default:
		return "", ""
	}
	if callNode == nil {
		return "", ""
	}
	args := callNode.ChildByFieldName("arguments")
	if args == nil {
		return "", ""
	}
	var first *sitter.Node
	for i := 0; i < int(args.NamedChildCount()); i++ {
		if c := args.NamedChild(i); c != nil {
			first = c
			break
		}
	}
	if first == nil || first.Type() != "string_literal" {
		return "", ""
	}
	return kind, strings.Trim(first.Content(src), `"`)
}

// Java Temporal invoker detection.
//
// PURPOSE: some Java Temporal codebases dispatch through a custom invoker
// wrapper (`invoker.invokeAsync("ProcessOrderWorkflow", opts, input)`) instead
// of annotated `@WorkflowInterface` proxies — analogous to Go's
// `workflow.ExecuteActivity(ctx, name, …)`. Those call sites are invisible to
// the annotation-based detector. This emits the SAME `via=temporal.stub` edge
// the Go extractor emits, so the resolver lands it on the Go workflow of that
// name (cross-language) and the three-layer trust system applies unchanged.
//
// RATIONALE: invoker class names are a per-repo corporate convention, so the
// detector is OFF until configured via ConfigureTemporalJavaInvokers. Precision-
// first: a call whose receiver type can't be resolved to a configured invoker,
// or whose name can't be extracted, is left to the generic call path (false
// negatives acceptable, false positives not).
//
// KEYWORDS: temporal, java, invoker, stub, cross-language

// javaInvokerDefaultMethods is the built-in set of invoker dispatch methods.
// `signal` is deliberately absent — a signal targets a RUNNING workflow, it is
// not a dispatch (no new workflow is started).
var javaInvokerDefaultMethods = map[string]bool{
	"invokeAsync":     true,
	"invokeSync":      true,
	"signalWithStart": true,
}

// javaInvokerKind maps a known invoker method to the dispatch kind, the 0-based
// argument index carrying the workflow/activity name, and (for signalWithStart)
// the index of the signal-name argument (-1 when none).
func javaInvokerKind(method string) (kind string, namePos int, signalNamePos int, ok bool) {
	switch method {
	case "invokeAsync", "invokeSync":
		return "workflow", 0, -1, true
	case "signalWithStart":
		return "workflow", 2, 0, true
	}
	return "", 0, -1, false
}

// isInvokerMethod reports whether name is a configured invoker dispatch method
// (the per-repo override set when present, else the built-in defaults).
func (e *JavaExtractor) isInvokerMethod(name string) bool {
	if e.javaInvokerMethods != nil {
		return e.javaInvokerMethods[name]
	}
	return javaInvokerDefaultMethods[name]
}

// emitJavaTemporalInvoker recognises a Temporal dispatch through a configured
// invoker and emits a via=temporal.stub EdgeCalls edge the resolver lands on the
// Go workflow of that name. Returns true when a stub was emitted (the caller
// then skips the generic call edge).
func (e *JavaExtractor) emitJavaTemporalInvoker(c javaDeferredCall, callerID string, tenv typeEnv, filePath string, src []byte, result *parser.ExtractionResult) bool {
	if len(e.javaInvokers) == 0 || !c.isSelector || c.callNode == nil {
		return false
	}
	if !e.isInvokerMethod(c.name) {
		return false
	}
	recvType, ok := tenv[c.receiver]
	if !ok || !e.javaInvokers[simpleJavaTypeName(recvType)] {
		return false
	}
	kind, namePos, signalNamePos, ok := javaInvokerKind(c.name)
	if !ok {
		// Config-provided method not in the known layout map: assume the
		// conventional workflow-dispatch shape (name is the first argument).
		kind, namePos, signalNamePos = "workflow", 0, -1
	}
	args := c.callNode.ChildByFieldName("arguments")
	name, source, envKey, ok := javaInvokerDispatchName(javaArgAt(args, namePos), src)
	if !ok || name == "" {
		return false
	}

	meta := map[string]any{
		"via":            "temporal.stub",
		"temporal_kind":  kind,
		"temporal_name":  name,
		"cross_language": true,
	}
	switch source {
	case "const_ref":
		// The name IS a constant reference; the resolver substitutes its
		// literal value via constVal (like a bare ALL_CAPS Go dispatch arg).
		meta["temporal_name_origin"] = "env_default"
		meta["temporal_env_source"] = "const_ref"
	case "heuristic", "variable":
		meta["temporal_name_origin"] = "env_default"
		meta["temporal_env_source"] = "heuristic"
		if envKey != "" {
			meta["temporal_env_key"] = envKey
		}
	default: // "exact" — a string literal; resolves at the register tier.
	}
	if signalNamePos >= 0 {
		if sn, _, _, okSig := javaInvokerDispatchName(javaArgAt(args, signalNamePos), src); okSig && sn != "" {
			meta["temporal_signal_name"] = sn
		}
	}
	result.Edges = append(result.Edges, &graph.Edge{
		From:     callerID,
		To:       temporalJavaStubPlaceholder(kind, name),
		Kind:     graph.EdgeCalls,
		FilePath: filePath,
		Line:     c.line,
		Origin:   graph.OriginASTResolved,
		Meta:     meta,
	})
	return true
}

// temporalJavaStubPlaceholder mirrors the Go-side placeholder format exactly, so
// the resolver's stub loop (keyed on via=temporal.stub + temporal_name) treats
// Java and Go stubs identically.
func temporalJavaStubPlaceholder(kind, name string) string {
	return "unresolved::temporal::" + kind + "::" + name
}

// javaInvokerDispatchName extracts the workflow/activity name from an invoker
// argument, by descending priority: string literal (exact) → env-property read
// with a literal default (heuristic) → constant reference (const_ref) → bare
// variable (heuristic, unresolvable). Returns the name, its source tier marker,
// and (for env reads) the env key.
func javaInvokerDispatchName(arg *sitter.Node, src []byte) (name, source, envKey string, ok bool) {
	if arg == nil {
		return "", "", "", false
	}
	switch arg.Type() {
	case "string_literal":
		if v := javaStringLiteralText(arg, src); v != "" {
			return v, "exact", "", true
		}
	case "field_access":
		// Constants.WF_NAME → trailing field → constant reference.
		if f := arg.ChildByFieldName("field"); f != nil {
			return f.Content(src), "const_ref", "", true
		}
	case "identifier":
		n := arg.Content(src)
		if isJavaConstName(n) {
			return n, "const_ref", "", true
		}
		return n, "variable", "", true
	case "method_invocation":
		return javaEnvPropertyName(arg, src)
	}
	return "", "", "", false
}

// javaEnvPropertyName recognises an env / config read with a literal default
// (`env.getProperty("key", "Default")`, `getRequiredProperty("key")`) and
// returns the literal default (or the key when no default), source=heuristic.
func javaEnvPropertyName(call *sitter.Node, src []byte) (name, source, envKey string, ok bool) {
	mname := ""
	if nf := call.ChildByFieldName("name"); nf != nil {
		mname = nf.Content(src)
	}
	switch mname {
	case "getProperty", "getRequiredProperty", "getEnv", "getOrDefault", "getString":
	default:
		return "", "", "", false
	}
	args := call.ChildByFieldName("arguments")
	key := javaStringArgAt(args, 0, src)
	def := javaStringArgAt(args, 1, src)
	if def != "" {
		return def, "heuristic", key, true
	}
	if key != "" {
		return key, "heuristic", key, true
	}
	return "", "", "", false
}

// javaArgAt returns the pos-th (0-based) argument node of an argument_list.
func javaArgAt(args *sitter.Node, pos int) *sitter.Node {
	if args == nil || pos < 0 || pos >= int(args.NamedChildCount()) {
		return nil
	}
	return args.NamedChild(pos)
}

// javaStringArgAt returns the pos-th argument's string-literal value, or "".
func javaStringArgAt(args *sitter.Node, pos int, src []byte) string {
	a := javaArgAt(args, pos)
	if a == nil || a.Type() != "string_literal" {
		return ""
	}
	return javaStringLiteralText(a, src)
}

// javaStringLiteralText returns a Java string literal's value, stripping the
// surrounding double quotes. Escapes are not decoded — dispatch names don't use
// them.
func javaStringLiteralText(n *sitter.Node, src []byte) string {
	t := n.Content(src)
	if len(t) >= 2 && t[0] == '"' && t[len(t)-1] == '"' {
		return t[1 : len(t)-1]
	}
	return t
}

// isJavaConstName reports whether an identifier follows Java's ALL_CAPS constant
// convention (upper-case letters, optionally with digits / underscores).
func isJavaConstName(s string) bool {
	if s == "" {
		return false
	}
	hasLetter := false
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			hasLetter = true
		case (r >= '0' && r <= '9') || r == '_':
		default:
			return false
		}
	}
	return hasLetter
}

// simpleJavaTypeName reduces a (possibly qualified / generic) type name to its
// simple class name: `com.x.Invoker<T>` → `Invoker`.
func simpleJavaTypeName(t string) string {
	if i := strings.IndexByte(t, '<'); i >= 0 {
		t = t[:i]
	}
	if i := strings.LastIndexByte(t, '.'); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSpace(t)
}
