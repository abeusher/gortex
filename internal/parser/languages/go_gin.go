package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

var (
	// ginDispatchRe matches the Gin middleware-chain dispatcher: a method that
	// invokes the next handler by indexing a handlers slice — `c.handlers[c.index](c)`
	// / `engine.handlers[i](ctx)`. The indexed-call is the indirection static
	// analysis cannot follow; the method that contains it is the chain
	// dispatcher every request flows through.
	ginDispatchRe = regexp.MustCompile(`\bhandlers\s*\[[^\]]+\]\s*\(`)
	// ginRegisterRe matches a Gin route/middleware registration verb on a
	// router or group — `.GET(`, `.Use(`, `.Handle(`, `.Any(`. The handler
	// identifiers are the call's non-string, non-closure arguments.
	ginRegisterRe = regexp.MustCompile(`\.(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|Any|Match|Handle|Use)\s*\(`)
)

// mineGinMiddleware stamps the Gin middleware-chain dispatcher and route
// registrations so the resolver's SynthGinMiddleware pass can bridge the
// dispatcher to the handlers it dispatches to (an edge static analysis drops at
// the `handlers[idx](c)` indirection). The dispatcher method gets
// gin_dispatcher=true; each registration's enclosing function accumulates the
// registered handler names in gin_handlers. The two are paired across files by
// the resolver, gated on a dispatcher existing — so this is inert in a repo
// that does not embed a Gin-style chain.
func mineGinMiddleware(src []byte, result *parser.ExtractionResult) {
	hasDispatch := ginDispatchRe.Match(src)
	hasRegister := ginRegisterRe.Match(src)
	if !hasDispatch && !hasRegister {
		return
	}
	funcRanges := buildFuncRanges(result)
	nodeByID := map[string]*graph.Node{}
	for _, n := range result.Nodes {
		if n.Kind == graph.KindMethod || n.Kind == graph.KindFunction {
			nodeByID[n.ID] = n
		}
	}

	if hasDispatch {
		for _, m := range ginDispatchRe.FindAllIndex(src, -1) {
			n := nodeByID[findEnclosingFunc(funcRanges, lineAt(src, m[0]))]
			if n == nil {
				continue
			}
			if n.Meta == nil {
				n.Meta = map[string]any{}
			}
			n.Meta["gin_dispatcher"] = true
		}
	}

	if hasRegister {
		for _, m := range ginRegisterRe.FindAllIndex(src, -1) {
			// m[1] points just past the matched verb + '('; balance-parse the
			// call's arguments from there.
			args := goBalancedCallArgs(src, m[1]-1)
			names := ginHandlerIdents(args)
			if len(names) == 0 {
				continue
			}
			n := nodeByID[findEnclosingFunc(funcRanges, lineAt(src, m[0]))]
			if n == nil {
				continue
			}
			if n.Meta == nil {
				n.Meta = map[string]any{}
			}
			existing, _ := n.Meta["gin_handlers"].([]string)
			n.Meta["gin_handlers"] = dedupAppend(existing, names)
		}
	}
}

// goBalancedCallArgs returns the source between the '(' at openParenIdx and its
// matching ')', skipping string/rune/raw literals so a paren inside a string is
// ignored. Empty when the call cannot be balanced.
func goBalancedCallArgs(src []byte, openParenIdx int) string {
	if openParenIdx < 0 || openParenIdx >= len(src) || src[openParenIdx] != '(' {
		return ""
	}
	depth := 0
	i := openParenIdx
	for i < len(src) {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return string(src[openParenIdx+1 : i])
			}
		case '"', '\'', '`':
			q := src[i]
			i++
			for i < len(src) && src[i] != q {
				if src[i] == '\\' && q != '`' {
					i++
				}
				i++
			}
		}
		i++
	}
	return ""
}

// ginHandlerIdents extracts the handler identifiers from a registration call's
// argument list: the named functions passed as middleware/handlers. String
// literals (the route path) and inline `func(...)` closures are dropped; a
// `pkg.Handler` / `h.Method` reference is reduced to its final identifier
// (the name the resolver binds against). A trailing `()` factory call
// (`Logger()`) keeps the factory name.
func ginHandlerIdents(args string) []string {
	var out []string
	for _, part := range splitTopLevelCommas(args) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Drop a string-literal path argument and inline closures.
		if part[0] == '"' || part[0] == '`' || strings.HasPrefix(part, "func") {
			continue
		}
		// Reduce `pkg.Handler` / `recv.Method` to the final identifier, and
		// strip a trailing factory call's parens.
		if i := strings.IndexByte(part, '('); i >= 0 {
			part = part[:i]
		}
		if i := strings.LastIndexByte(part, '.'); i >= 0 {
			part = part[i+1:]
		}
		part = strings.TrimSpace(part)
		if part != "" && isGoIdent(part) {
			out = append(out, part)
		}
	}
	return out
}

func isGoIdent(s string) bool {
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (i > 0 && b >= '0' && b <= '9') {
			continue
		}
		return false
	}
	return len(s) > 0
}

func dedupAppend(existing, add []string) []string {
	seen := map[string]bool{}
	for _, s := range existing {
		seen[s] = true
	}
	for _, s := range add {
		if !seen[s] {
			seen[s] = true
			existing = append(existing, s)
		}
	}
	return existing
}
