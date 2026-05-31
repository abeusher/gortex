//go:build ladybug

package store_ladybug

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// TestFTS_MultiRepoIsolation is the regression for the multi-repo
// clobber bug: per-repo Indexers share one Store, and a previous
// BulkUpsertSymbolFTS implementation wiped every row in SymbolFTS
// (MATCH (f:SymbolFTS) DELETE f) before COPY. The result was that
// only the last-committed repo's symbols survived in the FTS corpus
// and search_symbols was broken for every sibling.
//
// This test seeds two "repos" with disjoint IDs, calls
// BulkUpsertSymbolFTS twice in succession (once per prefix), then
// asserts that SearchSymbols still returns hits from BOTH repos.
func TestFTS_MultiRepoIsolation(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-multi-repo-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	repoA := "gortex"
	repoB := "gortex-cloud"

	itemsA := []graph.SymbolFTSItem{
		{NodeID: repoA + "/internal/mcp/server.go::NewServer", Tokens: "new server internal mcp"},
		{NodeID: repoA + "/internal/indexer/indexer.go::IndexAll", Tokens: "index all internal indexer"},
	}
	itemsB := []graph.SymbolFTSItem{
		{NodeID: repoB + "/api/billing.go::ChargeCustomer", Tokens: "charge customer api billing"},
	}
	for _, it := range itemsA {
		s.AddNode(&graph.Node{ID: it.NodeID, Kind: graph.KindFunction, RepoPrefix: repoA, FilePath: it.NodeID})
	}
	for _, it := range itemsB {
		s.AddNode(&graph.Node{ID: it.NodeID, Kind: graph.KindFunction, RepoPrefix: repoB, FilePath: it.NodeID})
	}

	// Commit repo A, then repo B — the live order: each repo's
	// per-repo Indexer drains and calls BulkUpsertSymbolFTS as it
	// finishes warming up.
	if err := s.BulkUpsertSymbolFTS(repoA, itemsA); err != nil {
		t.Fatalf("repo A bulk: %v", err)
	}
	if err := s.BulkUpsertSymbolFTS(repoB, itemsB); err != nil {
		t.Fatalf("repo B bulk: %v", err)
	}
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("build: %v", err)
	}

	// Repo A's symbol must still be searchable after repo B's
	// commit — pre-fix this returned 0 hits.
	hitsA, err := s.SearchSymbols("NewServer", 10)
	if err != nil {
		t.Fatalf("search A: %v", err)
	}
	if len(hitsA) == 0 {
		t.Fatalf("repo A NewServer wiped by repo B commit — fix regressed")
	}
	t.Logf("repo A 'NewServer' → %d hits", len(hitsA))

	hitsB, err := s.SearchSymbols("ChargeCustomer", 10)
	if err != nil {
		t.Fatalf("search B: %v", err)
	}
	if len(hitsB) == 0 {
		t.Fatalf("repo B ChargeCustomer not searchable")
	}
	t.Logf("repo B 'ChargeCustomer' → %d hits", len(hitsB))

	// A second pass on repo A (incremental re-commit) must wipe
	// only repo A's rows, leaving repo B intact.
	itemsAUpdated := []graph.SymbolFTSItem{
		// Original NewServer dropped; only IndexAll re-committed.
		{NodeID: repoA + "/internal/indexer/indexer.go::IndexAll", Tokens: "index all internal indexer"},
	}
	if err := s.BulkUpsertSymbolFTS(repoA, itemsAUpdated); err != nil {
		t.Fatalf("repo A re-commit: %v", err)
	}
	// Force the FTS index to rebuild against the post-wipe corpus
	// — the COPY path resets indexBuilt to force a rebuild on the
	// next search, but a stale build sentinel from a parallel
	// rebuild would skip it.
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("rebuild index: %v", err)
	}
	hitsA2, err := s.SearchSymbols("NewServer", 10)
	if err != nil {
		t.Fatalf("search A2: %v", err)
	}
	if len(hitsA2) != 0 {
		t.Fatalf("expected NewServer to be dropped after repo A re-commit, got %d hits", len(hitsA2))
	}
	hitsB2, err := s.SearchSymbols("ChargeCustomer", 10)
	if err != nil {
		t.Fatalf("search B2: %v", err)
	}
	if len(hitsB2) == 0 {
		t.Fatalf("repo B was wiped by repo A re-commit — selective wipe is leaking")
	}
	t.Logf("repo B preserved across repo A re-commit: %d hits", len(hitsB2))
}

// realisticTokens mirrors what indexer.ftsTokensFor would produce
// for a code symbol, without pulling in the indexer package: feed
// Name / FilePath / signature through search.Tokenize and join with
// spaces.
func realisticTokens(n *graph.Node) string {
	fields := []string{n.Name, n.FilePath}
	if n.QualName != "" {
		fields = append(fields, n.QualName)
	}
	if sig, ok := n.Meta["signature"].(string); ok && sig != "" {
		fields = append(fields, sig)
	}
	var out []string
	for _, f := range fields {
		out = append(out, search.Tokenize(f)...)
	}
	return strings.Join(out, " ")
}

// TestFTS_MultiTermRecall probes whether QUERY_FTS_INDEX matches a
// multi-word query against documents whose tokens column contains the
// same words in any order. The production search path stores
// pre-tokenised tokens like "new server" and queries with the same
// joined-by-spaces form; user-visible bench shows the multi-term case
// returning empty while single-term "store" returns hits.
//
// The probe seeds three SymbolFTS rows mirroring real symbol shapes:
//   - "new server" → matches "NewServer"
//   - "index all"  → matches "IndexAll"
//   - "store"      → matches "Store"
//
// Then queries with single-term and multi-term forms and logs what
// the engine returns.
func TestFTS_MultiTermRecall(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-multi-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	items := []graph.SymbolFTSItem{
		{NodeID: "pkg/mcp.go::NewServer", Tokens: "new server newserver mcp.newserver"},
		{NodeID: "pkg/indexer.go::IndexAll", Tokens: "index all indexall indexer.indexall"},
		{NodeID: "pkg/store.go::Store", Tokens: "store ladybug.store"},
		{NodeID: "pkg/proto.go::HandleStreamable", Tokens: "handle streamable handlestreamable mcp.handlestreamable"},
	}
	// Stamp the Node rows too — QUERY_FTS_INDEX joins back to the
	// base table via node.id, so unreferenced FTS rows return id=null
	// and the production code drops them.
	for _, it := range items {
		s.AddNode(&graph.Node{
			ID:       it.NodeID,
			Kind:     graph.KindFunction,
			Name:     it.NodeID, // doesn't matter for FTS — index is on SymbolFTS.tokens
			FilePath: "pkg/x.go",
			Language: "go",
		})
	}
	if err := s.BulkUpsertSymbolFTS("", items); err != nil {
		t.Fatalf("bulk upsert: %v", err)
	}
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("build index: %v", err)
	}

	probes := []struct {
		name  string
		query string
	}{
		{"single 'store'", "store"},
		{"single 'new'", "new"},
		{"single 'server'", "server"},
		{"multi 'new server'", "new server"},
		{"multi 'index all'", "index all"},
		{"multi 'handle streamable'", "handle streamable"},
		{"concat 'newserver'", "newserver"},
		{"concat 'indexall'", "indexall"},
	}
	const q = `CALL QUERY_FTS_INDEX('SymbolFTS', '` + ftsIndexName + `', $q) RETURN node.id AS id, score ORDER BY score DESC LIMIT 10`
	for _, p := range probes {
		rows, err := querySelectSafe(s, q, map[string]any{"q": p.query})
		if err != nil {
			t.Logf("FAIL %s (%q): err=%v", p.name, p.query, err)
			continue
		}
		t.Logf("%s (%q) → %d rows", p.name, p.query, len(rows))
		for _, r := range rows {
			t.Logf("  %v", r)
		}
	}

	// Also test with the conjunctive=false / top=10 option syntax
	// that some Ladybugdb/ Ladybug builds accept.
	probes2 := []struct {
		name  string
		query string
	}{
		{"opts conjunctive=false 'new server'", "new server"},
		{"opts conjunctive=true 'new server'", "new server"},
	}
	for _, p := range probes2 {
		// Try the optional-arg-map syntax: CALL QUERY_FTS_INDEX(...,
		// {conjunctive: false, top: 10}).
		conjunctive := strings.Contains(p.name, "true")
		qWithOpts := `CALL QUERY_FTS_INDEX('SymbolFTS', '` + ftsIndexName + `', $q, conjunctive:=$c) RETURN node.id AS id, score ORDER BY score DESC LIMIT 10`
		rows, err := querySelectSafe(s, qWithOpts, map[string]any{
			"q": p.query,
			"c": conjunctive,
		})
		if err != nil {
			t.Logf("FAIL %s (%q): err=%v", p.name, p.query, err)
			continue
		}
		t.Logf("%s (%q) → %d rows", p.name, p.query, len(rows))
		for _, r := range rows {
			t.Logf("  %v", r)
		}
	}
}

// TestFTS_RealisticCorpus uses ftsTokensFor-equivalent input
// (Tokenize on Name/QualName/FilePath/signature, join with spaces) so
// the probe runs against tokens shaped exactly like what the live
// indexer writes. Then it calls Store.SearchSymbols — the same code
// path the engine's BM25 backend hits. If this returns hits for
// "NewServer" the bug is in a layer above SearchSymbols (engine
// post-filter, rerank, scope); if it returns empty the bug is in the
// FTS tokenization or query construction.
func TestFTS_RealisticCorpus(t *testing.T) {
	dir, err := os.MkdirTemp("", "lbug-fts-real-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	s, err := Open(filepath.Join(dir, "store.lbug"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// A small but realistic corpus modelling several real gortex
	// symbols. Each Node carries the fields ftsTokensFor reads:
	// Name / QualName / FilePath / Meta["signature"].
	corpus := []*graph.Node{
		{
			ID:       "internal/mcp/server.go::NewServer",
			Kind:     graph.KindFunction,
			Name:     "NewServer",
			QualName: "mcp.NewServer",
			FilePath: "internal/mcp/server.go",
			Language: "go",
			Meta:     map[string]any{"signature": "func NewServer(g graph.Store) *Server"},
		},
		{
			ID:       "internal/mcp/server.go::Server",
			Kind:     graph.KindType,
			Name:     "Server",
			QualName: "mcp.Server",
			FilePath: "internal/mcp/server.go",
			Language: "go",
			Meta:     map[string]any{"signature": "type Server struct"},
		},
		{
			ID:       "internal/indexer/indexer.go::IndexAll",
			Kind:     graph.KindFunction,
			Name:     "IndexAll",
			QualName: "indexer.IndexAll",
			FilePath: "internal/indexer/indexer.go",
			Language: "go",
			Meta:     map[string]any{"signature": "func IndexAll(ctx context.Context) error"},
		},
		{
			ID:       "internal/mcp/streamable.go::handleStreamable",
			Kind:     graph.KindFunction,
			Name:     "handleStreamable",
			QualName: "mcp.handleStreamable",
			FilePath: "internal/mcp/streamable.go",
			Language: "go",
			Meta:     map[string]any{"signature": "func handleStreamable(w http.ResponseWriter, r *http.Request)"},
		},
		{
			ID:       "internal/graph/store_ladybug/store.go::Store",
			Kind:     graph.KindType,
			Name:     "Store",
			QualName: "store_ladybug.Store",
			FilePath: "internal/graph/store_ladybug/store.go",
			Language: "go",
			Meta:     map[string]any{"signature": "type Store struct"},
		},
		{
			ID:       "internal/auth/token.go::ValidateToken",
			Kind:     graph.KindFunction,
			Name:     "ValidateToken",
			QualName: "auth.ValidateToken",
			FilePath: "internal/auth/token.go",
			Language: "go",
			Meta:     map[string]any{"signature": "func ValidateToken(t string) error"},
		},
	}
	items := make([]graph.SymbolFTSItem, 0, len(corpus))
	for _, n := range corpus {
		s.AddNode(n)
		tok := realisticTokens(n)
		t.Logf("seed %-65s tokens=%q", n.ID, tok)
		items = append(items, graph.SymbolFTSItem{NodeID: n.ID, Tokens: tok})
	}
	if err := s.BulkUpsertSymbolFTS("", items); err != nil {
		t.Fatalf("bulk: %v", err)
	}
	if err := s.BuildSymbolIndex(); err != nil {
		t.Fatalf("build: %v", err)
	}

	for _, q := range []string{
		"NewServer",
		"IndexAll",
		"handleStreamable",
		"ValidateToken",
		"Store",
		"server",
		"index all",
		"new server",
		"validate token",
	} {
		hits, err := s.SearchSymbols(q, 20)
		if err != nil {
			t.Logf("FAIL %q: %v", q, err)
			continue
		}
		t.Logf("SearchSymbols(%q) → %d hits", q, len(hits))
		for _, h := range hits {
			t.Logf("  %s  score=%.4f", h.NodeID, h.Score)
		}
	}

	// Verify STARTS WITH works for selective wipes: this is the
	// primitive the multi-repo BulkUpsertSymbolFTS fix relies on.
	rows, err := querySelectSafe(s, `MATCH (f:SymbolFTS) WHERE f.id STARTS WITH $p RETURN f.id`, map[string]any{
		"p": "internal/mcp/",
	})
	if err != nil {
		t.Logf("STARTS WITH probe err: %v", err)
	} else {
		t.Logf("STARTS WITH 'internal/mcp/' → %d rows", len(rows))
		for _, r := range rows {
			t.Logf("  %v", r)
		}
	}
}
