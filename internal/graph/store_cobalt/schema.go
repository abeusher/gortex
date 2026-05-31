package store_cobalt

import (
	"fmt"
	"strings"
)

// The graph is two relational tables. `nodes.id` and `edges.edge_key`
// are the primary keys that make `INSERT OR REPLACE` an idempotent
// upsert. Every column is non-nullable in practice — writes always
// supply a concrete value — so reads never hit CobaltDB's NULL-into-
// *string sentinel.
const (
	createNodesTable = `CREATE TABLE nodes (
	id           TEXT PRIMARY KEY,
	kind         TEXT,
	name         TEXT,
	name_lower   TEXT,
	qual_name    TEXT,
	file_path    TEXT,
	start_line   INTEGER,
	end_line     INTEGER,
	language     TEXT,
	repo_prefix  TEXT,
	workspace_id TEXT,
	project_id   TEXT,
	meta         TEXT
)`

	// edge_key is the delimiter-joined identity tuple
	// (from|to|kind|file_path|line) — Line is part of edge identity, so
	// two calls to the same target from different lines are distinct
	// rows, while a re-add of the same call overwrites in place.
	createEdgesTable = `CREATE TABLE edges (
	edge_key         TEXT PRIMARY KEY,
	from_id          TEXT,
	to_id            TEXT,
	kind             TEXT,
	file_path        TEXT,
	line             INTEGER,
	confidence       REAL,
	confidence_label TEXT,
	origin           TEXT,
	tier             TEXT,
	cross_repo       INTEGER,
	meta             TEXT
)`
)

// schemaIndexes are the secondary B+Tree indexes that back the
// predicate-shaped reads (by name / kind / qual_name / repo / file and
// edge adjacency by from/to/kind). CobaltDB indexes these directly, so
// the backend needs no Go-side accelerator maps.
var schemaIndexes = []string{
	`CREATE INDEX idx_nodes_name ON nodes(name)`,
	`CREATE INDEX idx_nodes_name_lower ON nodes(name_lower)`,
	`CREATE INDEX idx_nodes_kind ON nodes(kind)`,
	`CREATE INDEX idx_nodes_qual ON nodes(qual_name)`,
	`CREATE INDEX idx_nodes_repo ON nodes(repo_prefix)`,
	`CREATE INDEX idx_nodes_file ON nodes(file_path)`,
	`CREATE INDEX idx_edges_from ON edges(from_id)`,
	`CREATE INDEX idx_edges_to ON edges(to_id)`,
	`CREATE INDEX idx_edges_kind ON edges(kind)`,
}

// applySchema installs the tables and indexes. It is idempotent: a
// reopened on-disk store whose `nodes` table already exists short-
// circuits, so CREATE never collides with an existing object.
func (s *Store) applySchema() error {
	for _, t := range s.db.Tables() {
		if strings.EqualFold(t, "nodes") {
			return nil
		}
	}
	if _, err := s.exec(createNodesTable); err != nil {
		return fmt.Errorf("create nodes table: %w", err)
	}
	if _, err := s.exec(createEdgesTable); err != nil {
		return fmt.Errorf("create edges table: %w", err)
	}
	for _, idx := range schemaIndexes {
		if _, err := s.exec(idx); err != nil {
			return fmt.Errorf("create index %q: %w", idx, err)
		}
	}
	return nil
}
