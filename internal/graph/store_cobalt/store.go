// Package store_cobalt implements graph.Store on top of CobaltDB, a
// pure-Go embedded SQL engine (github.com/cobaltdb/cobaltdb). It is an
// alternative to the Kuzu-backed store_ladybug backend with zero CGo:
// the whole engine is Go, so the daemon cross-compiles to any
// OS/arch and ships as a single static binary.
//
// Model. The knowledge graph is two relational tables — `nodes`
// (primary key `id`) and `edges` (primary key `edge_key`, the
// delimiter-joined identity tuple from|to|kind|file|line). Every
// graph query is a SQL statement over secondary B+Tree indexes;
// because CobaltDB indexes name/kind/qual_name/file_path/repo_prefix
// directly, this backend keeps NO Go-side accelerator maps (unlike
// store_ladybug, whose Kuzu layer needed them).
//
// Two design rules avoid the engine's only sharp edges:
//   - Never store SQL NULL. Every column is written with a concrete
//     "" / 0 value, so scanning into *string never yields the engine's
//     NULL sentinel ("<nil>"). Empty meta is the empty string.
//   - Idempotent upserts use `INSERT OR REPLACE` (CobaltDB's only
//     overwrite-by-PK form; ON CONFLICT / REPLACE INTO are not honoured).
//
// Capabilities. The store implements the core graph.Store contract plus
// graph.BulkLoader (a chunked cold-load fast path). It deliberately does
// NOT implement graph.BackendResolver: edge resolution is driven by the
// in-process Go resolver (internal/resolver) through the core Store
// methods. Unlike the cgo-bound Kuzu backend — where per-edge queries
// cross the cgo boundary and a native bulk-SQL resolver is essential —
// CobaltDB runs in-process with batched IN-list lookups, so the Go
// resolver path is already efficient and a SQL BackendResolver buys
// little. The higher-level capability interfaces (PageRanker,
// CommunityDetector, KCorer, …) are similarly left to the engine's
// in-memory fallbacks; the conformance suite skips every interface a
// backend does not implement.
package store_cobalt

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	cobalt "github.com/cobaltdb/cobaltdb/pkg/engine"
	cobaltlog "github.com/cobaltdb/cobaltdb/pkg/logger"

	"github.com/zzet/gortex/internal/graph"
)

// Compile-time assertion: *Store satisfies the core Store contract.
var _ graph.Store = (*Store)(nil)

// Options configures the embedded CobaltDB instance. The zero value is
// valid and applies engine defaults.
type Options struct {
	// InMemory opens a non-persistent database (path is ignored). Used
	// by the conformance suite and ephemeral callers.
	InMemory bool

	// CachePages caps the engine's page cache in pages (one page is a
	// few KiB). Zero leaves the engine default. openCobaltBackend
	// derives this from the daemon's --backend-buffer-pool-mb.
	CachePages int
}

// Store is a graph.Store backed by a single CobaltDB handle. CobaltDB
// is safe for concurrent reads and writes on one *DB, but to keep the
// write path deterministic under the resolver's fan-out we serialise
// all mutations through writeMu; reads run lock-free (the engine's MVCC
// gives them a consistent snapshot).
type Store struct {
	db  *cobalt.DB
	ctx context.Context

	// writeMu serialises every mutation (AddNode/AddEdge/AddBatch/
	// Evict*/Reindex*/SetEdgeProvenance*/RemoveEdge and bulk flush).
	writeMu sync.Mutex

	// resolveMu is handed to resolver instances via ResolveMutex so
	// they serialise their edge-mutation passes. Distinct from writeMu.
	resolveMu sync.Mutex

	// edgeRevs counts provenance-bearing identity changes (bumped by
	// SetEdgeProvenance[Batch]); surfaced via EdgeIdentityRevisions.
	edgeRevs atomic.Int64

	// Bulk-load staging (graph.BulkLoader). When bulkActive, writes are
	// buffered here and committed in one chunked transaction on FlushBulk.
	bulkMu     sync.Mutex
	bulkActive bool
	bulkNodes  []*graph.Node
	bulkEdges  []*graph.Edge
}

// Open is the zero-config entry point: opens (or creates) a CobaltDB
// database file at path and applies the schema. Pass ":memory:" (or an
// empty path) for a non-persistent store.
func Open(path string) (*Store, error) {
	return OpenWithOptions(path, Options{InMemory: path == "" || path == ":memory:"})
}

// OpenWithOptions opens (or creates) the database and installs the
// schema. On disk, CobaltDB owns the file at path plus a sibling WAL.
func OpenWithOptions(path string, opts Options) (*Store, error) {
	eopts := &cobalt.Options{
		InMemory: opts.InMemory,
		// WAL OFF. CobaltDB caps a single WAL record at 65535 bytes (the
		// length field is a uint16), and one row becomes one record — a
		// single node with a large meta/doc/string payload (common in real
		// repos) exceeds that and cannot be split, which makes a WAL-backed
		// store unusable here. With WAL off, writes flush straight to the
		// buffer pool and a clean Close persists the catalog + dirty pages,
		// so warm restarts still skip re-indexing; only an unclean crash
		// loses the tail, and the daemon simply re-indexes that repo. Bulk
		// load is also faster without per-row WAL framing.
		WALEnabled: cobalt.BoolPtr(false),
		// Silence the engine's default stdout INFO logger — the daemon
		// owns process output. A discard writer drops every level.
		Logger: cobaltlog.New(cobaltlog.WarnLevel, io.Discard),
		// No per-call timeout: a cold AllNodes/AllEdges scan on a large
		// graph legitimately runs longer than the 60s engine default.
		QueryTimeout: 0,
		// Unlimited connections: the indexer and resolver fan out across
		// many goroutines and must not block on a connection semaphore.
		MaxConnections: 0,
		CacheSize:      opts.CachePages,
	}
	if opts.InMemory {
		path = ":memory:"
	}
	db, err := cobalt.Open(path, eopts)
	if err != nil {
		return nil, fmt.Errorf("open cobalt store at %q: %w", path, err)
	}
	s := &Store{db: db, ctx: context.Background()}
	if err := s.applySchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply cobalt schema: %w", err)
	}
	return s, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ResolveMutex returns the backend-owned mutex resolver instances share
// to serialise edge-mutation passes. The returned pointer is owned by
// the store; callers must not Unlock it when they do not hold it.
func (s *Store) ResolveMutex() *sync.Mutex { return &s.resolveMu }

// NeedsRebuild reports whether the daemon should re-index from scratch
// after open. CobaltDB applies its schema idempotently with no
// version-ladder rebuild, so it never asks for one.
func (s *Store) NeedsRebuild() bool { return false }

// --- low-level helpers -------------------------------------------------

// exec runs a write/DDL statement.
func (s *Store) exec(query string, args ...any) (cobalt.Result, error) {
	return s.db.Exec(s.ctx, query, args...)
}

// mustExec runs a write statement and panics on error. The graph is
// inconsistent if a sanctioned write fails, so — like store_ladybug —
// the write path treats engine errors as fatal rather than silently
// dropping a mutation.
func (s *Store) mustExec(query string, args ...any) cobalt.Result {
	res, err := s.exec(query, args...)
	if err != nil {
		panic(fmt.Sprintf("store_cobalt write failed: %v\nquery: %s", err, query))
	}
	return res
}

// queryNodes runs a SELECT projecting nodeSelectCols and scans the rows
// into *graph.Node. Read errors degrade to an empty slice (a transient
// engine error during an oversized pass must not crash the daemon).
func (s *Store) queryNodes(query string, args ...any) []*graph.Node {
	rows, err := s.db.Query(s.ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*graph.Node
	for rows.Next() {
		if n := scanNode(rows); n != nil {
			out = append(out, n)
		}
	}
	return out
}

// queryEdges runs a SELECT projecting edgeSelectCols and scans the rows
// into *graph.Edge.
func (s *Store) queryEdges(query string, args ...any) []*graph.Edge {
	rows, err := s.db.Query(s.ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*graph.Edge
	for rows.Next() {
		if e := scanEdge(rows); e != nil {
			out = append(out, e)
		}
	}
	return out
}

// queryStrings runs a single-column string SELECT and returns that
// column for every row (used for id-list fetches and DISTINCT scans).
func (s *Store) queryStrings(query string, args ...any) []string {
	rows, err := s.db.Query(s.ctx, query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return out
		}
		out = append(out, v)
	}
	return out
}

// queryCount runs a `SELECT count(*) ...` style query and returns the
// single integer it yields (0 on error or empty result).
func (s *Store) queryCount(query string, args ...any) int {
	rows, err := s.db.Query(s.ctx, query, args...)
	if err != nil {
		return 0
	}
	defer rows.Close()
	if !rows.Next() {
		return 0
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		return 0
	}
	return int(n)
}
