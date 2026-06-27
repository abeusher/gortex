package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// C# ASP.NET attribute-routing prefix-join. A controller's class-level
// `[Route("api/[controller]")]` is the shared prefix of every action route, and
// a method may declare a verb-less `[Route("search")]` (a route with no HTTP-verb
// constraint). The per-line httpPatterns scan sees method routes in isolation, so
// this pass supplies the class context: it joins the controller prefix onto each
// RELATIVE method template (an absolute `/...` or `~/...` template ignores the
// prefix, per ASP.NET) and materialises a contract for each verb-less route.

var (
	csharpRouteAttrRe    = regexp.MustCompile(`\[Route\(\s*"([^"]+)"\s*\)\]`)
	csharpHTTPVerbAttrRe = regexp.MustCompile(`\[Http(?:Get|Post|Put|Delete|Patch|Head|Options)\b`)
	csharpClassDeclRe    = regexp.MustCompile(`\bclass\s+(\w+)`)
)

// csController is a C# controller class's resolved class-level route prefix and
// line span, used to prefix-join its method routes.
type csController struct {
	prefix    string
	startLine int
	endLine   int
}

// csVerblessRoute is a method-level `[Route("...")]` with no HTTP-verb attribute
// in the same attribute block -- an ASP.NET route whose verb is unconstrained.
type csVerblessRoute struct {
	template string
	line     int
}

// csharpScanControllerRoutes walks the C# source once, returning each
// controller class's class-level [Route(...)] prefix (with [controller]
// expanded) and each method-level verb-less [Route("...")] site.
func csharpScanControllerRoutes(lines []string, fileNodes []*graph.Node) ([]csController, []csVerblessRoute) {
	var controllers []csController
	var verbless []csVerblessRoute
	pendingRoute := ""
	pendingVerb := false
	blockLine := -1
	reset := func() { pendingRoute, pendingVerb, blockLine = "", false, -1 }
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if m := csharpRouteAttrRe.FindStringSubmatch(line); m != nil {
			pendingRoute = m[1]
			if blockLine < 0 {
				blockLine = i
			}
			continue
		}
		if csharpHTTPVerbAttrRe.MatchString(line) {
			pendingVerb = true
			if blockLine < 0 {
				blockLine = i
			}
			continue
		}
		if strings.HasPrefix(line, "[") {
			if blockLine < 0 {
				blockLine = i
			}
			continue
		}
		// A declaration (or other code) terminates the attribute block.
		if cm := csharpClassDeclRe.FindStringSubmatch(line); cm != nil {
			if pendingRoute != "" {
				prefix := csharpExpandRouteTokens(pendingRoute, cm[1], "")
				start, end := csharpClassSpan(cm[1], fileNodes, i+1, len(lines))
				controllers = append(controllers, csController{prefix: prefix, startLine: start, endLine: end})
			}
		} else if pendingRoute != "" && !pendingVerb && strings.Contains(line, "(") {
			ln := blockLine
			if ln < 0 {
				ln = i
			}
			verbless = append(verbless, csVerblessRoute{template: pendingRoute, line: ln + 1})
		}
		reset()
	}
	return controllers, verbless
}

// csharpClassSpan returns the 1-based [start,end] line span of a class -- from
// fileNodes when present, else a fallback of the declaration line to EOF.
func csharpClassSpan(name string, fileNodes []*graph.Node, declLine, total int) (int, int) {
	for _, n := range fileNodes {
		if n != nil && n.Name == name && n.Kind == graph.KindType {
			return n.StartLine, n.EndLine
		}
	}
	return declLine, total
}

// csharpExpandRouteTokens resolves ASP.NET route tokens: [controller] -> the
// controller name minus a trailing "Controller", lower-cased; [action] -> the
// action (method) name, lower-cased. Tokens are matched case-insensitively.
func csharpExpandRouteTokens(tmpl, controller, action string) string {
	out := csharpReplaceToken(tmpl, "controller", strings.ToLower(strings.TrimSuffix(controller, "Controller")))
	if action != "" {
		out = csharpReplaceToken(out, "action", strings.ToLower(action))
	}
	return out
}

// csharpReplaceToken replaces every case-insensitive `[token]` in s with val.
func csharpReplaceToken(s, token, val string) string {
	needle := "[" + token + "]"
	for {
		lower := strings.ToLower(s)
		idx := strings.Index(lower, needle)
		if idx < 0 {
			return s
		}
		s = s[:idx] + val + s[idx+len(needle):]
	}
}

// csharpJoinControllerRoute prefix-joins a controller's class-level route onto a
// method's RELATIVE template. A template that is absolute (starts with "/" or
// "~/") ignores the controller prefix, per ASP.NET routing.
func csharpJoinControllerRoute(path string, controllers []csController, line int, action string) string {
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "~/") {
		return path
	}
	prefix := csharpControllerPrefixAt(controllers, line)
	if prefix == "" {
		return path
	}
	prefix = csharpReplaceToken(prefix, "action", strings.ToLower(action))
	return strings.TrimRight(prefix, "/") + "/" + strings.TrimLeft(path, "/")
}

// csharpControllerPrefixAt returns the class-level route prefix of the innermost
// controller whose span contains line, or "" when none.
func csharpControllerPrefixAt(controllers []csController, line int) string {
	best := ""
	bestStart := -1
	for _, c := range controllers {
		if line >= c.startLine && line <= c.endLine && c.startLine > bestStart {
			bestStart = c.startLine
			best = c.prefix
		}
	}
	return best
}

// csharpActionName returns the bare method name of the symbol enclosing line.
func csharpActionName(fileNodes []*graph.Node, line int) string {
	id := findEnclosingSymbol(fileNodes, line)
	if i := strings.LastIndex(id, "::"); i >= 0 {
		id = id[i+2:]
	}
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}
	return id
}

// csharpVerblessContracts materialises a route Contract (verb "ANY") for each
// verb-less method-level [Route("...")], prefix-joined to its controller.
func (h *HTTPExtractor) csharpVerblessContracts(filePath string, lines []string, fileNodes []*graph.Node, controllers []csController, verbless []csVerblessRoute, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for _, vr := range verbless {
		path := csharpJoinControllerRoute(vr.template, controllers, vr.line, csharpActionName(fileNodes, vr.line))
		normPath, origNames := NormalizeHTTPPathWithParams(path)
		method := "ANY"
		c := Contract{
			ID:       fmt.Sprintf("http::%s::%s", method, normPath),
			Type:     ContractHTTP,
			Role:     RoleProvider,
			SymbolID: findEnclosingSymbol(fileNodes, vr.line),
			FilePath: filePath,
			Line:     vr.line,
			Meta: map[string]any{
				"method":    method,
				"path":      normPath,
				"framework": "aspnet",
			},
			Confidence: 0.9,
		}
		if len(origNames) > 0 {
			c.Meta["path_param_names"] = origNames
		}
		EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
		out = append(out, c)
	}
	return out
}
