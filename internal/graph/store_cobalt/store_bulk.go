package store_cobalt

import (
	"context"

	cobalt "github.com/cobaltdb/cobaltdb/pkg/engine"

	"github.com/zzet/gortex/internal/graph"
)

// Compile-time assertion: *Store offers the cold-load fast path.
var _ graph.BulkLoader = (*Store)(nil)

const (
	// rowsPerStmt is the FIXED number of rows per multi-row INSERT. A
	// constant tuple count is the crucial perf lever: it keeps the SQL text
	// identical across statements so CobaltDB's prepared-statement cache
	// reuses the parse. Variable-sized statements re-parse on every call and
	// make the bulk load ~50× slower. WAL is disabled (see OpenWithOptions),
	// so there is no per-record size cap to respect — a chunk that happens
	// to include a large-meta `doc` row is fine.
	rowsPerStmt = 100
	// txRowBudget bounds rows per transaction during a bulk load so a single
	// commit does not have to buffer the entire cold-load.
	txRowBudget = 5000
)

// BeginBulkLoad switches the store into buffering mode. Subsequent
// AddNode/AddEdge/AddBatch calls accumulate in memory instead of issuing
// per-call writes; FlushBulk commits them. The indexer probes for this via a
// graph.BulkLoader type assertion and uses it for cold indexing.
func (s *Store) BeginBulkLoad() {
	s.bulkMu.Lock()
	s.bulkActive = true
	s.bulkMu.Unlock()
}

// stageIfBulk buffers nodes/edges when bulk-load mode is active. It returns
// true when the items were buffered, signalling the calling mutator to perform
// no direct write. Returns false in normal mode.
func (s *Store) stageIfBulk(nodes []*graph.Node, edges []*graph.Edge) bool {
	s.bulkMu.Lock()
	defer s.bulkMu.Unlock()
	if !s.bulkActive {
		return false
	}
	if len(nodes) > 0 {
		s.bulkNodes = append(s.bulkNodes, nodes...)
	}
	if len(edges) > 0 {
		s.bulkEdges = append(s.bulkEdges, edges...)
	}
	return true
}

// FlushBulk commits everything staged since BeginBulkLoad and leaves bulk-load
// mode. Nodes and edges are deduplicated (last write wins, by id / edge_key)
// before loading, matching the idempotent semantics of the per-call path.
func (s *Store) FlushBulk() error {
	s.bulkMu.Lock()
	nodes := s.bulkNodes
	edges := s.bulkEdges
	s.bulkNodes = nil
	s.bulkEdges = nil
	s.bulkActive = false
	s.bulkMu.Unlock()

	if len(nodes) == 0 && len(edges) == 0 {
		return nil
	}
	return s.bulkCommit(nodes, edges)
}

// bulkCommit dedups staged rows then bulk-loads them.
func (s *Store) bulkCommit(nodes []*graph.Node, edges []*graph.Edge) error {
	nodeByID := make(map[string]*graph.Node, len(nodes))
	nodeOrder := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if n == nil || n.ID == "" {
			continue
		}
		if _, ok := nodeByID[n.ID]; !ok {
			nodeOrder = append(nodeOrder, n.ID)
		}
		nodeByID[n.ID] = n
	}
	edgeByKey := make(map[string]*graph.Edge, len(edges))
	edgeOrder := make([]string, 0, len(edges))
	for _, e := range edges {
		if e == nil {
			continue
		}
		k := edgeKeyOf(e)
		if _, ok := edgeByKey[k]; !ok {
			edgeOrder = append(edgeOrder, k)
		}
		edgeByKey[k] = e
	}

	nodeRows := make([][]any, 0, len(nodeOrder))
	for _, id := range nodeOrder {
		nodeRows = append(nodeRows, nodeValues(nodeByID[id]))
	}
	edgeRows := make([][]any, 0, len(edgeOrder))
	for _, k := range edgeOrder {
		edgeRows = append(edgeRows, edgeValues(edgeByKey[k]))
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.bulkInsert("nodes", nodeInsertCols, nodeInsertCount, nodeRows); err != nil {
		return err
	}
	return s.bulkInsert("edges", edgeInsertCols, edgeInsertCount, edgeRows)
}

// bulkInsert loads pre-built value rows in transactions of at most txRowBudget
// rows, each emitting byte-budgeted multi-row INSERT OR REPLACE statements.
// The caller holds writeMu.
func (s *Store) bulkInsert(table, cols string, perRow int, rows [][]any) error {
	for start := 0; start < len(rows); {
		end := min(start+txRowBudget, len(rows))
		tx, err := s.db.Begin(s.ctx)
		if err != nil {
			return err
		}
		if err := insertRowsTx(s.ctx, tx, table, cols, perRow, rows[start:end]); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		start = end
	}
	return nil
}

// insertRowsTx emits fixed-size multi-row INSERT OR REPLACE statements within
// tx. Holding the tuple count constant (rowsPerStmt) keeps the SQL text stable
// so the prepared-statement cache hits; only the final short chunk differs.
func insertRowsTx(ctx context.Context, tx *cobalt.Tx, table, cols string, perRow int, rows [][]any) error {
	for i := 0; i < len(rows); i += rowsPerStmt {
		end := min(i+rowsPerStmt, len(rows))
		chunk := rows[i:end]
		args := make([]any, 0, len(chunk)*perRow)
		for _, r := range chunk {
			args = append(args, r...)
		}
		if _, err := tx.Exec(ctx, buildInsert(table, cols, perRow, len(chunk)), args...); err != nil {
			return err
		}
	}
	return nil
}
