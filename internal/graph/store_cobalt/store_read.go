package store_cobalt

import (
	"fmt"
	"iter"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// selNodes returns the SELECT prefix projecting nodeSelectCols from the
// nodes table; callers append their own WHERE/ORDER/LIMIT clause.
func selNodes() string { return "SELECT " + nodeSelectCols + " FROM nodes " }

// selEdges returns the SELECT prefix projecting edgeSelectCols from the
// edges table; callers append their own WHERE/ORDER/LIMIT clause.
func selEdges() string { return "SELECT " + edgeSelectCols + " FROM edges " }

// --- point lookups -----------------------------------------------------

// GetNode returns the node with the given id, or nil if absent.
func (s *Store) GetNode(id string) *graph.Node {
	ns := s.queryNodes(selNodes()+"WHERE id=? LIMIT 1", id)
	if len(ns) > 0 {
		return ns[0]
	}
	return nil
}

// GetNodeByQualName returns the node with the given fully-qualified name,
// or nil if absent (or qualName is empty).
func (s *Store) GetNodeByQualName(qualName string) *graph.Node {
	if qualName == "" {
		return nil
	}
	ns := s.queryNodes(selNodes()+"WHERE qual_name=? LIMIT 1", qualName)
	if len(ns) > 0 {
		return ns[0]
	}
	return nil
}

// GetNodesByQualNames returns a map from qualified name to the first node
// carrying it, for every requested name that resolves.
func (s *Store) GetNodesByQualNames(qualNames []string) map[string]*graph.Node {
	out := make(map[string]*graph.Node)
	names := dedupeStrings(qualNames)
	if len(names) == 0 {
		return out
	}
	for _, chunk := range chunkStrings(names, idChunkSize) {
		ns := s.queryNodes(selNodes()+"WHERE qual_name IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		for _, n := range ns {
			if _, ok := out[n.QualName]; !ok {
				out[n.QualName] = n
			}
		}
	}
	return out
}

// --- name / scope ------------------------------------------------------

// FindNodesByName returns every node whose unqualified name matches name,
// ordered by id for a deterministic result.
func (s *Store) FindNodesByName(name string) []*graph.Node {
	return s.queryNodes(selNodes()+"WHERE name=? ORDER BY id", name)
}

// FindNodesByNameInRepo returns nodes named name within the given repo,
// ordered by id.
func (s *Store) FindNodesByNameInRepo(name, repoPrefix string) []*graph.Node {
	return s.queryNodes(selNodes()+"WHERE name=? AND repo_prefix=? ORDER BY id", name, repoPrefix)
}

// FindNodesByNameContaining returns nodes whose name contains substr
// (case-insensitive), ordered by id. A limit > 0 caps the result count.
func (s *Store) FindNodesByNameContaining(substr string, limit int) []*graph.Node {
	// An empty substring matches nothing (mirrors the in-memory backend),
	// rather than the match-everything semantics of `LIKE '%%'`.
	if substr == "" {
		return nil
	}
	lower := strings.ToLower(substr)
	// CobaltDB's LIKE treats `_` and `%` as wildcards, and its lexer rejects
	// the `ESCAPE '\'` clause, so the metacharacters cannot be escaped in the
	// engine. To preserve the literal-substring contract (parity with the
	// in-memory strings.Contains), the LIKE fetches a superset which is then
	// filtered literally in Go. When substr carries no LIKE metacharacter the
	// LIKE is already exact, so the SQL LIMIT is safe and avoids
	// materialising the whole match set.
	hasMeta := strings.ContainsAny(lower, "%_")
	q := selNodes() + "WHERE name_lower LIKE ? ORDER BY id"
	if limit > 0 && !hasMeta {
		// CobaltDB ignores a parameterized `LIMIT ?`, so inline the integer
		// (limit is an int, never user text — safe to format in).
		q += fmt.Sprintf(" LIMIT %d", limit)
	}
	cands := s.queryNodes(q, "%"+lower+"%")
	if !hasMeta {
		return cands
	}
	out := make([]*graph.Node, 0, len(cands))
	for _, n := range cands {
		if strings.Contains(strings.ToLower(n.Name), lower) {
			out = append(out, n)
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out
}

// GetFileNodes returns every node declared in the given file.
func (s *Store) GetFileNodes(filePath string) []*graph.Node {
	return s.queryNodes(selNodes()+"WHERE file_path=?", filePath)
}

// GetRepoNodes returns every node in the given repo.
func (s *Store) GetRepoNodes(repoPrefix string) []*graph.Node {
	return s.queryNodes(selNodes()+"WHERE repo_prefix=?", repoPrefix)
}

// --- edge adjacency ----------------------------------------------------

// GetOutEdges returns every edge whose source is nodeID.
func (s *Store) GetOutEdges(nodeID string) []*graph.Edge {
	return s.queryEdges(selEdges()+"WHERE from_id=?", nodeID)
}

// GetInEdges returns every edge whose target is nodeID.
func (s *Store) GetInEdges(nodeID string) []*graph.Edge {
	return s.queryEdges(selEdges()+"WHERE to_id=?", nodeID)
}

// GetOutEdgesByNodeIDs returns outgoing edges for each id, keyed by source
// node id.
func (s *Store) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	out := make(map[string][]*graph.Edge)
	d := dedupeStrings(ids)
	if len(d) == 0 {
		return out
	}
	for _, chunk := range chunkStrings(d, idChunkSize) {
		es := s.queryEdges(selEdges()+"WHERE from_id IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		for _, e := range es {
			out[e.From] = append(out[e.From], e)
		}
	}
	return out
}

// GetInEdgesByNodeIDs returns incoming edges for each id, keyed by target
// node id.
func (s *Store) GetInEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	out := make(map[string][]*graph.Edge)
	d := dedupeStrings(ids)
	if len(d) == 0 {
		return out
	}
	for _, chunk := range chunkStrings(d, idChunkSize) {
		es := s.queryEdges(selEdges()+"WHERE to_id IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		for _, e := range es {
			out[e.To] = append(out[e.To], e)
		}
	}
	return out
}

// GetRepoEdges returns every edge whose source node belongs to the given
// repo.
func (s *Store) GetRepoEdges(repoPrefix string) []*graph.Edge {
	if repoPrefix == "" {
		return nil
	}
	ids := s.queryStrings("SELECT id FROM nodes WHERE repo_prefix=?", repoPrefix)
	if len(ids) == 0 {
		return nil
	}
	var out []*graph.Edge
	for _, chunk := range chunkStrings(ids, idChunkSize) {
		out = append(out, s.queryEdges(selEdges()+"WHERE from_id IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)...)
	}
	return out
}

// --- bulk reads --------------------------------------------------------

// AllNodes returns every node in the store.
func (s *Store) AllNodes() []*graph.Node { return s.queryNodes(selNodes()) }

// AllEdges returns every edge in the store.
func (s *Store) AllEdges() []*graph.Edge { return s.queryEdges(selEdges()) }

// --- iterators ---------------------------------------------------------

// EdgesByKind iterates every edge of the given kind, honouring early-stop.
func (s *Store) EdgesByKind(kind graph.EdgeKind) iter.Seq[*graph.Edge] {
	return func(yield func(*graph.Edge) bool) {
		for _, e := range s.queryEdges(selEdges()+"WHERE kind=?", string(kind)) {
			if !yield(e) {
				return
			}
		}
	}
}

// NodesByKind iterates every node of the given kind, honouring early-stop.
func (s *Store) NodesByKind(kind graph.NodeKind) iter.Seq[*graph.Node] {
	return func(yield func(*graph.Node) bool) {
		for _, n := range s.queryNodes(selNodes()+"WHERE kind=?", string(kind)) {
			if !yield(n) {
				return
			}
		}
	}
}

// EdgesWithUnresolvedTarget iterates every edge pointing at an unresolved
// target — both the bare `unresolved::X` and prefixed
// `<repo>::unresolved::X` forms — honouring early-stop.
func (s *Store) EdgesWithUnresolvedTarget() iter.Seq[*graph.Edge] {
	return func(yield func(*graph.Edge) bool) {
		es := s.queryEdges(selEdges() + "WHERE to_id LIKE 'unresolved::%' OR to_id LIKE '%::unresolved::%'")
		for _, e := range es {
			if !yield(e) {
				return
			}
		}
	}
}

// --- batched lookups ---------------------------------------------------

// GetNodesByIDs returns a map from id to node for every requested id that
// resolves.
func (s *Store) GetNodesByIDs(ids []string) map[string]*graph.Node {
	out := make(map[string]*graph.Node)
	d := dedupeStrings(ids)
	if len(d) == 0 {
		return out
	}
	for _, chunk := range chunkStrings(d, idChunkSize) {
		ns := s.queryNodes(selNodes()+"WHERE id IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		for _, n := range ns {
			out[n.ID] = n
		}
	}
	return out
}

// FindNodesByNames returns a map from unqualified name to the nodes
// carrying it (exact, case-sensitive) for every requested name.
func (s *Store) FindNodesByNames(names []string) map[string][]*graph.Node {
	out := make(map[string][]*graph.Node)
	d := dedupeStrings(names)
	if len(d) == 0 {
		return out
	}
	for _, chunk := range chunkStrings(d, idChunkSize) {
		ns := s.queryNodes(selNodes()+"WHERE name IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		for _, n := range ns {
			out[n.Name] = append(out[n.Name], n)
		}
	}
	return out
}
