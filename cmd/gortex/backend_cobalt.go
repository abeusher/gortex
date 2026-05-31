package main

import (
	"fmt"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_cobalt"
)

// Capability assertion: the cobalt store answers the daemon's optional
// rebuild probe (it never needs a from-scratch rebuild — its schema is
// applied idempotently).
var _ interface{ NeedsRebuild() bool } = (*store_cobalt.Store)(nil)

// openCobaltBackend opens (or creates) the CobaltDB store at path.
// CobaltDB is a pure-Go embedded SQL engine — zero CGo — so this backend
// cross-compiles anywhere and persists to a single file (plus a sibling
// WAL). Returns a cleanup func that closes the handle.
func openCobaltBackend(path string, bufferPoolMB uint64) (graph.Store, func(), error) {
	opts := store_cobalt.Options{}
	if bufferPoolMB > 0 {
		// CobaltDB sizes its page cache in 4 KiB pages.
		opts.CachePages = int(bufferPoolMB * 1024 * 1024 / 4096)
	}
	s, err := store_cobalt.OpenWithOptions(path, opts)
	if err != nil {
		hint := "if another gortex daemon or server is using this store, stop it first (`gortex daemon status` / `gortex daemon stop`)"
		if pid, ok := daemon.RunningPID(); ok {
			hint = fmt.Sprintf("a gortex daemon is already running (pid %d) — stop it with `gortex daemon stop`, or use `gortex daemon restart`", pid)
		}
		return nil, nil, fmt.Errorf("open cobalt store at %q: %w (%s)", path, err, hint)
	}
	return s, func() { _ = s.Close() }, nil
}
