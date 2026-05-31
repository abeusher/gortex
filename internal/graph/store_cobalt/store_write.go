package store_cobalt

import (
	"fmt"

	"github.com/zzet/gortex/internal/graph"
)

// AddNode upserts a single node by id (INSERT OR REPLACE).
func (s *Store) AddNode(n *graph.Node) {
	if n == nil || n.ID == "" {
		return
	}
	if s.stageIfBulk([]*graph.Node{n}, nil) {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mustExec(buildInsert("nodes", nodeInsertCols, nodeInsertCount, 1), nodeValues(n)...)
}

// AddEdge upserts a single edge by its identity key (INSERT OR REPLACE).
// Endpoint node rows are not synthesised: edges reference node ids freely.
func (s *Store) AddEdge(e *graph.Edge) {
	if e == nil {
		return
	}
	if s.stageIfBulk(nil, []*graph.Edge{e}) {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mustExec(buildInsert("edges", edgeInsertCols, edgeInsertCount, 1), edgeValues(e)...)
}

// AddBatch upserts nodes then edges via byte-budgeted multi-row INSERT OR
// REPLACE statements (bounded transactions). Nil entries are skipped. The
// shared bulkInsert path keeps every statement's WAL record under CobaltDB's
// per-record cap regardless of row size.
func (s *Store) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	if len(nodes) == 0 && len(edges) == 0 {
		return
	}
	if s.stageIfBulk(nodes, edges) {
		return
	}

	nodeRows := make([][]any, 0, len(nodes))
	for _, n := range nodes {
		if n != nil && n.ID != "" {
			nodeRows = append(nodeRows, nodeValues(n))
		}
	}
	edgeRows := make([][]any, 0, len(edges))
	for _, e := range edges {
		if e != nil {
			edgeRows = append(edgeRows, edgeValues(e))
		}
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.bulkInsert("nodes", nodeInsertCols, nodeInsertCount, nodeRows); err != nil {
		panic(fmt.Sprintf("store_cobalt AddBatch node insert failed: %v", err))
	}
	if err := s.bulkInsert("edges", edgeInsertCols, edgeInsertCount, edgeRows); err != nil {
		panic(fmt.Sprintf("store_cobalt AddBatch edge insert failed: %v", err))
	}
}

// RemoveEdge deletes exactly one edge matching (from, to, kind), mirroring
// the in-memory graph's first-match removal. Returns false if none exists.
func (s *Store) RemoveEdge(from, to string, kind graph.EdgeKind) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	rows, err := s.db.Query(s.ctx, "SELECT edge_key FROM edges WHERE from_id=? AND to_id=? AND kind=? LIMIT 1", from, to, string(kind))
	if err != nil {
		return false
	}
	var key string
	found := rows.Next()
	if found {
		_ = rows.Scan(&key)
	}
	rows.Close()
	if !found {
		return false
	}
	s.mustExec("DELETE FROM edges WHERE edge_key=?", key)
	return true
}

// SetEdgeProvenance rewrites the origin (and re-derived tier) of one edge,
// mutating the passed *Edge in place. Returns false if the origin is
// unchanged. Bumps the edge-identity revision counter on change.
func (s *Store) SetEdgeProvenance(e *graph.Edge, newOrigin string) bool {
	if e == nil {
		return false
	}
	if e.Origin == newOrigin {
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	newTier := e.Tier
	if e.Tier != "" {
		newTier = graph.ResolvedBy(newOrigin)
	}
	s.mustExec("UPDATE edges SET origin=?, tier=? WHERE edge_key=?", newOrigin, newTier, edgeKeyOf(e))
	e.Origin = newOrigin
	e.Tier = newTier
	s.edgeRevs.Add(1)
	return true
}

// SetEdgeProvenanceBatch applies a batch of provenance updates, returning
// the number of edges actually changed. Each update locks independently.
func (s *Store) SetEdgeProvenanceBatch(batch []graph.EdgeProvenanceUpdate) (changed int) {
	for _, u := range batch {
		if u.Edge == nil {
			continue
		}
		if s.SetEdgeProvenance(u.Edge, u.NewOrigin) {
			changed++
		}
	}
	return changed
}

// ReindexEdge moves an edge to a new target by deleting its old-To row and
// inserting the (already-mutated) edge under its new key. No-op if To is
// unchanged.
func (s *Store) ReindexEdge(e *graph.Edge, oldTo string) {
	if oldTo == e.To {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	oldKey := edgeKeyFor(e.From, oldTo, e.Kind, e.FilePath, e.Line)
	s.mustExec("DELETE FROM edges WHERE edge_key=?", oldKey)
	s.mustExec(buildInsert("edges", edgeInsertCols, edgeInsertCount, 1), edgeValues(e)...)
}

// ReindexEdges applies a batch of edge re-targetings. Each entry locks
// independently via ReindexEdge.
func (s *Store) ReindexEdges(batch []graph.EdgeReindex) {
	for _, r := range batch {
		if r.Edge == nil {
			continue
		}
		s.ReindexEdge(r.Edge, r.OldTo)
	}
}

// EvictFile removes every node defined in filePath plus all edges touching
// those nodes (on either endpoint). Returns the counts removed.
func (s *Store) EvictFile(filePath string) (nodesRemoved, edgesRemoved int) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.evictByColumn("file_path", filePath)
}

// EvictRepo removes every node in repoPrefix plus all edges touching those
// nodes (on either endpoint). Returns the counts removed.
func (s *Store) EvictRepo(repoPrefix string) (nodesRemoved, edgesRemoved int) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.evictByColumn("repo_prefix", repoPrefix)
}

// evictByColumn deletes all nodes whose column equals value, plus every
// edge incident to a removed node. The caller holds writeMu. column is a
// fixed identifier ("file_path"/"repo_prefix"), safe to interpolate.
func (s *Store) evictByColumn(column, value string) (nodesRemoved, edgesRemoved int) {
	ids := s.queryStrings("SELECT id FROM nodes WHERE "+column+"=?", value)
	nodesRemoved = len(ids)
	if nodesRemoved == 0 {
		return 0, 0
	}

	keySet := map[string]struct{}{}
	for _, chunk := range chunkStrings(ids, idChunkSize) {
		ph := placeholders(len(chunk))
		args := strArgs(chunk)
		for _, k := range s.queryStrings("SELECT edge_key FROM edges WHERE from_id IN ("+ph+")", args...) {
			keySet[k] = struct{}{}
		}
		for _, k := range s.queryStrings("SELECT edge_key FROM edges WHERE to_id IN ("+ph+")", args...) {
			keySet[k] = struct{}{}
		}
	}
	edgesRemoved = len(keySet)

	if edgesRemoved > 0 {
		keys := make([]string, 0, len(keySet))
		for k := range keySet {
			keys = append(keys, k)
		}
		for _, chunk := range chunkStrings(keys, idChunkSize) {
			s.mustExec("DELETE FROM edges WHERE edge_key IN ("+placeholders(len(chunk))+")", strArgs(chunk)...)
		}
	}

	s.mustExec("DELETE FROM nodes WHERE "+column+"=?", value)
	return nodesRemoved, edgesRemoved
}
