package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// computeETag produces a short content hash suitable for conditional fetch.
// The hash is computed from the JSON serialization of the data.
func computeETag(data any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8]) // 16 hex chars — collision-safe for session use
}

// notModifiedResult returns a minimal "not modified" response with the matching etag.
func notModifiedResult(etag string) *mcp.CallToolResult {
	result, _ := mcp.NewToolResultJSON(map[string]any{
		"not_modified": true,
		"etag":         etag,
	})
	return result
}

// withETag adds an etag field to a map result and returns the JSON tool result.
func withETag(data map[string]any) (*mcp.CallToolResult, error) {
	etag := computeETag(data)
	data["etag"] = etag
	return mcp.NewToolResultJSON(data)
}
