package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadStructuredArg_Inline asserts a non-empty inline value is returned
// verbatim (trimmed) as the JSON payload.
func TestReadStructuredArg_Inline(t *testing.T) {
	raw, err := readStructuredArg(`  [{"a":1}]  `, "", nil)
	require.NoError(t, err)
	require.Equal(t, `[{"a":1}]`, string(raw))
}

// TestReadStructuredArg_File asserts the file path is read when no inline
// value is given.
func TestReadStructuredArg_File(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/payload.json"
	require.NoError(t, os.WriteFile(path, []byte(`{"k":"v"}`), 0o600))

	raw, err := readStructuredArg("", path, nil)
	require.NoError(t, err)
	require.Equal(t, `{"k":"v"}`, string(raw))
}

// TestReadStructuredArg_Stdin asserts "-" reads from the injected reader.
func TestReadStructuredArg_Stdin(t *testing.T) {
	raw, err := readStructuredArg("-", "", strings.NewReader(`{"from":"stdin"}`))
	require.NoError(t, err)
	require.Equal(t, `{"from":"stdin"}`, string(raw))
}

// TestReadStructuredArg_Precedence asserts inline beats the file path.
func TestReadStructuredArg_Precedence(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/payload.json"
	require.NoError(t, os.WriteFile(path, []byte(`{"src":"file"}`), 0o600))

	raw, err := readStructuredArg(`{"src":"inline"}`, path, nil)
	require.NoError(t, err)
	require.Equal(t, `{"src":"inline"}`, string(raw))
}

// TestReadStructuredArg_None asserts no source yields (nil, nil) so the caller
// can decide whether the argument is required.
func TestReadStructuredArg_None(t *testing.T) {
	raw, err := readStructuredArg("", "", nil)
	require.NoError(t, err)
	require.Nil(t, raw)
}

// TestReadStructuredArg_InvalidJSON asserts a syntactically-broken payload is
// rejected with a JSON error.
func TestReadStructuredArg_InvalidJSON(t *testing.T) {
	_, err := readStructuredArg(`{not json`, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not valid JSON")
}

// TestReadStructuredArg_EmptyPayload asserts an empty (whitespace-only) inline
// value is rejected rather than treated as "no source".
func TestReadStructuredArg_EmptyPayload(t *testing.T) {
	_, err := readStructuredArg(`   `, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty")
}

// TestReadStructuredArg_MissingFile asserts a bad file path surfaces a read
// error naming the path.
func TestReadStructuredArg_MissingFile(t *testing.T) {
	_, err := readStructuredArg("", t.TempDir()+"/does-not-exist.json", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading JSON file")
}
