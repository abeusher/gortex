package contracts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

var (
	// djangoRouteRE matches a urlpatterns entry — path / re_path / url — and
	// captures the call name, the route literal (regex or path converter) and
	// the remainder of the line (the view + name= kwargs).
	djangoRouteRE = regexp.MustCompile(`\b(path|re_path|url)\(\s*r?["']([^"']*)["']\s*,\s*(.+)`)
	// djangoRouteCallRE is the cheap prefilter for the dedicated pass.
	djangoRouteCallRE = regexp.MustCompile(`\b(?:path|re_path|url)\s*\(`)
	// djangoIncludeRE matches an include('app.urls') sub-URLconf mount.
	djangoIncludeRE = regexp.MustCompile(`include\s*\(\s*["']([^"']+)["']`)
	// djangoAsViewRE matches a class-based view handler, View.as_view().
	djangoAsViewRE = regexp.MustCompile(`([A-Za-z_][\w.]*)\.as_view\b`)
	// djangoLeadIdentRE captures the leading dotted identifier of a handler.
	djangoLeadIdentRE = regexp.MustCompile(`^([A-Za-z_][\w.]*)`)
	// djangoPathConverterRE rewrites a path() converter, <int:year> / <year>,
	// to the {year} placeholder NormalizeHTTPPathWithParams understands.
	djangoPathConverterRE = regexp.MustCompile(`<(?:\w+:)?(\w+)>`)
	// djangoNamedGroupRE rewrites a re_path() named group, (?P<year>[0-9]{4}),
	// to {year}.
	djangoNamedGroupRE = regexp.MustCompile(`\(\?P<(\w+)>[^)]*\)`)
)

// extractDjangoRoutes detects Django urlpatterns route shapes —
// path / re_path / url — resolving the view handler (function, or a
// class-based View.as_view()) and recording include('app.urls') sub-URLconf
// mounts. It runs as a node-aware pass because the handler is a symbol declared
// elsewhere in the file, which the per-line provider table cannot resolve.
func (h *HTTPExtractor) extractDjangoRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for i, line := range lines {
		m := djangoRouteRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		callName, rawRoute, rest := m[1], m[2], m[3]
		lineNum := i + 1

		// path('blog/', include('blog.urls')) mounts a sub-URLconf.
		if inc := djangoIncludeRE.FindStringSubmatch(rest); inc != nil {
			out = append(out, h.buildDjangoMount(filePath, callName, rawRoute, inc[1], lineNum, lines, fileNodes, lang, tree))
			continue
		}

		symbolID := resolveDjangoHandler(rest, fileNodes)
		out = append(out, h.buildDjangoContract(filePath, callName, rawRoute, symbolID, lineNum, lines, fileNodes, lang, tree))
	}
	return out
}

// resolveDjangoHandler resolves the view a route dispatches to: a class-based
// View.as_view() resolves to the class node, otherwise the leading identifier
// resolves as a function/method handler.
func resolveDjangoHandler(expr string, fileNodes []*graph.Node) string {
	expr = strings.TrimSpace(expr)
	if m := djangoAsViewRE.FindStringSubmatch(expr); m != nil {
		name := m[1]
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if t := findTypeNodeByName(fileNodes, name); t != nil {
			return t.ID
		}
		return ""
	}
	if m := djangoLeadIdentRE.FindStringSubmatch(expr); m != nil {
		return resolveHandlerIdent(fileNodes, m[1])
	}
	return ""
}

// normalizeDjangoRoute rewrites a Django route literal to the canonical HTTP
// path the contract ID hashes on: path() converters and re_path() named groups
// both collapse onto positional placeholders, and the route is rooted at "/".
func normalizeDjangoRoute(callName, raw string) string {
	s := strings.TrimSpace(raw)
	if callName == "re_path" || callName == "url" {
		s = strings.TrimPrefix(s, "^")
		s = strings.TrimSuffix(s, "$")
		s = djangoNamedGroupRE.ReplaceAllString(s, "{$1}")
	} else {
		s = djangoPathConverterRE.ReplaceAllString(s, "{$1}")
	}
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return s
}

// buildDjangoContract assembles a provider contract for one Django route. The
// method is ANY: Django dispatches every verb to the same view, which decides
// the methods it serves.
func (h *HTTPExtractor) buildDjangoContract(filePath, callName, rawRoute, symbolID string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	normPath, origNames := NormalizeHTTPPathWithParams(normalizeDjangoRoute(callName, rawRoute))
	meta := map[string]any{
		"method":      "ANY",
		"path":        normPath,
		"framework":   "django",
		"route_shape": callName,
	}
	if len(origNames) > 0 {
		meta["path_param_names"] = origNames
	}
	c := Contract{
		ID:         fmt.Sprintf("http::ANY::%s", normPath),
		Type:       ContractHTTP,
		Role:       RoleProvider,
		SymbolID:   symbolID,
		FilePath:   filePath,
		Line:       lineNum,
		Meta:       meta,
		Confidence: 0.85,
	}
	EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
	return c
}

// drfAction is one Django REST Framework ViewSet action and the HTTP verb +
// collection/detail route the default router maps it to.
type drfAction struct {
	name   string
	method string
	detail bool // true → prefix/{pk}, false → prefix
}

// drfActions is the standard ViewSet action set a DefaultRouter wires up.
var drfActions = []drfAction{
	{"list", "GET", false},
	{"create", "POST", false},
	{"retrieve", "GET", true},
	{"update", "PUT", true},
	{"partial_update", "PATCH", true},
	{"destroy", "DELETE", true},
}

// drfRegisterRE matches router.register(r'prefix', ViewSetClass[, ...]).
var drfRegisterRE = regexp.MustCompile(`\.register\s*\(\s*r?["']([^"']*)["']\s*,\s*([A-Za-z_]\w*)`)

// extractDRFRoutes detects Django REST Framework router.register(prefix,
// ViewSet) registrations and expands each into the per-action routes the
// default router generates. An action defined explicitly on the ViewSet
// resolves to that method node; the standard actions a ModelViewSet /
// ReadOnlyModelViewSet inherits resolve to the ViewSet class node.
func (h *HTTPExtractor) extractDRFRoutes(filePath, text string, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) []Contract {
	var out []Contract
	for i, line := range lines {
		m := drfRegisterRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prefix, viewSet := m[1], m[2]
		lineNum := i + 1

		classID := ""
		if t := findTypeNodeByName(fileNodes, viewSet); t != nil {
			classID = t.ID
		}
		explicit := drfActionMethods(fileNodes, classID)
		for _, a := range drfViewSetActions(text, viewSet, explicit) {
			handler := explicit[a.name]
			if handler == "" {
				if classID == "" {
					continue
				}
				handler = classID
			}
			out = append(out, h.buildDRFContract(filePath, a, prefix, handler, viewSet, lineNum, lines, fileNodes, lang, tree))
		}
	}
	return out
}

// drfViewSetActions returns the actions a registered ViewSet serves: every
// action it defines explicitly, plus the standard set its base class supplies
// (all six for ModelViewSet, list+retrieve for ReadOnlyModelViewSet).
func drfViewSetActions(text, viewSet string, explicit map[string]string) []drfAction {
	bases := ""
	if m := regexp.MustCompile(`class\s+` + regexp.QuoteMeta(viewSet) + `\s*\(([^)]*)\)`).FindStringSubmatch(text); m != nil {
		bases = m[1]
	}
	readonly := strings.Contains(bases, "ReadOnlyModelViewSet")
	full := strings.Contains(bases, "ModelViewSet") && !readonly

	var out []drfAction
	for _, a := range drfActions {
		switch {
		case explicit[a.name] != "":
			out = append(out, a)
		case full:
			out = append(out, a)
		case readonly && (a.name == "list" || a.name == "retrieve"):
			out = append(out, a)
		}
	}
	return out
}

// drfActionMethods returns action-name→methodNodeID for the standard ViewSet
// actions a class defines explicitly.
func drfActionMethods(fileNodes []*graph.Node, classID string) map[string]string {
	out := map[string]string{}
	if classID == "" {
		return out
	}
	for _, a := range drfActions {
		want := classID + "." + a.name
		for _, n := range fileNodes {
			if n.Kind == graph.KindMethod && n.ID == want {
				out[a.name] = n.ID
				break
			}
		}
	}
	return out
}

// buildDRFContract assembles a provider contract for one DRF ViewSet action.
func (h *HTTPExtractor) buildDRFContract(filePath string, a drfAction, prefix, symbolID, viewSet string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	route := "/" + strings.Trim(prefix, "^$/ ")
	if a.detail {
		route += "/{pk}"
	}
	normPath, origNames := NormalizeHTTPPathWithParams(route)
	meta := map[string]any{
		"method":      a.method,
		"path":        normPath,
		"framework":   "drf",
		"route_shape": "viewset",
		"drf_action":  a.name,
		"viewset":     viewSet,
	}
	if len(origNames) > 0 {
		meta["path_param_names"] = origNames
	}
	c := Contract{
		ID:         fmt.Sprintf("http::%s::%s", a.method, normPath),
		Type:       ContractHTTP,
		Role:       RoleProvider,
		SymbolID:   symbolID,
		FilePath:   filePath,
		Line:       lineNum,
		Meta:       meta,
		Confidence: 0.85,
	}
	EnrichHTTPContractWithTree(&c, lines, fileNodes, lang, tree)
	return c
}

// buildDjangoMount records a path('prefix/', include('app.urls')) sub-URLconf
// mount — the prefix-join seed the route-prefix pass consumes.
func (h *HTTPExtractor) buildDjangoMount(filePath, callName, rawRoute, includeModule string, lineNum int, lines []string, fileNodes []*graph.Node, lang string, tree *parser.ParseTree) Contract {
	normPath, _ := NormalizeHTTPPathWithParams(normalizeDjangoRoute(callName, rawRoute))
	return Contract{
		ID:   fmt.Sprintf("http::MOUNT::%s", normPath),
		Type: ContractHTTP,
		Role: RoleProvider,
		Meta: map[string]any{
			"method":         "MOUNT",
			"path":           normPath,
			"framework":      "django",
			"route_shape":    callName,
			"django_include": includeModule,
			"mount":          true,
		},
		FilePath:   filePath,
		Line:       lineNum,
		Confidence: 0.85,
	}
}
