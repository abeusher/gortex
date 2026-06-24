package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Rust route extraction for the builder/chained forms the per-line httpPatterns
// table cannot express:
//
//   - chained Axum methods: `.route("/u", get(list).post(create))` — one
//     Contract per HTTP method, each bound to its own handler;
//   - the Actix builder: `web::resource("/u").route(web::get().to(list))`,
//     with `web::scope("/api")` prefixing the resources nested inside it.
//
// Axum `.nest("/api", router)` prefixing is already handled by route_prefix.go
// (framework "axum"); Actix scopes nest resources inline, so the prefix is
// joined here from the enclosing-call region of each `web::scope`.

var (
	// rustAxumRouteHeadRE matches the start of an axum `.route("/path", …)`
	// call. Group 1 is the path; the builder chain follows the comma.
	rustAxumRouteHeadRE = regexp.MustCompile(`\.route\(\s*"([^"]+)"\s*,`)
	// rustMethodHandlerRE matches a `method(handler)` in an axum builder
	// chain — `get(list)`, `post(create)`.
	rustMethodHandlerRE = regexp.MustCompile(`\b(get|post|put|delete|patch|head|options)\s*\(\s*(\w+)`)
	// rustActixResourceRE matches `web::resource("/path")`.
	rustActixResourceRE = regexp.MustCompile(`web::resource\(\s*"([^"]+)"\s*\)`)
	// rustActixRouteRE matches `.route(web::get().to(handler))` /
	// `.to_async(handler)` inside an Actix resource chain.
	rustActixRouteRE = regexp.MustCompile(`\.route\(\s*web::(get|post|put|delete|patch|head|options)\(\)\s*\.to(?:_async)?\(\s*(\w+)`)
	// rustActixScopeRE matches `web::scope("/prefix")`.
	rustActixScopeRE = regexp.MustCompile(`web::scope\(\s*"([^"]+)"\s*\)`)
)

// extractRustRoutes emits the chained-Axum and Actix-builder route Contracts.
func (h *HTTPExtractor) extractRustRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	out = append(out, h.extractAxumChainedRoutes(filePath, text, lines, fileNodes, lang, tree)...)
	out = append(out, h.extractActixBuilderRoutes(filePath, text, lines, fileNodes, lang, tree)...)
	return out
}

// extractAxumChainedRoutes scans every `.route("/path", <chain>)` for each
// `method(handler)` in its builder chain, emitting one Contract per method.
func (h *HTTPExtractor) extractAxumChainedRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for _, head := range rustAxumRouteHeadRE.FindAllStringSubmatchIndex(text, -1) {
		path := text[head[2]:head[3]]
		open := strings.IndexByte(text[head[0]:], '(')
		if open < 0 {
			continue
		}
		open += head[0]
		closeIdx := rustMatchingParen(text, open)
		if closeIdx < 0 || head[1] > closeIdx {
			continue
		}
		chain := text[head[1]:closeIdx]
		lineNum := lineAtOffset(lines, head[0])
		seen := map[string]bool{}
		for _, mh := range rustMethodHandlerRE.FindAllStringSubmatchIndex(chain, -1) {
			method := strings.ToUpper(chain[mh[2]:mh[3]])
			handler := chain[mh[4]:mh[5]]
			if seen[method] {
				continue
			}
			seen[method] = true
			out = append(out, h.buildRustRoute(filePath, lines, fileNodes, lang, tree, method, path, handler, lineNum, "axum"))
		}
	}
	return out
}

// extractActixBuilderRoutes emits one Contract per `.route(web::method().to(h))`
// of every `web::resource("/path")`, joining any enclosing `web::scope` prefix.
func (h *HTTPExtractor) extractActixBuilderRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	scopes := rustActixScopes(text)
	var out []Contract
	locs := rustActixResourceRE.FindAllStringSubmatchIndex(text, -1)
	for i, loc := range locs {
		path := rustJoinPath(rustScopePrefix(scopes, loc[0]), text[loc[2]:loc[3]])
		regionEnd := len(text)
		if i+1 < len(locs) {
			regionEnd = locs[i+1][0]
		}
		region := text[loc[1]:regionEnd]
		lineNum := lineAtOffset(lines, loc[0])
		for _, mh := range rustActixRouteRE.FindAllStringSubmatch(region, -1) {
			method := strings.ToUpper(mh[1])
			out = append(out, h.buildRustRoute(filePath, lines, fileNodes, lang, tree, method, path, mh[2], lineNum, "actix"))
		}
	}
	return out
}

// buildRustRoute assembles a route Contract, binding the handler identifier to
// its same-file function and running the body schema enricher.
func (h *HTTPExtractor) buildRustRoute(filePath string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree, method, path, handler string, lineNum int, framework string) Contract {
	normPath, origNames := NormalizeHTTPPathWithParams(path)
	meta := map[string]any{"method": method, "path": normPath, "framework": framework}
	if len(origNames) > 0 {
		meta["path_param_names"] = origNames
	}
	symbolID := findEnclosingSymbol(fileNodes, lineNum)
	if handler != "" {
		meta["handler_ident"] = handler
		if hID := resolveHandlerIdent(fileNodes, handler); hID != "" {
			symbolID = hID
		}
	}
	c := Contract{
		ID:         fmt.Sprintf("http::%s::%s", method, normPath),
		Type:       ContractHTTP,
		Role:       RoleProvider,
		SymbolID:   symbolID,
		FilePath:   filePath,
		Line:       lineNum,
		Meta:       meta,
		Confidence: 0.9,
	}
	EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
	return c
}

// rustScopeRegion is one `web::scope("/prefix")` and the text span its nested
// resources occupy (the enclosing call's balanced region).
type rustScopeRegion struct {
	start, end int
	prefix     string
}

// rustActixScopes locates every `web::scope("/prefix")` and the text region of
// the call that encloses it, so resources inside that region inherit the prefix.
func rustActixScopes(text string) []rustScopeRegion {
	var out []rustScopeRegion
	for _, m := range rustActixScopeRE.FindAllStringSubmatchIndex(text, -1) {
		out = append(out, rustScopeRegion{
			start:  m[0],
			end:    rustEnclosingClose(text, m[0]),
			prefix: text[m[2]:m[3]],
		})
	}
	return out
}

// rustScopePrefix returns the concatenated prefixes of every scope region that
// contains pos, outermost first.
func rustScopePrefix(scopes []rustScopeRegion, pos int) string {
	var b strings.Builder
	for _, s := range scopes {
		if pos > s.start && pos < s.end {
			b.WriteString(s.prefix)
		}
	}
	return b.String()
}

// rustEnclosingClose returns the index of the close paren of the call that
// wraps the scope at scopeStart (the `(` immediately preceding it), or
// len(text) when the scope is not an argument to a wrapping call.
func rustEnclosingClose(text string, scopeStart int) int {
	i := scopeStart - 1
	for i >= 0 && (text[i] == ' ' || text[i] == '\t' || text[i] == '\n' || text[i] == '\r') {
		i--
	}
	if i < 0 || text[i] != '(' {
		return len(text)
	}
	if c := rustMatchingParen(text, i); c >= 0 {
		return c
	}
	return len(text)
}

// rustMatchingParen returns the index of the close paren matching the open
// paren at index open, or -1.
func rustMatchingParen(text string, open int) int {
	depth := 0
	for j := open; j < len(text); j++ {
		switch text[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// rustJoinPath concatenates a scope prefix and a resource path, collapsing the
// boundary slash.
func rustJoinPath(prefix, path string) string {
	if prefix == "" {
		return path
	}
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}
