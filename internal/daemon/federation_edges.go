package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/resolver"
)

// ProxyEdgeProber implements resolver.RemoteDeclarationProber for the
// Option-B mint path: it asks each enabled remote that advertises the
// `subgraph` capability whether it owns a declaration of `name`, via the
// existing find_declaration tool over POST /v1/tools/find_declaration.
// It reuses the Federator's shared client cache, health cache, and
// circuit breaker, so it inherits the bounded-deadline + breaker
// protection of the read-only fan-out.
type ProxyEdgeProber struct {
	fed     *Federator
	remotes func() []ServerEntry // enabled-remote snapshot
	timeout time.Duration
	logger  *zap.Logger
}

// NewProxyEdgeProber wires the prober to the Federator's plumbing and an
// enabled-remote snapshot. Constructed by the daemon entry point only
// when federation.edges.enabled.
func NewProxyEdgeProber(fed *Federator, remotes func() []ServerEntry, timeout time.Duration, logger *zap.Logger) *ProxyEdgeProber {
	if logger == nil {
		logger = zap.NewNop()
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &ProxyEdgeProber{fed: fed, remotes: remotes, timeout: timeout, logger: logger}
}

// ProbeDeclaration asks each subgraph-capable enabled remote whether it
// owns a declaration of name, returning the first positive hit (cheapest,
// deterministic by roster order; design.md §6.4 lean). importHint is
// already the positive evidence the resolver required to call us at all
// (R-FED-6); the remote confirmation is the second half.
func (p *ProxyEdgeProber) ProbeDeclaration(ctx context.Context, name, importHint string) (resolver.RemoteDecl, bool) {
	if p == nil || p.fed == nil || name == "" || importHint == "" {
		return resolver.RemoteDecl{}, false
	}
	body, _ := json.Marshal(map[string]any{"use_site": name})

	for _, rem := range p.remotes() {
		if p.fed.breaker.isOpen(rem.Slug) {
			continue
		}
		cli, err := p.fed.clientFor(rem)
		if err != nil {
			continue
		}
		// R-NFR-4: only probe remotes that advertise the subgraph
		// capability; otherwise Option B is skipped for this remote and
		// the read path stays Option-C.
		h, herr := p.fed.health.get(ctx, cli, p.timeout)
		if herr != nil || !h.HasCapability("subgraph") {
			continue
		}

		rctx, cancel := context.WithTimeout(ctx, p.timeout)
		out, status, err := cli.ProxyToolCtx(rctx, "find_declaration", body)
		cancel()
		if err != nil || status != http.StatusOK {
			p.fed.breaker.fail(rem.Slug)
			continue
		}
		if decl, ok := parseRemoteDecl(out, rem.Slug, name); ok {
			return decl, true
		}
	}
	return resolver.RemoteDecl{}, false
}

// parseRemoteDecl unwraps a find_declaration tool result and returns the
// first declaration whose Name matches name (a real declaration of the
// symbol, not a coincidental use site), mapped to a resolver.RemoteDecl.
func parseRemoteDecl(out []byte, slug, name string) (resolver.RemoteDecl, bool) {
	toolJSON, _ := unwrapToolJSON(out)
	var payload struct {
		Declarations []struct {
			Declaration *graph.Node `json:"declaration"`
		} `json:"declarations"`
	}
	if err := json.Unmarshal(toolJSON, &payload); err != nil {
		return resolver.RemoteDecl{}, false
	}
	for _, g := range payload.Declarations {
		d := g.Declaration
		if d == nil || d.Name != name {
			continue
		}
		return resolver.RemoteDecl{
			Slug:        slug,
			RemoteID:    d.ID,
			Kind:        d.Kind,
			RepoPrefix:  d.RepoPrefix,
			WorkspaceID: d.WorkspaceID,
			File:        d.FilePath,
			Line:        d.StartLine,
		}, true
	}
	return resolver.RemoteDecl{}, false
}
