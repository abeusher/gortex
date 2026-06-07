package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
)

func declRemote(t *testing.T, declJSON string, caps []string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok", "indexed": true,
			"schema_version": localSchemaMajor, "api_version": 1, "read_only": true,
			"capabilities": caps,
		})
	})
	mux.HandleFunc("/v1/tools/find_declaration", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(envelope(declJSON))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func proberFor(remotes []ServerEntry) *ProxyEdgeProber {
	fed := NewFederator(FederationConfig{
		PerRemoteTimeout: 250 * time.Millisecond,
		Budget:           2 * time.Second,
		HealthTTL:        time.Millisecond,
	}, func(e ServerEntry) (*ServerClient, error) { return NewServerClient(e) }, nil)
	return NewProxyEdgeProber(fed, func() []ServerEntry { return remotes }, 250*time.Millisecond, nil)
}

const helperDeclJSON = `{"declarations":[{"declaration":{"id":"rb/lib/c.go::Helper","kind":"function","name":"Helper","file_path":"rb/lib/c.go","start_line":12,"repo_prefix":"rb","workspace_id":"wsB"},"use_sites":[]}]}`

func TestProbeDeclaration_Hit(t *testing.T) {
	remote := declRemote(t, helperDeclJSON, []string{"events", "subgraph"})
	p := proberFor([]ServerEntry{{Slug: "remoteB", URL: remote.URL}})

	decl, ok := p.ProbeDeclaration(context.Background(), "Helper", "extmod")
	if !ok {
		t.Fatal("expected a positive declaration hit")
	}
	if decl.Slug != "remoteB" || decl.RemoteID != "rb/lib/c.go::Helper" {
		t.Errorf("wrong decl identity: %+v", decl)
	}
	if decl.Kind != graph.KindFunction || decl.RepoPrefix != "rb" || decl.Line != 12 {
		t.Errorf("decl fields wrong: %+v", decl)
	}
}

func TestProbeDeclaration_NoSubgraphCap_Skipped(t *testing.T) {
	// The remote advertises no `subgraph` capability, so Option B is
	// skipped for it (R-NFR-4) even though it would have the declaration.
	remote := declRemote(t, helperDeclJSON, []string{"events"})
	p := proberFor([]ServerEntry{{Slug: "remoteB", URL: remote.URL}})

	if _, ok := p.ProbeDeclaration(context.Background(), "Helper", "extmod"); ok {
		t.Error("a remote without the subgraph cap must not yield a hit (R-NFR-4)")
	}
}

func TestProbeDeclaration_EmptyHint_NoProbe(t *testing.T) {
	remote := declRemote(t, helperDeclJSON, []string{"events", "subgraph"})
	p := proberFor([]ServerEntry{{Slug: "remoteB", URL: remote.URL}})

	if _, ok := p.ProbeDeclaration(context.Background(), "Helper", ""); ok {
		t.Error("an empty import hint must short-circuit before probing")
	}
}

func TestProbeDeclaration_NameMismatch_NoHit(t *testing.T) {
	// Remote returns a declaration named something else — not a hit.
	other := `{"declarations":[{"declaration":{"id":"rb/lib/c.go::Other","kind":"function","name":"Other"}}]}`
	remote := declRemote(t, other, []string{"events", "subgraph"})
	p := proberFor([]ServerEntry{{Slug: "remoteB", URL: remote.URL}})

	if _, ok := p.ProbeDeclaration(context.Background(), "Helper", "extmod"); ok {
		t.Error("a declaration whose name does not match must not be a hit")
	}
}
