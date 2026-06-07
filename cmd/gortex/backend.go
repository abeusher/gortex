package main

import (
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/serverstack"
)

// openBackend delegates to the shared constructor's backend dispatch.
// Kept as a thin wrapper so existing call sites compile unchanged while
// the single construction path lives in internal/serverstack.
func openBackend(name, path string, bufferPoolMB uint64, logger *zap.Logger) (graph.Store, func(), error) {
	return serverstack.OpenBackend(name, path, bufferPoolMB, logger)
}
