package main

import (
	"fmt"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
)

// openSqliteBackend opens (or creates) the SQLite store at path. It uses
// the pure-Go modernc.org/sqlite driver, so this backend keeps the binary
// CGo-free while still getting a real query planner that drives the graph's
// secondary indexes. Returns a cleanup func that closes the handle.
//
// bufferPoolMB is accepted for signature parity with the other on-disk
// backends but is unused — SQLite sizes its page cache via the cache_size
// pragma set in store_sqlite.Open, not a single fixed pool.
func openSqliteBackend(path string, bufferPoolMB uint64) (graph.Store, func(), error) {
	_ = bufferPoolMB
	s, err := store_sqlite.Open(path)
	if err != nil {
		hint := "if another gortex daemon or server is using this store, stop it first (`gortex daemon status` / `gortex daemon stop`)"
		if pid, ok := daemon.RunningPID(); ok {
			hint = fmt.Sprintf("a gortex daemon is already running (pid %d) — stop it with `gortex daemon stop`, or use `gortex daemon restart`", pid)
		}
		return nil, nil, fmt.Errorf("open sqlite store at %q: %w (%s)", path, err, hint)
	}
	return s, func() { _ = s.Close() }, nil
}
