package contracts

import (
	"path"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// File-based routing for the dominant frontend frameworks. Unlike every other
// HTTP extractor these routes are derived from the file PATH, not its content
// — `app/users/[id]/page.tsx` IS the route `GET /users/{id}`. The derived
// routes land on the same `http::METHOD::path` provider model as code-defined
// routes, so a file-routed page pairs with the `fetch('/users/'+id)` consumer
// elsewhere in the workspace through Gortex's canonical-path consumer pairing
// — the cross-file/cross-repo reach a same-file regex scanner cannot match.

// routeFileMarkers are the directory signatures that make a file worth routing.
// A cheap substring gate so the per-file extractor can bypass the content
// prefilter (which keys off fetch/axios/app. markers a page file never has).
var routeFileMarkers = []string{
	"/app/", "/pages/", "/src/routes/", "/src/pages/",
	"/server/api/", "/server/routes/",
}

// isFileBasedRouteFile reports whether filePath sits under a framework's route
// root. Cheap path check used to bypass the content-marker prefilter.
func isFileBasedRouteFile(filePath string) bool {
	p := "/" + strings.TrimPrefix(filePath, "/")
	for _, m := range routeFileMarkers {
		if strings.Contains(p, m) {
			return true
		}
	}
	return false
}

// fileRoute is one route derived from a path: its HTTP method, route path
// (with :params), and the framework that produced it.
type fileRoute struct {
	method    string
	routePath string
	framework string
}

// exportedHTTPMethodRE matches an exported request-method handler in an API
// route module: `export async function GET`, `export const POST = ...`.
var exportedHTTPMethodRE = regexp.MustCompile(`(?m)export\s+(?:async\s+)?(?:function|const|let|var)\s+(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|ALL)\b`)

// nuxtMethodSuffixRE matches a Nuxt server-route method suffix: `users.get.ts`.
var nuxtMethodSuffixRE = regexp.MustCompile(`\.(get|post|put|patch|delete|head|options)$`)

// extractFileBasedRoutes maps a route file to its provider contracts, binding
// each to the page/handler symbol in the file when one is present.
func (h *HTTPExtractor) extractFileBasedRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	routes := deriveFileRoutes(filePath, text)
	if len(routes) == 0 {
		return nil
	}
	// The page/handler symbol is the file's first exported function/component,
	// when present — gives request->render tracing a symbol to land on.
	handlerID := firstHandlerSymbol(fileNodes)

	var out []Contract
	for _, r := range routes {
		normPath, origNames := NormalizeHTTPPathWithParams(r.routePath)
		c := Contract{
			ID:         "http::" + r.method + "::" + normPath,
			Type:       ContractHTTP,
			Role:       RoleProvider,
			SymbolID:   handlerID,
			FilePath:   filePath,
			Line:       1,
			Confidence: 0.85,
			Meta: map[string]any{
				"method":      r.method,
				"path":        normPath,
				"framework":   r.framework,
				"file_routed": true,
			},
		}
		if len(origNames) > 0 {
			c.Meta["path_param_names"] = origNames
		}
		EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
		out = append(out, c)
	}
	return out
}

// deriveFileRoutes maps a file path (and, for API modules, its exported
// methods) to the routes it serves. Returns nil when the path is under a route
// root but is not itself a routable file (a component, a util, a layout-only).
func deriveFileRoutes(filePath, text string) []fileRoute {
	norm := "/" + strings.TrimPrefix(toSlash(filePath), "/")
	base := path.Base(norm)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	switch {
	// SvelteKit: src/routes/**, special `+`-prefixed files.
	case strings.Contains(norm, "/src/routes/") && strings.HasPrefix(base, "+"):
		rel, _ := afterSegment(norm, "routes")
		dir := path.Dir(rel)
		rp := dirToRoutePath(dir)
		if stem == "+server" {
			return apiRoutes(text, rp, "sveltekit")
		}
		if stem == "+page" || stem == "+page.server" || stem == "+layout" {
			if stem == "+layout" {
				return nil // a layout is not itself a route
			}
			return []fileRoute{{"GET", rp, "sveltekit"}}
		}
		return nil

	// Next.js app router: app/**, page.* / route.* files.
	case strings.Contains(norm, "/app/") && (stem == "page" || stem == "route"):
		rel, _ := afterSegment(norm, "app")
		rp := dirToRoutePath(path.Dir(rel))
		if stem == "route" {
			return apiRoutes(text, rp, "nextjs")
		}
		return []fileRoute{{"GET", rp, "nextjs"}}

	// Astro: src/pages/** — .astro pages (GET) and .ts/.js endpoints (scanned).
	case strings.Contains(norm, "/src/pages/"):
		rel, _ := afterSegment(norm, "pages")
		rp := fileToRoutePath(rel)
		if ext == ".astro" {
			return []fileRoute{{"GET", rp, "astro"}}
		}
		if ext == ".ts" || ext == ".js" || ext == ".mjs" {
			return apiRoutes(text, rp, "astro")
		}
		return nil

	// Nuxt server routes: server/api/** and server/routes/**.
	case strings.Contains(norm, "/server/api/") || strings.Contains(norm, "/server/routes/"):
		seg := "api"
		prefix := "/api"
		if strings.Contains(norm, "/server/routes/") {
			seg, prefix = "routes", ""
		}
		rel, _ := afterSegment(norm, seg)
		// A `.get`/`.post` suffix on the stem selects the method.
		method := ""
		relStem := strings.TrimSuffix(rel, ext)
		if mm := nuxtMethodSuffixRE.FindStringSubmatch(relStem); mm != nil {
			method = strings.ToUpper(mm[1])
			relStem = strings.TrimSuffix(relStem, "."+mm[1])
		}
		rp := joinRoute(prefix, fileToRoutePath(relStem+ext))
		if method != "" {
			return []fileRoute{{method, rp, "nuxt"}}
		}
		return apiRoutes(text, rp, "nuxt")

	// Nuxt pages: pages/**/*.vue.
	case strings.Contains(norm, "/pages/") && ext == ".vue":
		rel, _ := afterSegment(norm, "pages")
		return []fileRoute{{"GET", fileToRoutePath(rel), "nuxt"}}

	// Next.js pages router: pages/**/*.{tsx,ts,jsx,js}, skipping framework files.
	case strings.Contains(norm, "/pages/") && isJSFamily(ext) && !strings.HasPrefix(stem, "_"):
		rel, _ := afterSegment(norm, "pages")
		rp := fileToRoutePath(rel)
		if strings.HasPrefix(rp, "/api") {
			return apiRoutes(text, rp, "nextjs")
		}
		return []fileRoute{{"GET", rp, "nextjs"}}
	}
	return nil
}

// apiRoutes builds one route per exported request-method handler; an API module
// with no recognizable export still registers a GET so the path is navigable.
func apiRoutes(text, routePath, framework string) []fileRoute {
	methods := scanExportedHTTPMethods(text)
	if len(methods) == 0 {
		methods = []string{"GET"}
	}
	out := make([]fileRoute, 0, len(methods))
	for _, m := range methods {
		out = append(out, fileRoute{m, routePath, framework})
	}
	return out
}

// scanExportedHTTPMethods returns the distinct exported request-method names in
// source order (ALL -> ANY, matching the existing wildcard convention).
func scanExportedHTTPMethods(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range exportedHTTPMethodRE.FindAllStringSubmatch(text, -1) {
		v := m[1]
		if v == "ALL" {
			v = "ANY"
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

// dirToRoutePath converts a directory path (the part after a route root) into a
// route path, applying dynamic-segment + route-group rules. Used by the
// directory-as-route frameworks (Next app router, SvelteKit).
func dirToRoutePath(dir string) string {
	if dir == "." || dir == "" || dir == "/" {
		return "/"
	}
	var segs []string
	for _, s := range strings.Split(dir, "/") {
		if t := convertRouteSegment(s); t != "" {
			segs = append(segs, t)
		}
	}
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// fileToRoutePath converts a file path (the part after a route root, including
// the file name) into a route path. Used by the filename-as-route frameworks
// (Next pages router, Nuxt, Astro): the file stem is the last segment, and an
// `index` stem maps to the parent route.
func fileToRoutePath(rel string) string {
	ext := path.Ext(rel)
	rel = strings.TrimSuffix(rel, ext)
	var segs []string
	for _, s := range strings.Split(rel, "/") {
		if s == "index" {
			continue // index file -> parent route
		}
		if t := convertRouteSegment(s); t != "" {
			segs = append(segs, t)
		}
	}
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// convertRouteSegment maps one path segment to its route form: a route group
// `(group)` is elided; every bracketed dynamic form — `[id]`, `[...slug]`,
// `[[...opt]]` (Next/Nuxt), `[id]`/`[...rest]` (SvelteKit) — becomes a `:name`
// parameter. Returns "" for an elided segment.
func convertRouteSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Route groups / private folders contribute no URL segment.
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		return ""
	}
	if strings.HasPrefix(s, "[") {
		inner := strings.Trim(s, "[]")
		inner = strings.TrimPrefix(inner, "...")
		if inner == "" {
			inner = "param"
		}
		return ":" + inner
	}
	return s
}

// firstHandlerSymbol returns the file's first function/method node ID — the
// page component or the route handler — so the contract has a symbol to bind.
func firstHandlerSymbol(fileNodes []*graph.Node) string {
	for _, n := range fileNodes {
		if n.Kind == graph.KindFunction || n.Kind == graph.KindMethod {
			return n.ID
		}
	}
	return ""
}

// --- React Router (content-based) ---------------------------------------

var (
	reactRouteElementRE = regexp.MustCompile(`<Route\b[^>]*\bpath\s*=\s*["']([^"']+)["']`)
	reactRouterObjectRE = regexp.MustCompile(`\bpath\s*:\s*["']([^"']+)["']`)
)

// hasReactRouterMarkers reports whether the source uses React Router's route
// declaration forms — the gate to run extractReactRouterRoutes (and to bypass
// the content prefilter for a routes-only module).
func hasReactRouterMarkers(src []byte) bool {
	return strings.Contains(string(src), "createBrowserRouter") ||
		strings.Contains(string(src), "<Route")
}

// extractReactRouterRoutes mines `<Route path=...>` JSX elements and
// createBrowserRouter object `path:` entries into GET provider contracts on the
// canonical path model so they pair with fetch consumers.
func (h *HTTPExtractor) extractReactRouterRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	seen := map[string]bool{}
	emit := func(p string, off int) {
		p = strings.TrimSpace(p)
		if p == "" || p == "*" {
			return
		}
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		normPath, origNames := NormalizeHTTPPathWithParams(p)
		if seen[normPath] {
			return
		}
		seen[normPath] = true
		c := Contract{
			ID:         "http::GET::" + normPath,
			Type:       ContractHTTP,
			Role:       RoleProvider,
			FilePath:   filePath,
			Line:       lineAtOffset(lines, off),
			Confidence: 0.7,
			Meta: map[string]any{
				"method":      "GET",
				"path":        normPath,
				"framework":   "react-router",
				"file_routed": false,
			},
		}
		if len(origNames) > 0 {
			c.Meta["path_param_names"] = origNames
		}
		EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
		out = append(out, c)
	}
	for _, m := range reactRouteElementRE.FindAllStringSubmatchIndex(text, -1) {
		emit(text[m[2]:m[3]], m[0])
	}
	if strings.Contains(text, "createBrowserRouter") || strings.Contains(text, "createRoutesFromElements") {
		for _, m := range reactRouterObjectRE.FindAllStringSubmatchIndex(text, -1) {
			emit(text[m[2]:m[3]], m[0])
		}
	}
	return out
}

// --- small path helpers -------------------------------------------------

func toSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// afterSegment returns the path after the LAST `/seg/` marker (closest route
// root in a nested/monorepo layout).
func afterSegment(p, seg string) (string, bool) {
	marker := "/" + seg + "/"
	if i := strings.LastIndex(p, marker); i >= 0 {
		return p[i+len(marker):], true
	}
	return "", false
}

// joinRoute joins a route prefix and a route path, collapsing a root child.
func joinRoute(prefix, rp string) string {
	if prefix == "" {
		return rp
	}
	if rp == "/" {
		return prefix
	}
	return prefix + rp
}

func isJSFamily(ext string) bool {
	switch ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return true
	}
	return false
}
