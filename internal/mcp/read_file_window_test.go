package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReadFile_LineWindow covers the offset/limit line-window added to
// read_file: a bounded read returns exactly the requested lines plus a
// window descriptor, instead of the whole file.
func TestReadFile_LineWindow(t *testing.T) {
	srv, dir := setupTestServer(t)
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lines.txt"), []byte(b.String()), 0o644))

	// offset + limit: lines 3..5 inclusive.
	r := callTool(t, srv, "read_file", map[string]any{
		"path":   "lines.txt",
		"offset": 3,
		"limit":  3,
	})
	m := extractTextResult(t, r)
	assert.Equal(t, "line3\nline4\nline5", m["content"],
		"offset/limit must return exactly the requested line window")
	win, ok := m["window"].(map[string]any)
	require.True(t, ok, "windowed read must carry a window descriptor")
	assert.EqualValues(t, 3, win["start_line"])
	assert.EqualValues(t, 5, win["end_line"])
	assert.EqualValues(t, 10, win["total_lines"])

	// limit-only starts at line 1.
	r = callTool(t, srv, "read_file", map[string]any{"path": "lines.txt", "limit": 2})
	m = extractTextResult(t, r)
	assert.Equal(t, "line1\nline2", m["content"])

	// offset-only reads to EOF.
	r = callTool(t, srv, "read_file", map[string]any{"path": "lines.txt", "offset": 9})
	m = extractTextResult(t, r)
	assert.Equal(t, "line9\nline0", m["content"])
	win, _ = m["window"].(map[string]any)
	assert.EqualValues(t, 10, win["end_line"])

	// offset past EOF yields an empty window, not an error.
	r = callTool(t, srv, "read_file", map[string]any{"path": "lines.txt", "offset": 99})
	m = extractTextResult(t, r)
	assert.Equal(t, "", m["content"])

	// No offset/limit => whole file, no window descriptor.
	r = callTool(t, srv, "read_file", map[string]any{"path": "lines.txt"})
	m = extractTextResult(t, r)
	assert.Contains(t, m["content"], "line1\n")
	assert.Contains(t, m["content"], "line0")
	_, has := m["window"]
	assert.False(t, has, "a full read must not carry a window descriptor")
}

// TestWindowFileLines unit-tests the slicing helper directly, including the
// trailing-newline phantom-line handling.
func TestWindowFileLines(t *testing.T) {
	content := []byte("a\nb\nc\nd\n") // 4 lines + trailing newline

	out, applied, start, end, total := windowFileLines(content, 2, 2)
	assert.True(t, applied)
	assert.Equal(t, "b\nc", string(out))
	assert.Equal(t, 2, start)
	assert.Equal(t, 3, end)
	assert.Equal(t, 4, total, "trailing newline must not inflate the line count")

	// Unset => no window.
	out, applied, _, _, _ = windowFileLines(content, 0, 0)
	assert.False(t, applied)
	assert.Equal(t, content, out)

	// limit past EOF clamps to the last line.
	out, _, start, end, total = windowFileLines(content, 3, 99)
	assert.Equal(t, "c\nd", string(out))
	assert.Equal(t, 3, start)
	assert.Equal(t, 4, end)
	assert.Equal(t, 4, total)
}
