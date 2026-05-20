package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// handleSearchText runs a trigram-accelerated literal code search
// across the indexed repository — the alt grep backbone. A trigram
// index narrows the file set, then each candidate is scanned to
// confirm the match, so a repo-wide substring search costs roughly
// the size of the matching files rather than the whole tree.
func (s *Server) handleSearchText(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := req.GetString("query", "")
	if query == "" {
		return mcp.NewToolResultError("search_text: query is required"), nil
	}
	if s.indexer == nil {
		return mcp.NewToolResultError("search_text: no indexer available"), nil
	}

	limit := req.GetInt("limit", 100)
	if limit < 1 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	matches := s.indexer.GrepText(query, limit)
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"query":   query,
		"matches": matches,
		"count":   len(matches),
	})
}
