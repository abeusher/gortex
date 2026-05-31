package store_ladybug

import (
	"fmt"
	"os"
	"strings"

	lbug "github.com/LadybugDB/go-ladybug"
)

// runWriteLocked executes a write-shaped Cypher statement under the
// caller-held writeMu. Panics on a genuine engine error (closed
// connection / schema mismatch / disk-full) — graph.Store has no
// error channel and the in-memory store can't fail either, so a
// fatal storage failure cannot be ignored.
func (s *Store) runWriteLocked(query string, args map[string]any) {
	res, release, err := s.executeOrQuery(query, args)
	if err != nil {
		panicOnFatal(err)
		return
	}
	res.Close()
	release()
}

// querySelect runs a read-shaped Cypher statement and materialises
// every row before returning. The connection pool gives each
// caller its own private connection so concurrent reads no longer
// need a serialisation mutex — every per-repo Indexer's
// NodeCount / shadow-swap probe runs in parallel.
//
// We still consume the iterator before releasing the connection
// to the pool — open iterators hold the kuzu_query handle and
// the connection isn't safe to reuse until the result is closed.
func (s *Store) querySelect(query string, args map[string]any) [][]any {
	// RLock excludes the read from the window any writer (COPY / MERGE /
	// DELETE) holds the exclusive Lock — a read on a sibling pooled
	// connection while a COPY extends the .lbug file is the source of
	// both the "Cannot read N bytes" IO exceptions and the harder
	// lbug_connection_query SIGSEGV. Concurrent reads still run in
	// parallel; only a write blocks them. Callers that already hold the
	// write Lock must route through querySelectLocked, which skips this
	// acquisition (an RWMutex is not reentrant).
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return s.querySelectInner(query, args)
}

// querySelectInner is the unlocked body shared between querySelect
// (locks) and querySelectLocked (caller already holds writeMu).
//
// Engine errors on the read path are logged + the partial-or-empty
// row buffer is returned instead of panicking. A read failure here
// is almost always a transient LadybugdbIO exception (e.g. a buffer-pool
// read landing in the middle of a concurrent COPY's file extension —
// "Cannot read N bytes at position M") and used to kill the daemon
// via panicOnFatal. The graph.Store interface still has no error
// channel so we can't bubble it up; degrading to an empty result on
// reads gives the caller a recoverable "looks like the symbol has
// no edges right now" path while the daemon stays up. Write paths
// (runWriteLocked) keep panic semantics because a write failure
// means the graph is now inconsistent and continuing would corrupt
// subsequent state.
func (s *Store) querySelectInner(query string, args map[string]any) [][]any {
	res, release, err := s.executeOrQuery(query, args)
	if err != nil {
		readPathLogf("executeOrQuery: %v (query=%q)", err, firstLine(query))
		return nil
	}
	defer release()
	defer res.Close()
	var rows [][]any
	for res.HasNext() {
		tup, err := res.Next()
		if err != nil {
			readPathLogf("Next: %v (query=%q rows=%d)", err, firstLine(query), len(rows))
			return rows
		}
		vals, err := tup.GetAsSlice()
		if err != nil {
			tup.Close()
			readPathLogf("GetAsSlice: %v (query=%q rows=%d)", err, firstLine(query), len(rows))
			return rows
		}
		rows = append(rows, vals)
		tup.Close()
	}
	return rows
}

// readPathLogf emits a degraded-read warning to stderr (which the
// daemon redirects to its log file). Format: a single line prefixed
// with `store_ladybug: read degraded:` so log scrapers can find these
// without parsing JSON. We deliberately avoid the structured zap
// logger here — the Store has no logger reference and threading one
// through every callsite would be a much larger change than this
// hot-path fix is meant to be.
func readPathLogf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(os.Stderr, "store_ladybug: read degraded: %s\n", msg)
}

// querySelectLocked is querySelect for callers that already hold
// writeMu. Routes to the same unlocked body querySelect uses
// (re-acquiring writeMu would deadlock).
func (s *Store) querySelectLocked(query string, args map[string]any) [][]any {
	return s.querySelectInner(query, args)
}

// executeOrQuery hides the prepared-vs-direct distinction. KuzuDB
// requires the Prepare → Execute path for parameterised statements;
// a bare Query with `$arg` placeholders is rejected. Statements
// without parameters fall through to a direct Query for clarity.
//
// Borrows a connection from s.pool so concurrent calls don't race
// in cgo. Returns a release function the caller MUST defer — the
// connection cannot return to the pool until the QueryResult has
// been fully consumed (open iterators hold the kuzu_query handle
// on the borrowed connection). Falls back to the setup s.conn if
// the pool isn't ready (test fixtures that construct Store{}
// directly); release() is a no-op in that case.
func (s *Store) executeOrQuery(query string, args map[string]any) (*lbug.QueryResult, func(), error) {
	conn := s.conn
	release := func() {}
	// discard pulls a connection OUT of circulation on error instead of
	// recycling it — a connection that errored mid-statement (a failed
	// COPY in particular) can be left poisoned, and reusing it makes a
	// later Prepare on an unrelated goroutine panic with "mutex lock
	// failed: Invalid argument". Falls back to a no-op for the
	// non-pooled setup connection (test fixtures) where there's nothing
	// to replace.
	discard := func() {}
	if s.pool != nil {
		conn = s.pool.get()
		release = func() { s.pool.put(conn) }
		discard = func() { s.pool.discard(conn) }
	}
	if len(args) == 0 {
		res, err := conn.Query(query)
		if err != nil {
			discard()
			return nil, func() {}, err
		}
		return res, release, nil
	}
	stmt, err := conn.Prepare(query)
	if err != nil {
		discard()
		return nil, func() {}, fmt.Errorf("prepare: %w", err)
	}
	defer stmt.Close()
	res, err := conn.Execute(stmt, args)
	if err != nil {
		discard()
		return nil, func() {}, err
	}
	return res, release, nil
}

// panicOnFatal turns a non-nil engine error into a panic so callers
// see catastrophic failures. The graph.Store interface deliberately
// does not surface errors — it mirrors the in-memory store's
// "everything succeeds" contract — so a fatal storage failure
// cannot be silently dropped.
func panicOnFatal(err error) {
	if err == nil {
		return
	}
	panic(fmt.Errorf("store_ladybug: %w", err))
}

// firstLine is a small helper for trimming a multi-line Cypher
// statement to its first non-empty line for use in error messages.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
