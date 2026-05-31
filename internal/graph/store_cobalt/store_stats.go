package store_cobalt

import (
	"github.com/zzet/gortex/internal/graph"
)

// Approximate in-memory footprint of one node / edge. Used only to size
// RepoMemoryEstimate; these are deliberate rough constants, not measured.
const (
	perNodeBytes = 240 // approx in-memory footprint per node
	perEdgeBytes = 144 // approx in-memory footprint per edge
)

// NodeCount returns the total number of node rows.
func (s *Store) NodeCount() int {
	return s.queryCount("SELECT count(*) FROM nodes")
}

// EdgeCount returns the total number of edge rows.
func (s *Store) EdgeCount() int {
	return s.queryCount("SELECT count(*) FROM edges")
}

// Stats returns whole-graph totals plus per-kind and per-language node breakdowns.
func (s *Store) Stats() graph.GraphStats {
	byKind := make(map[string]int)
	if rows, err := s.db.Query(s.ctx, "SELECT kind, count(*) FROM nodes GROUP BY kind"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var k string
			var c int64
			if err := rows.Scan(&k, &c); err != nil {
				break
			}
			byKind[k] = int(c)
		}
	}
	byLang := make(map[string]int)
	if rows, err := s.db.Query(s.ctx, "SELECT language, count(*) FROM nodes GROUP BY language"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var l string
			var c int64
			if err := rows.Scan(&l, &c); err != nil {
				break
			}
			byLang[l] = int(c)
		}
	}
	return graph.GraphStats{
		TotalNodes: s.NodeCount(),
		TotalEdges: s.EdgeCount(),
		ByKind:     byKind,
		ByLanguage: byLang,
	}
}

// RepoStats returns per-repo node/edge totals and kind/language breakdowns, keyed by repo_prefix.
func (s *Store) RepoStats() map[string]graph.GraphStats {
	tmp := make(map[string]*graph.GraphStats)
	ensure := func(p string) *graph.GraphStats {
		st := tmp[p]
		if st == nil {
			st = &graph.GraphStats{
				ByKind:     make(map[string]int),
				ByLanguage: make(map[string]int),
			}
			tmp[p] = st
		}
		return st
	}

	if rows, err := s.db.Query(s.ctx, "SELECT repo_prefix, kind, count(*) FROM nodes WHERE repo_prefix <> '' GROUP BY repo_prefix, kind"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var repo, kind string
			var c int64
			if err := rows.Scan(&repo, &kind, &c); err != nil {
				break
			}
			st := ensure(repo)
			st.ByKind[kind] += int(c)
			st.TotalNodes += int(c)
		}
	}

	if rows, err := s.db.Query(s.ctx, "SELECT repo_prefix, language, count(*) FROM nodes WHERE repo_prefix <> '' AND language <> '' GROUP BY repo_prefix, language"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var repo, lang string
			var c int64
			if err := rows.Scan(&repo, &lang, &c); err != nil {
				break
			}
			ensure(repo).ByLanguage[lang] += int(c)
		}
	}

	if rows, err := s.db.Query(s.ctx, "SELECT n.repo_prefix, count(*) FROM edges e JOIN nodes n ON e.from_id = n.id WHERE n.repo_prefix <> '' GROUP BY n.repo_prefix"); err == nil {
		defer rows.Close()
		for rows.Next() {
			var repo string
			var c int64
			if err := rows.Scan(&repo, &c); err != nil {
				break
			}
			ensure(repo).TotalEdges = int(c)
		}
	}

	out := make(map[string]graph.GraphStats, len(tmp))
	for p, st := range tmp {
		out[p] = *st
	}
	return out
}

// RepoPrefixes returns the distinct non-empty repo prefixes present in the graph.
func (s *Store) RepoPrefixes() []string {
	return s.queryStrings("SELECT DISTINCT repo_prefix FROM nodes WHERE repo_prefix <> ''")
}

// EdgeIdentityRevisions returns the provenance-bearing identity-change counter.
func (s *Store) EdgeIdentityRevisions() int {
	return int(s.edgeRevs.Load())
}

// VerifyEdgeIdentities is a no-op for the SQL backend: a single canonical row
// per edge identity means the out/in adjacency views cannot diverge, so there
// is nothing to verify.
func (s *Store) VerifyEdgeIdentities() error {
	return nil
}

// RepoMemoryEstimate returns an approximate in-memory footprint for one repo's nodes and edges.
func (s *Store) RepoMemoryEstimate(repoPrefix string) graph.RepoMemoryEstimate {
	nc := s.queryCount("SELECT count(*) FROM nodes WHERE repo_prefix = ?", repoPrefix)
	ec := s.queryCount("SELECT count(*) FROM edges e JOIN nodes n ON e.from_id = n.id WHERE n.repo_prefix = ?", repoPrefix)
	return graph.RepoMemoryEstimate{
		NodeCount: nc,
		EdgeCount: ec,
		NodeBytes: uint64(nc) * perNodeBytes,
		EdgeBytes: uint64(ec) * perEdgeBytes,
	}
}

// AllRepoMemoryEstimates returns the memory estimate for every non-empty repo prefix.
func (s *Store) AllRepoMemoryEstimates() map[string]graph.RepoMemoryEstimate {
	out := make(map[string]graph.RepoMemoryEstimate)
	for _, p := range s.RepoPrefixes() {
		out[p] = s.RepoMemoryEstimate(p)
	}
	return out
}
