package contracts

import (
	"testing"
)

func TestRouteShapeMeta_OriginalPathAndKind(t *testing.T) {
	src := []byte(`
app.get('/v1/sessions/:id', getSession)
app.get('/health', getHealth)
`)
	cs := (&HTTPExtractor{}).Extract("app.ts", src, nil, nil)

	sess := flaskFind(cs, "GET", "/v1/sessions/{p1}")
	if sess == nil {
		t.Fatalf("expected GET /v1/sessions/{p1}, got %+v", contractPaths(cs))
	}
	if sess.Meta["original_path"] != "/v1/sessions/{id}" {
		t.Errorf("original_path = %v, want /v1/sessions/{id}", sess.Meta["original_path"])
	}
	if sess.Meta["route_kind"] != "parametric" {
		t.Errorf("route_kind = %v, want parametric", sess.Meta["route_kind"])
	}

	health := flaskFind(cs, "GET", "/health")
	if health == nil {
		t.Fatalf("expected GET /health, got %+v", contractPaths(cs))
	}
	if health.Meta["original_path"] != "/health" {
		t.Errorf("original_path = %v, want /health", health.Meta["original_path"])
	}
	if health.Meta["route_kind"] != "static" {
		t.Errorf("route_kind = %v, want static", health.Meta["route_kind"])
	}
}

func TestRouteShapeMeta_JoinedOriginalPath(t *testing.T) {
	files := srcMap{
		"routes.ts": `
const router = express.Router();
router.get('/users/:id', getUser);
`,
		"app.ts": `
const app = express();
app.use('/api', router);
`,
	}
	reg := NewRegistry()
	extractInto(t, reg, files, "svc", "svc")
	JoinRouterPrefixes(reg, files.paths(), files.reader())

	found := false
	for _, c := range reg.All() {
		if c.ID != "http::GET::/api/users/{p1}" {
			continue
		}
		found = true
		// original_path tracks the joined path with the developer's param name.
		if c.Meta["original_path"] != "/api/users/{id}" {
			t.Errorf("joined original_path = %v, want /api/users/{id}", c.Meta["original_path"])
		}
		if c.Meta["route_kind"] != "parametric" {
			t.Errorf("route_kind = %v, want parametric", c.Meta["route_kind"])
		}
	}
	if !found {
		t.Fatalf("expected joined http::GET::/api/users/{p1} in registry")
	}
}
