package main

import (
	"fmt"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_ladybug"
)

// openLadybugBackend opens (or creates) the ladybug store at
// path. Returns a cleanup func that closes the underlying handle
// — important because ladybug's writer locks the directory and
// a subsequent reopen on the same path would fail until the
// previous handle is closed.
func openLadybugBackend(path string, bufferPoolMB uint64) (graph.Store, func(), error) {
	s, err := store_ladybug.OpenWithOptions(path, store_ladybug.Options{
		BufferPoolMB: bufferPoolMB,
	})
	if err != nil {
		// liblbug collapses every open failure — including "another
		// process already holds the lock on this store" — into a single
		// generic status with no message (lbug_state is just Success/Error,
		// and lbug_database_init exposes no error string). A second gortex
		// process on the same store is the most common cause, so name it
		// instead of leaving the user the bare, unactionable status code.
		hint := "if another gortex daemon or server is using this store, stop it first (`gortex daemon status` / `gortex daemon stop`)"
		if pid, ok := daemon.RunningPID(); ok {
			hint = fmt.Sprintf("a gortex daemon is already running (pid %d) — stop it with `gortex daemon stop`, or use `gortex daemon restart`", pid)
		}
		return nil, nil, fmt.Errorf("open ladybug store at %q: %w (%s)", path, err, hint)
	}
	return s, func() { _ = s.Close() }, nil
}

// The daemon warm-restart path consults this optional capability
// (cmd/gortex/daemon_state.go: storeNeedsRebuild) to force a full re-index
// when a schema migration crossed a rebuild rung. This assertion keeps the
// concrete store and the daemon's optional-interface check from drifting.
var _ interface{ NeedsRebuild() bool } = (*store_ladybug.Store)(nil)
