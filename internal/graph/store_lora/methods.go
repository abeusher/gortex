//go:build lora


package store_lora

import (
	"fmt"
	"iter"

	lora "github.com/lora-db/lora/crates/bindings/lora-go"

	"github.com/zzet/gortex/internal/graph"
)

// -- writes --------------------------------------------------------------

const upsertNodeCypher = `
MERGE (n:Node {id: $id})
SET n.kind = $kind, n.name = $name, n.qual_name = $qual_name,
    n.file_path = $file_path, n.start_line = $start_line, n.end_line = $end_line,
    n.language = $language, n.repo_prefix = $repo_prefix,
    n.workspace_id = $workspace_id, n.project_id = $project_id,
    n.abs_path = $abs_path, n.meta = $meta`

// AddNode upserts a node.
func (s *Store) AddNode(n *graph.Node) {
	if n == nil || n.ID == "" {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.upsertNodeLocked(n)
}

func (s *Store) upsertNodeLocked(n *graph.Node) {
	p, err := nodeParams(n)
	if err != nil {
		panicOnFatal(err)
		return
	}
	if _, err := s.db.Execute(upsertNodeCypher, p); err != nil {
		panicOnFatal(fmt.Errorf("upsert node: %w", err))
	}
}

const upsertEdgeCypher = `
MERGE (a:Node {id: $from_id})
MERGE (b:Node {id: $to_id})
MERGE (a)-[e:EDGE {e_kind: $e_kind, file_path: $file_path, line: $line}]->(b)
SET e.confidence = $confidence, e.confidence_label = $confidence_label,
    e.origin = $origin, e.tier = $tier, e.cross_repo = $cross_repo, e.meta = $meta`

func (s *Store) AddEdge(e *graph.Edge) {
	if e == nil {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.upsertEdgeLocked(e)
}

func (s *Store) upsertEdgeLocked(e *graph.Edge) {
	metaStr, merr := encodeMeta(e.Meta)
	if merr != nil {
		panicOnFatal(merr)
		return
	}
	if _, err := s.db.Execute(upsertEdgeCypher, lora.Params{
		"from_id":          e.From,
		"to_id":            e.To,
		"e_kind":           string(e.Kind),
		"file_path":        e.FilePath,
		"line":             int64(e.Line),
		"confidence":       e.Confidence,
		"confidence_label": e.ConfidenceLabel,
		"origin":           e.Origin,
		"tier":             e.Tier,
		"cross_repo":       e.CrossRepo,
		"meta":             metaStr,
	}); err != nil {
		panicOnFatal(fmt.Errorf("upsert edge: %w", err))
	}
}

// loraBatchChunkSize is the number of rows per UNWIND-driven Cypher
// statement. The whole chunk goes through one parse+plan+execute
// instead of N. 5000 matches the Kuzu chunk shape.
const loraBatchChunkSize = 5000

const unwindUpsertNodeCypher = `
UNWIND $rows AS row
MERGE (n:Node {id: row.id})
SET n.kind = row.kind, n.name = row.name, n.qual_name = row.qual_name,
    n.file_path = row.file_path, n.start_line = row.start_line,
    n.end_line = row.end_line, n.language = row.language,
    n.repo_prefix = row.repo_prefix, n.workspace_id = row.workspace_id,
    n.project_id = row.project_id, n.abs_path = row.abs_path,
    n.meta = row.meta`

const unwindUpsertEdgeCypher = `
UNWIND $rows AS row
MERGE (a:Node {id: row.from_id})
MERGE (b:Node {id: row.to_id})
MERGE (a)-[e:EDGE {e_kind: row.e_kind, file_path: row.file_path, line: row.line}]->(b)
SET e.confidence = row.confidence, e.confidence_label = row.confidence_label,
    e.origin = row.origin, e.tier = row.tier, e.cross_repo = row.cross_repo,
    e.meta = row.meta`

// AddBatch fans node and edge inserts into UNWIND-driven Cypher
// statements — one Execute per ≤loraBatchChunkSize rows instead of
// one per record. Without UNWIND, per-call MERGE pays a full
// parse+plan+execute per record (~1-2 ms each); at indexer scale
// that's tens of minutes of pure binding overhead. UNWIND collapses
// N MERGEs into one statement.
func (s *Store) AddBatch(nodes []*graph.Node, edges []*graph.Edge) {
	if len(nodes) == 0 && len(edges) == 0 {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.addNodesUnwindLocked(nodes)
	s.addEdgesUnwindLocked(edges)
}

func (s *Store) addNodesUnwindLocked(nodes []*graph.Node) {
	for i := 0; i < len(nodes); i += loraBatchChunkSize {
		end := i + loraBatchChunkSize
		if end > len(nodes) {
			end = len(nodes)
		}
		chunk := nodes[i:end]
		rows := make([]map[string]any, 0, len(chunk))
		for _, n := range chunk {
			if n == nil || n.ID == "" {
				continue
			}
			metaStr, err := encodeMeta(n.Meta)
			if err != nil {
				panicOnFatal(err)
				return
			}
			rows = append(rows, map[string]any{
				"id":           n.ID,
				"kind":         string(n.Kind),
				"name":         n.Name,
				"qual_name":    n.QualName,
				"file_path":    n.FilePath,
				"start_line":   int64(n.StartLine),
				"end_line":     int64(n.EndLine),
				"language":     n.Language,
				"repo_prefix":  n.RepoPrefix,
				"workspace_id": n.WorkspaceID,
				"project_id":   n.ProjectID,
				"abs_path":     n.AbsoluteFilePath,
				"meta":         metaStr,
			})
		}
		if len(rows) == 0 {
			continue
		}
		if _, err := s.db.Execute(unwindUpsertNodeCypher, lora.Params{"rows": rows}); err != nil {
			panicOnFatal(fmt.Errorf("unwind nodes: %w", err))
		}
	}
}

func (s *Store) addEdgesUnwindLocked(edges []*graph.Edge) {
	for i := 0; i < len(edges); i += loraBatchChunkSize {
		end := i + loraBatchChunkSize
		if end > len(edges) {
			end = len(edges)
		}
		chunk := edges[i:end]
		rows := make([]map[string]any, 0, len(chunk))
		for _, e := range chunk {
			if e == nil {
				continue
			}
			metaStr, err := encodeMeta(e.Meta)
			if err != nil {
				panicOnFatal(err)
				return
			}
			rows = append(rows, map[string]any{
				"from_id":          e.From,
				"to_id":            e.To,
				"e_kind":           string(e.Kind),
				"file_path":        e.FilePath,
				"line":             int64(e.Line),
				"confidence":       e.Confidence,
				"confidence_label": e.ConfidenceLabel,
				"origin":           e.Origin,
				"tier":             e.Tier,
				"cross_repo":       e.CrossRepo,
				"meta":             metaStr,
			})
		}
		if len(rows) == 0 {
			continue
		}
		if _, err := s.db.Execute(unwindUpsertEdgeCypher, lora.Params{"rows": rows}); err != nil {
			panicOnFatal(fmt.Errorf("unwind edges: %w", err))
		}
	}
}

func (s *Store) SetEdgeProvenance(e *graph.Edge, newOrigin string) bool {
	if e == nil {
		return false
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.setEdgeProvenanceLocked(e, newOrigin)
}

func (s *Store) setEdgeProvenanceLocked(e *graph.Edge, newOrigin string) bool {
	const sel = `
MATCH (a:Node {id: $from})-[e:EDGE {e_kind: $kind, file_path: $file, line: $line}]->(b:Node {id: $to})
RETURN e.origin AS origin LIMIT 1`
	res, err := s.db.Execute(sel, lora.Params{
		"from": e.From, "to": e.To, "kind": string(e.Kind),
		"file": e.FilePath, "line": int64(e.Line),
	})
	if err != nil || res == nil || len(res.Rows) == 0 {
		return false
	}
	stored := asString(res.Rows[0]["origin"])
	if stored == newOrigin {
		return false
	}
	newTier := e.Tier
	if newTier != "" {
		newTier = graph.ResolvedBy(newOrigin)
	}
	const upd = `
MATCH (a:Node {id: $from})-[e:EDGE {e_kind: $kind, file_path: $file, line: $line}]->(b:Node {id: $to})
SET e.origin = $origin, e.tier = $tier`
	if _, err := s.db.Execute(upd, lora.Params{
		"from": e.From, "to": e.To, "kind": string(e.Kind),
		"file": e.FilePath, "line": int64(e.Line),
		"origin": newOrigin, "tier": newTier,
	}); err != nil {
		return false
	}
	e.Origin = newOrigin
	if e.Tier != "" {
		e.Tier = newTier
	}
	s.edgeIdentityRevs.Add(1)
	return true
}

func (s *Store) SetEdgeProvenanceBatch(batch []graph.EdgeProvenanceUpdate) int {
	if len(batch) == 0 {
		return 0
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	changed := 0
	for _, u := range batch {
		if u.Edge == nil {
			continue
		}
		if s.setEdgeProvenanceLocked(u.Edge, u.NewOrigin) {
			changed++
		}
	}
	return changed
}

func (s *Store) ReindexEdge(e *graph.Edge, oldTo string) {
	if e == nil || oldTo == e.To {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.reindexEdgeLocked(e, oldTo)
}

func (s *Store) reindexEdgeLocked(e *graph.Edge, oldTo string) {
	const del = `
MATCH (a:Node {id: $from})-[e:EDGE {e_kind: $kind, file_path: $file, line: $line}]->(b:Node {id: $oldTo})
DELETE e`
	if _, err := s.db.Execute(del, lora.Params{
		"from": e.From, "oldTo": oldTo, "kind": string(e.Kind),
		"file": e.FilePath, "line": int64(e.Line),
	}); err != nil {
		// Not fatal — the row may already be absent.
	}
	s.upsertEdgeLocked(e)
	s.edgeIdentityRevs.Add(1)
}

func (s *Store) ReindexEdges(batch []graph.EdgeReindex) {
	if len(batch) == 0 {
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, r := range batch {
		if r.Edge == nil || r.OldTo == r.Edge.To {
			continue
		}
		s.reindexEdgeLocked(r.Edge, r.OldTo)
	}
}

func (s *Store) RemoveEdge(from, to string, kind graph.EdgeKind) bool {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	const q = `
MATCH (a:Node {id: $from})-[e:EDGE {e_kind: $kind}]->(b:Node {id: $to})
DELETE e RETURN count(e) AS n`
	res, err := s.db.Execute(q, lora.Params{
		"from": from, "to": to, "kind": string(kind),
	})
	if err != nil || res == nil || len(res.Rows) == 0 {
		return false
	}
	return asInt(res.Rows[0]["n"]) > 0
}

func (s *Store) EvictFile(filePath string) (int, int) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	// Count + delete edges incident to nodes with this file_path, plus
	// edges whose own file_path matches.
	const eq = `
MATCH (a:Node)-[e:EDGE]->(b:Node)
WHERE a.file_path = $fp OR b.file_path = $fp OR e.file_path = $fp
DELETE e RETURN count(e) AS n`
	er, _ := s.db.Execute(eq, lora.Params{"fp": filePath})
	edgesRemoved := 0
	if er != nil && len(er.Rows) > 0 {
		edgesRemoved = asInt(er.Rows[0]["n"])
	}
	const nq = `
MATCH (n:Node {file_path: $fp})
DELETE n RETURN count(n) AS n`
	nr, _ := s.db.Execute(nq, lora.Params{"fp": filePath})
	nodesRemoved := 0
	if nr != nil && len(nr.Rows) > 0 {
		nodesRemoved = asInt(nr.Rows[0]["n"])
	}
	return nodesRemoved, edgesRemoved
}

func (s *Store) EvictRepo(repoPrefix string) (int, int) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	const eq = `
MATCH (a:Node)-[e:EDGE]->(b:Node)
WHERE a.repo_prefix = $rp OR b.repo_prefix = $rp
DELETE e RETURN count(e) AS n`
	er, _ := s.db.Execute(eq, lora.Params{"rp": repoPrefix})
	edgesRemoved := 0
	if er != nil && len(er.Rows) > 0 {
		edgesRemoved = asInt(er.Rows[0]["n"])
	}
	const nq = `
MATCH (n:Node {repo_prefix: $rp})
DELETE n RETURN count(n) AS n`
	nr, _ := s.db.Execute(nq, lora.Params{"rp": repoPrefix})
	nodesRemoved := 0
	if nr != nil && len(nr.Rows) > 0 {
		nodesRemoved = asInt(nr.Rows[0]["n"])
	}
	return nodesRemoved, edgesRemoved
}

// -- reads ---------------------------------------------------------------

const nodeReturnFields = `n.id AS id, n.kind AS kind, n.name AS name,
    n.qual_name AS qual_name, n.file_path AS file_path,
    n.start_line AS start_line, n.end_line AS end_line,
    n.language AS language, n.repo_prefix AS repo_prefix,
    n.workspace_id AS workspace_id, n.project_id AS project_id,
    n.abs_path AS abs_path, n.meta AS meta`

const edgeReturnFields = `a.id AS from_id, b.id AS to_id,
    e.e_kind AS e_kind, e.file_path AS file_path, e.line AS line,
    e.confidence AS confidence, e.confidence_label AS confidence_label,
    e.origin AS origin, e.tier AS tier, e.cross_repo AS cross_repo,
    e.meta AS meta`

func (s *Store) GetNode(id string) *graph.Node {
	if id == "" {
		return nil
	}
	q := `MATCH (n:Node {id: $id}) RETURN ` + nodeReturnFields + ` LIMIT 1`
	res, err := s.db.Execute(q, lora.Params{"id": id})
	if err != nil || res == nil || len(res.Rows) == 0 {
		return nil
	}
	return rowToNode(res.Rows[0])
}

func (s *Store) GetNodeByQualName(qualName string) *graph.Node {
	if qualName == "" {
		return nil
	}
	q := `MATCH (n:Node {qual_name: $q}) RETURN ` + nodeReturnFields + ` LIMIT 1`
	res, err := s.db.Execute(q, lora.Params{"q": qualName})
	if err != nil || res == nil || len(res.Rows) == 0 {
		return nil
	}
	return rowToNode(res.Rows[0])
}

func (s *Store) FindNodesByName(name string) []*graph.Node {
	if name == "" {
		return nil
	}
	q := `MATCH (n:Node {name: $n}) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"n": name})
	if res == nil {
		return nil
	}
	out := make([]*graph.Node, 0, len(res.Rows))
	for _, r := range res.Rows {
		if n := rowToNode(r); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) FindNodesByNameInRepo(name, repoPrefix string) []*graph.Node {
	if name == "" {
		return nil
	}
	q := `MATCH (n:Node {name: $n, repo_prefix: $r}) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"n": name, "r": repoPrefix})
	if res == nil {
		return nil
	}
	out := make([]*graph.Node, 0, len(res.Rows))
	for _, r := range res.Rows {
		if n := rowToNode(r); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) GetFileNodes(filePath string) []*graph.Node {
	if filePath == "" {
		return nil
	}
	q := `MATCH (n:Node {file_path: $fp}) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"fp": filePath})
	if res == nil {
		return nil
	}
	out := make([]*graph.Node, 0, len(res.Rows))
	for _, r := range res.Rows {
		if n := rowToNode(r); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) GetRepoNodes(repoPrefix string) []*graph.Node {
	q := `MATCH (n:Node {repo_prefix: $r}) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"r": repoPrefix})
	if res == nil {
		return nil
	}
	out := make([]*graph.Node, 0, len(res.Rows))
	for _, r := range res.Rows {
		if n := rowToNode(r); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) GetOutEdges(nodeID string) []*graph.Edge {
	if nodeID == "" {
		return nil
	}
	q := `MATCH (a:Node {id: $id})-[e:EDGE]->(b:Node) RETURN ` + edgeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"id": nodeID})
	if res == nil {
		return nil
	}
	out := make([]*graph.Edge, 0, len(res.Rows))
	for _, r := range res.Rows {
		if e := rowToEdge(r); e != nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) GetInEdges(nodeID string) []*graph.Edge {
	if nodeID == "" {
		return nil
	}
	q := `MATCH (a:Node)-[e:EDGE]->(b:Node {id: $id}) RETURN ` + edgeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"id": nodeID})
	if res == nil {
		return nil
	}
	out := make([]*graph.Edge, 0, len(res.Rows))
	for _, r := range res.Rows {
		if e := rowToEdge(r); e != nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) AllNodes() []*graph.Node {
	q := `MATCH (n:Node) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, nil)
	if res == nil {
		return nil
	}
	out := make([]*graph.Node, 0, len(res.Rows))
	for _, r := range res.Rows {
		if n := rowToNode(r); n != nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Store) AllEdges() []*graph.Edge {
	q := `MATCH (a:Node)-[e:EDGE]->(b:Node) RETURN ` + edgeReturnFields
	res, _ := s.db.Execute(q, nil)
	if res == nil {
		return nil
	}
	out := make([]*graph.Edge, 0, len(res.Rows))
	for _, r := range res.Rows {
		if e := rowToEdge(r); e != nil {
			out = append(out, e)
		}
	}
	return out
}

func (s *Store) EdgesByKind(kind graph.EdgeKind) iter.Seq[*graph.Edge] {
	q := `MATCH (a:Node)-[e:EDGE {e_kind: $k}]->(b:Node) RETURN ` + edgeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"k": string(kind)})
	edges := make([]*graph.Edge, 0, len(res.Rows))
	if res != nil {
		for _, r := range res.Rows {
			if e := rowToEdge(r); e != nil {
				edges = append(edges, e)
			}
		}
	}
	return func(yield func(*graph.Edge) bool) {
		for _, e := range edges {
			if !yield(e) {
				return
			}
		}
	}
}

func (s *Store) NodesByKind(kind graph.NodeKind) iter.Seq[*graph.Node] {
	q := `MATCH (n:Node {kind: $k}) RETURN ` + nodeReturnFields
	res, _ := s.db.Execute(q, lora.Params{"k": string(kind)})
	nodes := make([]*graph.Node, 0, len(res.Rows))
	if res != nil {
		for _, r := range res.Rows {
			if n := rowToNode(r); n != nil {
				nodes = append(nodes, n)
			}
		}
	}
	return func(yield func(*graph.Node) bool) {
		for _, n := range nodes {
			if !yield(n) {
				return
			}
		}
	}
}

func (s *Store) EdgesWithUnresolvedTarget() iter.Seq[*graph.Edge] {
	q := `MATCH (a:Node)-[e:EDGE]->(b:Node)
          WHERE b.id STARTS WITH 'unresolved::'
          RETURN ` + edgeReturnFields
	res, _ := s.db.Execute(q, nil)
	edges := make([]*graph.Edge, 0, len(res.Rows))
	if res != nil {
		for _, r := range res.Rows {
			if e := rowToEdge(r); e != nil {
				edges = append(edges, e)
			}
		}
	}
	return func(yield func(*graph.Edge) bool) {
		for _, e := range edges {
			if !yield(e) {
				return
			}
		}
	}
}

func (s *Store) GetNodesByIDs(ids []string) map[string]*graph.Node {
	if len(ids) == 0 {
		return nil
	}
	uniq := map[string]struct{}{}
	for _, id := range ids {
		if id != "" {
			uniq[id] = struct{}{}
		}
	}
	out := make(map[string]*graph.Node, len(uniq))
	for id := range uniq {
		if n := s.GetNode(id); n != nil {
			out[id] = n
		}
	}
	return out
}

func (s *Store) FindNodesByNames(names []string) map[string][]*graph.Node {
	if len(names) == 0 {
		return nil
	}
	uniq := map[string]struct{}{}
	for _, n := range names {
		if n != "" {
			uniq[n] = struct{}{}
		}
	}
	out := make(map[string][]*graph.Node, len(uniq))
	for name := range uniq {
		if hits := s.FindNodesByName(name); len(hits) > 0 {
			out[name] = hits
		}
	}
	return out
}

func (s *Store) NodeCount() int {
	res, _ := s.db.Execute(`MATCH (n:Node) RETURN count(n) AS n`, nil)
	if res == nil || len(res.Rows) == 0 {
		return 0
	}
	return asInt(res.Rows[0]["n"])
}

func (s *Store) EdgeCount() int {
	res, _ := s.db.Execute(`MATCH ()-[e:EDGE]->() RETURN count(e) AS n`, nil)
	if res == nil || len(res.Rows) == 0 {
		return 0
	}
	return asInt(res.Rows[0]["n"])
}

func (s *Store) Stats() graph.GraphStats {
	st := graph.GraphStats{
		TotalNodes: s.NodeCount(),
		TotalEdges: s.EdgeCount(),
		ByKind:     map[string]int{},
		ByLanguage: map[string]int{},
	}
	if r, err := s.db.Execute(`MATCH (n:Node) RETURN n.kind AS k, count(n) AS c`, nil); err == nil && r != nil {
		for _, row := range r.Rows {
			st.ByKind[asString(row["k"])] = asInt(row["c"])
		}
	}
	if r, err := s.db.Execute(`MATCH (n:Node) WHERE n.language <> '' RETURN n.language AS l, count(n) AS c`, nil); err == nil && r != nil {
		for _, row := range r.Rows {
			st.ByLanguage[asString(row["l"])] = asInt(row["c"])
		}
	}
	return st
}

func (s *Store) RepoStats() map[string]graph.GraphStats {
	out := make(map[string]graph.GraphStats)
	if r, err := s.db.Execute(`MATCH (n:Node) RETURN n.repo_prefix AS r, count(n) AS c`, nil); err == nil && r != nil {
		for _, row := range r.Rows {
			rp := asString(row["r"])
			st := out[rp]
			st.TotalNodes = asInt(row["c"])
			out[rp] = st
		}
	}
	if r, err := s.db.Execute(`MATCH (a:Node)-[e:EDGE]->(b:Node) RETURN a.repo_prefix AS r, count(e) AS c`, nil); err == nil && r != nil {
		for _, row := range r.Rows {
			rp := asString(row["r"])
			st := out[rp]
			st.TotalEdges = asInt(row["c"])
			out[rp] = st
		}
	}
	return out
}

func (s *Store) RepoPrefixes() []string {
	r, err := s.db.Execute(`MATCH (n:Node) RETURN DISTINCT n.repo_prefix AS r`, nil)
	if err != nil || r == nil {
		return nil
	}
	out := make([]string, 0, len(r.Rows))
	for _, row := range r.Rows {
		out = append(out, asString(row["r"]))
	}
	return out
}

func (s *Store) EdgeIdentityRevisions() int { return int(s.edgeIdentityRevs.Load()) }
func (s *Store) VerifyEdgeIdentities() error { return nil }

func (s *Store) RepoMemoryEstimate(repoPrefix string) graph.RepoMemoryEstimate {
	est := graph.RepoMemoryEstimate{}
	if r, err := s.db.Execute(`MATCH (n:Node {repo_prefix: $r}) RETURN count(n) AS c`,
		lora.Params{"r": repoPrefix}); err == nil && r != nil && len(r.Rows) > 0 {
		est.NodeCount = asInt(r.Rows[0]["c"])
	}
	if r, err := s.db.Execute(`MATCH (a:Node {repo_prefix: $r})-[e:EDGE]->(b:Node) RETURN count(e) AS c`,
		lora.Params{"r": repoPrefix}); err == nil && r != nil && len(r.Rows) > 0 {
		est.EdgeCount = asInt(r.Rows[0]["c"])
	}
	return est
}

func (s *Store) AllRepoMemoryEstimates() map[string]graph.RepoMemoryEstimate {
	out := make(map[string]graph.RepoMemoryEstimate)
	for _, rp := range s.RepoPrefixes() {
		out[rp] = s.RepoMemoryEstimate(rp)
	}
	return out
}

var _ = firstLine // quiet unused-fn lint when only some helpers are referenced
