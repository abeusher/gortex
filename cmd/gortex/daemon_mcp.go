package main

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/daemon"
	gortexmcp "github.com/zzet/gortex/internal/mcp"
)

// mcpDispatcher routes MCP JSON-RPC frames from daemon sessions to the
// shared *gortexmcp.Server. Every frame returns through
// MCPServer.HandleMessage, which is the public entry point the
// mark3labs/mcp-go library exposes for non-stdio embeddings.
//
// v1 caveat — one shared *gortexmcp.Server. Per-session state
// (`Server.session`, `Server.tokenStats`, `Server.symHistory`) therefore
// aggregates across all connected proxies, which is tolerable for a
// first release but confuses per-session telemetry. The follow-up
// session-isolation refactor moves that state into a session-keyed
// store; see spec-daemon.md §"Per-session vs shared state."
type mcpDispatcher struct {
	srv    *gortexmcp.Server
	logger *zap.Logger
}

func newMCPDispatcher(srv *gortexmcp.Server, logger *zap.Logger) *mcpDispatcher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &mcpDispatcher{srv: srv, logger: logger}
}

// Dispatch implements daemon.MCPDispatcher. It hands the raw JSON-RPC
// frame to MCPServer.HandleMessage and returns the response bytes.
// Empty return value means the client sent a notification (no response).
func (d *mcpDispatcher) Dispatch(ctx context.Context, sess *daemon.Session, frame []byte) ([]byte, error) {
	if d.srv == nil || d.srv.MCPServer() == nil {
		return nil, fmt.Errorf("mcp dispatcher: no server attached")
	}

	// HandleMessage returns either a JSONRPCResponse, a JSONRPCError, or
	// nil (the message was a notification). It never panics on malformed
	// JSON — it returns a JSON-RPC parse-error frame instead.
	reply := d.srv.MCPServer().HandleMessage(ctx, json.RawMessage(frame))
	if reply == nil {
		return nil, nil
	}

	out, err := json.Marshal(reply)
	if err != nil {
		d.logger.Warn("dispatch: marshal reply failed",
			zap.String("session_id", sess.ID), zap.Error(err))
		return nil, fmt.Errorf("marshal reply: %w", err)
	}
	return out, nil
}
