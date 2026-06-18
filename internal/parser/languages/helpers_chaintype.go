package languages

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Chained-receiver / factory-chain return-type resolution, shared by every
// AST extractor that records receiver types (Go, TypeScript, C#, Java, Kotlin,
// Python, Rust). Given the receiver text of a call, it infers the type the
// receiver expression evaluates to so the call edge can be stamped with an
// accurate receiver_type even when the receiver is itself a chain of calls.
//
// Two base shapes are seeded:
//
//   - a typed local/parameter variable — looked up in the per-function tenv;
//   - a static-factory / fluent-builder call — New().With(x).Build() — whose
//     leading segment is a free function (or constructor) whose declared
//     return type starts the walk. This is the factory-chain case: the base is
//     not a variable at all, so without it the whole chain stays untyped.
//
// Each subsequent segment advances through the prior segment's method
// return_type. Resolution stops (returns "") at the first hop it cannot type.

// resolveChainType walks a dotted/chained receiver expression text like
// `svc.GetUser().Save()` or a factory chain `New().Router()` and returns the
// inferred type of the final segment when each hop is typed — the first
// segment via tenv or, failing that, a factory function's return_type;
// subsequent segments via a method's return_type Meta. Returns "" on the first
// unresolvable hop.
func resolveChainType(expr string, tenv typeEnv, result *parser.ExtractionResult) string {
	cleaned := stripCallArgs(expr)

	parts := strings.Split(cleaned, ".")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}

	currentType, ok := tenv[parts[0]]
	if !ok || currentType == "" {
		// Factory chain: the base segment is not a typed variable but a free
		// function / constructor that was called (New(), createClient(), …).
		// Seed the walk from its declared return type so fluent builders and
		// static factories resolve like a typed receiver would.
		if baseIsCall(expr, parts[0]) {
			currentType = findFactoryReturnType(parts[0], result)
		}
	}
	if currentType == "" {
		return ""
	}

	for i := 1; i < len(parts); i++ {
		methodName := parts[i]
		returnType := findMethodReturnType(currentType, methodName, result)
		if returnType == "" {
			return ""
		}
		currentType = returnType
	}

	return currentType
}

// baseIsCall reports whether the leading segment of a receiver expression was
// itself invoked — i.e. the original text starts with `base(`. This keeps the
// factory-chain seed from firing for an ordinary `obj.method()` whose base is
// an (untyped) variable rather than a called factory.
func baseIsCall(expr, base string) bool {
	return strings.HasPrefix(strings.TrimSpace(expr), base+"(")
}

// findFactoryReturnType returns the declared return type of a free function or
// constructor named name — the seed for a static-factory / fluent chain. A
// receiver-less declaration (a true free function / constructor) wins; a
// same-named method is only a fallback so a package function shadows a method
// of the same name when both exist.
func findFactoryReturnType(name string, result *parser.ExtractionResult) string {
	var fallback string
	for _, n := range result.Nodes {
		if n.Kind != graph.KindFunction && n.Kind != graph.KindMethod {
			continue
		}
		if n.Name != name {
			continue
		}
		rt, ok := n.Meta["return_type"].(string)
		if !ok || rt == "" {
			continue
		}
		if _, hasRecv := n.Meta["receiver"]; !hasRecv {
			return rt
		}
		if fallback == "" {
			fallback = rt
		}
	}
	return fallback
}

// stripCallArgs removes balanced parentheses (and anything inside them)
// from a receiver expression so "svc.GetUser(arg).Save()" collapses to
// "svc.GetUser.Save" for chain walking.
func stripCallArgs(expr string) string {
	var b strings.Builder
	depth := 0
	for _, ch := range expr {
		switch ch {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(ch)
			}
		}
	}
	return b.String()
}

// findMethodReturnType scans result.Nodes for a method (or package-level
// function, for pkg.Func cases) with the given name on the given
// receiver type and returns its return_type Meta. Empty string when
// not found or unannotated.
func findMethodReturnType(receiverType, methodName string, result *parser.ExtractionResult) string {
	for _, n := range result.Nodes {
		if n.Kind != graph.KindMethod && n.Kind != graph.KindFunction {
			continue
		}
		if n.Name != methodName {
			continue
		}
		if recv, ok := n.Meta["receiver"].(string); ok && recv == receiverType {
			if rt, ok := n.Meta["return_type"].(string); ok {
				return rt
			}
		}
	}
	return ""
}
