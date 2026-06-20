package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// readStructuredArg resolves a JSON argument from, in precedence order:
//
//  1. the inline value, when non-empty and not "-"
//  2. the file path, when non-empty
//  3. stdin, when the inline value is exactly "-"
//
// It is the shared lowering primitive for the edit verb's JSON-shaped
// parameters (verify_change's changes, preview_edit's workspace_edit,
// simulate_chain's steps, batch_edit's edits, change_contract's ranges).
// The returned value is the verbatim JSON bytes (validated as syntactically
// well-formed JSON); the caller decides whether to forward it as a string or
// a parsed value. An empty result (no source provided) returns (nil, nil) so
// the caller can decide whether the argument is required.
//
// stdinReader is injected so tests can drive the "-" path without touching
// the process's real stdin; nil falls back to os.Stdin.
func readStructuredArg(inline, file string, stdinReader io.Reader) (json.RawMessage, error) {
	var (
		data   []byte
		source string
	)
	switch {
	case inline == "-":
		r := stdinReader
		if r == nil {
			r = os.Stdin
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("reading JSON from stdin: %w", err)
		}
		data, source = b, "stdin"
	case inline != "":
		data, source = []byte(inline), "inline JSON"
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading JSON file %s: %w", file, err)
		}
		data, source = b, fmt.Sprintf("file %s", file)
	default:
		return nil, nil
	}

	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("%s is empty", source)
	}
	if !json.Valid([]byte(trimmed)) {
		return nil, fmt.Errorf("%s is not valid JSON", source)
	}
	return json.RawMessage(trimmed), nil
}
