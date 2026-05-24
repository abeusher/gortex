//go:build lora


// Package store_lora is the LoraDB-backed implementation of
// graph.Store. LoraDB is an embeddable property-graph database
// written in Rust with a Cypher front-end and a thin Go cgo binding
// over its C ABI (`crates/bindings/lora-go`).
//
// API shape differs from go-kuzu: Lora exposes one Database type
// (no separate Connection) and a single Execute method that returns
// a fully-materialised *Result {Columns, Rows} — no streaming
// iterator, no prepared statements. We translate every graph.Store
// method onto a per-call Cypher statement with parameter binding.
//
// Schema is one Node label and one Relationship type, parameterised
// by a `kind` property — matching the go-kuzu store's design so the
// two backends are directly comparable.
package store_lora

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	lora "github.com/lora-db/lora/crates/bindings/lora-go"

	"github.com/zzet/gortex/internal/graph"
)

// Store is the LoraDB-backed graph.Store implementation.
type Store struct {
	db *lora.Database

	// writeMu serialises every mutation. Lora's RWMutex wraps the
	// native handle, but Go-side serialisation keeps the conformance
	// suite's 8-goroutine concurrency test deterministic.
	writeMu sync.Mutex

	// resolveMu is the resolver-coordination mutex returned by
	// ResolveMutex.
	resolveMu sync.Mutex

	edgeIdentityRevs atomic.Int64
}

var _ graph.Store = (*Store)(nil)

// Open opens (or creates) a LoraDB at path. The Lora binding stores
// each named database under a configurable directory; we use
// filepath.Dir(path) as the database directory and filepath.Base
// (stripping the file extension) as the database name.
func Open(path string) (*Store, error) {
	dir := filepathDir(path)
	name := filepathBase(path)
	// Strip extension to derive the db name (lora appends .loradb).
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	db, err := lora.New(name, lora.Options{DatabaseDir: dir})
	if err != nil {
		return nil, fmt.Errorf("store_lora: open %q (dir=%q name=%q): %w", path, dir, name, err)
	}
	s := &Store{db: db}
	if err := s.applySchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store_lora: schema: %w", err)
	}
	return s, nil
}

func filepathDir(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[:i]
	}
	return "."
}

func filepathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) ResolveMutex() *sync.Mutex { return &s.resolveMu }

// applySchema sets up the Node label and Edge relationship type.
// Lora's Cypher implementation auto-creates labels on first use; the
// only DDL we need is an index on Node.id for point-lookup speed.
func (s *Store) applySchema() error {
	for _, q := range []string{
		"CREATE INDEX IF NOT EXISTS FOR (n:Node) ON (n.id)",
	} {
		if _, err := s.db.Execute(q, nil); err != nil {
			// Treat schema errors as non-fatal — the index is an
			// optimisation; if the engine doesn't support the syntax,
			// every read still works via the default scan.
			_ = err
		}
	}
	return nil
}

// -- meta encode/decode --------------------------------------------------

func encodeMeta(m map[string]any) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(m); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func decodeMeta(s string) (map[string]any, error) {
	if s == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func nodeParams(n *graph.Node) (lora.Params, error) {
	metaStr, err := encodeMeta(n.Meta)
	if err != nil {
		return nil, err
	}
	return lora.Params{
		"id":           n.ID,
		"kind":         string(n.Kind),
		"name":         n.Name,
		"qual_name":    n.QualName,
		"file_path":    n.FilePath,
		"start_line":   int64(n.StartLine),
		"end_line":     int64(n.EndLine),
		"language":     n.Language,
		"repo_prefix":  n.RepoPrefix,
		"workspace_id": n.WorkspaceID,
		"project_id":   n.ProjectID,
		"abs_path":     n.AbsoluteFilePath,
		"meta":         metaStr,
	}, nil
}

func rowToNode(r lora.Row) *graph.Node {
	if r == nil {
		return nil
	}
	id := asString(r["id"])
	if id == "" {
		return nil
	}
	n := &graph.Node{
		ID:               id,
		Kind:             graph.NodeKind(asString(r["kind"])),
		Name:             asString(r["name"]),
		QualName:         asString(r["qual_name"]),
		FilePath:         asString(r["file_path"]),
		StartLine:        asInt(r["start_line"]),
		EndLine:          asInt(r["end_line"]),
		Language:         asString(r["language"]),
		RepoPrefix:       asString(r["repo_prefix"]),
		WorkspaceID:      asString(r["workspace_id"]),
		ProjectID:        asString(r["project_id"]),
		AbsoluteFilePath: asString(r["abs_path"]),
	}
	if metaStr := asString(r["meta"]); metaStr != "" {
		if m, err := decodeMeta(metaStr); err == nil {
			n.Meta = m
		}
	}
	return n
}

func rowToEdge(r lora.Row) *graph.Edge {
	if r == nil {
		return nil
	}
	e := &graph.Edge{
		From:            asString(r["from_id"]),
		To:              asString(r["to_id"]),
		Kind:            graph.EdgeKind(asString(r["e_kind"])),
		FilePath:        asString(r["file_path"]),
		Line:            asInt(r["line"]),
		Confidence:      asFloat(r["confidence"]),
		ConfidenceLabel: asString(r["confidence_label"]),
		Origin:          asString(r["origin"]),
		Tier:            asString(r["tier"]),
		CrossRepo:       asBool(r["cross_repo"]),
	}
	if metaStr := asString(r["meta"]); metaStr != "" {
		if m, err := decodeMeta(metaStr); err == nil {
			e.Meta = m
		}
	}
	return e
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	}
	return 0
}

func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func panicOnFatal(err error) {
	if err == nil {
		return
	}
	panic(fmt.Errorf("store_lora: %w", err))
}

// -- BulkLoader marker ---------------------------------------------------

var _ graph.BulkLoader = (*Store)(nil)

func (s *Store) BeginBulkLoad()   {}
func (s *Store) FlushBulk() error { return nil }
