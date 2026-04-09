package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/zzet/gortex/internal/graph"
	"go.uber.org/zap"
)

// Handler wraps the MCP server's tool dispatch as an HTTP handler.
// It exposes /health, /tool/{tool_name}, /augment, and /stats endpoints.
type Handler struct {
	mcpServer *mcpserver.MCPServer
	graph     *graph.Graph
	version   string
	logger    *zap.Logger
	mux       *http.ServeMux
	startTime time.Time
}

// NewHandler creates an HTTP handler that dispatches to MCP tools.
func NewHandler(mcpServer *mcpserver.MCPServer, g *graph.Graph, version string, logger *zap.Logger) *Handler {
	h := &Handler{
		mcpServer: mcpServer,
		graph:     g,
		version:   version,
		logger:    logger,
		mux:       http.NewServeMux(),
		startTime: time.Now(),
	}
	h.registerRoutes()
	return h
}

// ServeHTTP implements http.Handler with panic recovery middleware.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			stack := debug.Stack()
			h.logger.Error("panic recovered in HTTP handler",
				zap.Any("panic", rec),
				zap.String("stack", string(stack)),
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
			)
			writeJSONError(w, http.StatusInternalServerError, "internal server error")
		}
	}()
	h.mux.ServeHTTP(w, r)
}

// registerRoutes sets up the HTTP routes.
func (h *Handler) registerRoutes() {
	h.mux.HandleFunc("GET /health", h.handleHealth)
	h.mux.HandleFunc("POST /tool/", h.handleToolCall)
	h.mux.HandleFunc("POST /augment", h.handleAugment)
	h.mux.HandleFunc("GET /stats", h.handleStats)
}

// healthResponse is the JSON structure for the /health endpoint.
type healthResponse struct {
	Status        string  `json:"status"`
	Indexed       bool    `json:"indexed"`
	Nodes         int     `json:"nodes"`
	Edges         int     `json:"edges"`
	Version       string  `json:"version"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	stats := h.graph.Stats()
	resp := healthResponse{
		Status:        "ok",
		Indexed:       stats.TotalNodes > 0,
		Nodes:         stats.TotalNodes,
		Edges:         stats.TotalEdges,
		Version:       h.version,
		UptimeSeconds: time.Since(h.startTime).Seconds(),
	}
	writeJSON(w, http.StatusOK, resp)
}

// toolRequest is the expected JSON body for POST /tool/{tool_name}.
type toolRequest struct {
	Arguments map[string]any `json:"arguments"`
}

// toolResponse wraps the MCP tool call result for JSON serialization.
type toolResponse struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// toolContent is a simplified content item from the MCP tool result.
type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func (h *Handler) handleToolCall(w http.ResponseWriter, r *http.Request) {
	// Extract tool name from path: /tool/{tool_name}
	toolName := strings.TrimPrefix(r.URL.Path, "/tool/")
	if toolName == "" {
		writeJSONError(w, http.StatusBadRequest, "missing tool name in path")
		return
	}

	// Look up the tool in the MCP server.
	tool := h.mcpServer.GetTool(toolName)
	if tool == nil {
		// Collect available tool names for the error response.
		available := h.availableToolNames()
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":           "tool_not_found",
			"message":         fmt.Sprintf("tool '%s' not found", toolName),
			"available_tools": available,
		})
		return
	}

	// Parse the request body.
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var args map[string]any
	if len(body) > 0 {
		var req toolRequest
		if err := json.Unmarshal(body, &req); err != nil {
			// Try parsing body directly as arguments (convenience).
			if err2 := json.Unmarshal(body, &args); err2 != nil {
				writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("malformed JSON: %s", err.Error()))
				return
			}
		} else {
			args = req.Arguments
			// If arguments field was empty/null, try the body as direct args.
			if args == nil {
				_ = json.Unmarshal(body, &args)
			}
		}
	}

	// Build the MCP CallToolRequest.
	mcpReq := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	// Invoke the tool handler.
	result, err := tool.Handler(r.Context(), mcpReq)
	if err != nil {
		h.logger.Error("tool call failed",
			zap.String("tool", toolName),
			zap.Error(err),
		)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "tool_error",
			"message": err.Error(),
		})
		return
	}

	// Convert MCP result to our response format.
	resp := toolResponse{
		IsError: result.IsError,
	}
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			resp.Content = append(resp.Content, toolContent{
				Type: "text",
				Text: tc.Text,
			})
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// augmentRequest is the expected JSON body for POST /augment.
type augmentRequest struct {
	Pattern string `json:"pattern"`
}

// augmentResponse is the JSON response for POST /augment.
type augmentResponse struct {
	Pattern     string           `json:"pattern"`
	Symbols     []augmentSymbol  `json:"symbols"`
}

// augmentSymbol holds graph annotations for a matched symbol.
type augmentSymbol struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	File     string   `json:"file"`
	Kind     string   `json:"kind"`
	Callers  []string `json:"callers,omitempty"`
	Callees  []string `json:"callees,omitempty"`
	CallChain []string `json:"call_chain,omitempty"`
}

func (h *Handler) handleAugment(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req augmentRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("malformed JSON: %s", err.Error()))
		return
	}

	if req.Pattern == "" {
		writeJSONError(w, http.StatusBadRequest, "missing 'pattern' field")
		return
	}

	ctx := r.Context()

	// Step 1: search_symbols for the pattern.
	searchResults := h.callTool(ctx, "search_symbols", map[string]any{
		"query":   req.Pattern,
		"compact": true,
	})

	// Parse symbol IDs from search results.
	symbolIDs := extractSymbolIDs(searchResults)

	var symbols []augmentSymbol
	for _, id := range symbolIDs {
		sym := augmentSymbol{ID: id}

		// Extract name and file from the ID (format: file::Name).
		if parts := strings.SplitN(id, "::", 2); len(parts) == 2 {
			sym.File = parts[0]
			sym.Name = parts[1]
		} else {
			sym.Name = id
		}

		// Look up the node kind from the graph.
		if node := h.graph.GetNode(id); node != nil {
			sym.Kind = string(node.Kind)
		}

		// Step 2: find_usages for each symbol.
		usageResults := h.callTool(ctx, "find_usages", map[string]any{
			"id":      id,
			"compact": true,
		})
		sym.Callers = extractLines(usageResults)

		// Step 3: get_call_chain for functions/methods.
		chainResults := h.callTool(ctx, "get_call_chain", map[string]any{
			"function_id": id,
			"compact":     true,
			"depth":       float64(2),
		})
		sym.CallChain = extractLines(chainResults)

		symbols = append(symbols, sym)
	}

	resp := augmentResponse{
		Pattern: req.Pattern,
		Symbols: symbols,
	}
	writeJSON(w, http.StatusOK, resp)
}

// statsResponse is the JSON structure for the /stats endpoint.
type statsResponse struct {
	TotalNodes int            `json:"total_nodes"`
	TotalEdges int            `json:"total_edges"`
	ByKind     map[string]int `json:"by_kind"`
	ByLanguage map[string]int `json:"by_language"`
}

func (h *Handler) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := h.graph.Stats()
	resp := statsResponse{
		TotalNodes: stats.TotalNodes,
		TotalEdges: stats.TotalEdges,
		ByKind:     stats.ByKind,
		ByLanguage: stats.ByLanguage,
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- Helper functions ---

// callTool invokes an MCP tool by name and returns the text content of the result.
// Returns empty string on error or if the tool is not found.
func (h *Handler) callTool(ctx context.Context, toolName string, args map[string]any) string {
	tool := h.mcpServer.GetTool(toolName)
	if tool == nil {
		return ""
	}

	req := mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      toolName,
			Arguments: args,
		},
	}

	result, err := tool.Handler(ctx, req)
	if err != nil {
		h.logger.Debug("internal tool call failed",
			zap.String("tool", toolName),
			zap.Error(err),
		)
		return ""
	}

	// Concatenate all text content.
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// extractSymbolIDs parses symbol IDs from compact search_symbols output.
// Each line is expected to be: "kind name file:line" — we extract "file::name".
func extractSymbolIDs(text string) []string {
	if text == "" {
		return nil
	}
	var ids []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Compact format: "kind name file:line"
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			name := parts[1]
			fileLine := parts[2]
			file := fileLine
			if idx := strings.LastIndex(fileLine, ":"); idx > 0 {
				file = fileLine[:idx]
			}
			id := file + "::" + name
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// extractLines splits text into non-empty trimmed lines.
func extractLines(text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// availableToolNames returns a sorted list of registered tool names.
func (h *Handler) availableToolNames() []string {
	tools := h.mcpServer.ListTools()
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	return names
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeJSONError writes a JSON error response.
func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{
		"error":   http.StatusText(status),
		"message": message,
	})
}
