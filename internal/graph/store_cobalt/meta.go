package store_cobalt

import "encoding/json"

// Node.Meta and Edge.Meta are stored as JSON text. CobaltDB has no
// problem with arbitrary UTF-8 (JSON escapes control bytes), so unlike
// the Kuzu backend there is no gob+base64 NUL-workaround. JSON is also
// queryable through the engine's JSON_EXTRACT and is readable on disk.
//
// Decoding into map[string]any yields the conformance-expected dynamic
// types: JSON numbers decode to float64 and JSON booleans to bool,
// which is exactly what the storetest assertions check (coverage_pct as
// float64, uses_cgo as bool, string fields as string).

// encodeMeta serialises a meta map to a JSON string. nil/empty maps and
// any (vanishingly unlikely) marshal error collapse to "" so the column
// is never NULL.
func encodeMeta(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeMeta reverses encodeMeta. Empty input or a decode error yields
// nil (the in-memory backend's zero value for absent meta).
func decodeMeta(s string) map[string]any {
	if s == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}
