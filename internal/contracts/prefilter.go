package contracts

import "bytes"

// srcHasAnyMarker reports whether src contains at least one of the
// given byte markers. Used by contract extractors as a cheap
// short-circuit before running their regex suites — bytes.Contains
// over a handful of distinctive substrings skips the vast majority
// of files in a repo that has zero usage of a given contract style
// (e.g. gRPC-free TS files, WebSocket-free Go files). See
// spec-extractor-perf.md §6.3 for the pattern's origin (gRPC) and
// measured impact.
func srcHasAnyMarker(src []byte, markers [][]byte) bool {
	for _, m := range markers {
		if bytes.Contains(src, m) {
			return true
		}
	}
	return false
}
