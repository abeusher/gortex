package store_sqlite

import (
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// This file implements graph.ContentSearcher on the SQLite backend using
// the content_fts FTS5 virtual table declared in schema.go — the
// dedicated, on-disk full-text index for CONTENT (data_class="content")
// section bodies, kept physically separate from symbol_fts so content
// text never reaches the symbol search or the code-oriented graph passes.
//
// Streamed build: WipeContent(repoPrefix) once at the start of a full
// index, AppendContent each content file's sections as they are parsed
// (no per-file wipe), then BuildContentIndex to merge segments.
// Incremental reindex of one content file is WipeContentFile +
// AppendContent.

// Compile-time assertion: *Store satisfies the content-search capability.
var _ graph.ContentSearcher = (*Store)(nil)

// contentInsertChunkRows bounds rows per multi-row INSERT. Each row binds
// 5 host params (node_id, repo_prefix, file_path, ordinal, body); 180 rows
// is 900 params, comfortably under SQLite's default 999-variable limit.
const contentInsertChunkRows = 180

// WipeContent removes a repo's content rows before a full rebuild. Empty
// repoPrefix wipes the whole table (single-repo / conformance behaviour).
func (s *Store) WipeContent(repoPrefix string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM content_fts WHERE repo_prefix = ?`, repoPrefix)
	return err
}

// WipeContentFile removes one file's content rows — the incremental
// reindex path when a single content file changes.
func (s *Store) WipeContentFile(filePath string) error {
	if filePath == "" {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.Exec(`DELETE FROM content_fts WHERE file_path = ?`, filePath)
	return err
}

// AppendContent inserts content rows for repoPrefix without wiping — the
// streamed per-file build path. Callers wipe (whole repo or one file)
// first. Rows with an empty NodeID are skipped.
func (s *Store) AppendContent(repoPrefix string, items []graph.ContentFTSItem) error {
	if len(items) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			_ = tx.Rollback()
		}
	}()

	for start := 0; start < len(items); start += contentInsertChunkRows {
		end := minInt(start+contentInsertChunkRows, len(items))
		chunk := items[start:end]

		var b strings.Builder
		b.WriteString(`INSERT INTO content_fts (node_id, repo_prefix, file_path, ordinal, body) VALUES `)
		args := make([]any, 0, len(chunk)*5)
		for _, it := range chunk {
			if it.NodeID == "" {
				continue
			}
			if len(args) > 0 {
				b.WriteByte(',')
			}
			b.WriteString(`(?,?,?,?,?)`)
			args = append(args, it.NodeID, repoPrefix, it.FilePath, it.Ordinal, it.Body)
		}
		if len(args) == 0 {
			continue
		}
		if _, err := tx.Exec(b.String(), args...); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	commit = true
	return nil
}

// BuildContentIndex opportunistically merges FTS5 segments (a read-latency
// improvement). Like BuildSymbolIndex it is a no-op for correctness — the
// FTS index is maintained incrementally on every insert — and idempotent.
func (s *Store) BuildContentIndex() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, _ = s.db.Exec(`INSERT INTO content_fts(content_fts) VALUES('optimize')`)
	return nil
}

// SearchContent runs a content query scoped to repoPrefix (empty = all
// repos) and returns hits ordered by descending relevance, each carrying a
// short snippet excerpt from the matched body. Reuses the symbol path's
// write-side tokeniser (buildFTSMatch) so the content corpus and queries
// agree on camelCase / path-separator splitting.
func (s *Store) SearchContent(query, repoPrefix string, limit int) ([]graph.ContentHit, error) {
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 20
	}
	match := s.buildFTSMatch(query)
	if match == "" {
		return nil, nil
	}

	var sb strings.Builder
	// snippet() over the body column (index 4): no highlight marks, an
	// ellipsis for elision, ~16 tokens of context. CAST(ordinal AS INTEGER)
	// forces integer affinity so the FTS5 text column scans cleanly into an
	// int.
	sb.WriteString(`SELECT node_id, file_path, CAST(ordinal AS INTEGER), snippet(content_fts, 4, '', '', '…', 16), bm25(content_fts) FROM content_fts WHERE content_fts MATCH ?`)
	args := []any{match}
	if repoPrefix != "" {
		sb.WriteString(` AND repo_prefix = ?`)
		args = append(args, repoPrefix)
	}
	sb.WriteString(` ORDER BY bm25(content_fts) LIMIT ?`)
	args = append(args, limit)

	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var hits []graph.ContentHit
	for rows.Next() {
		var (
			id, fp, snip string
			ordinal      int
			score        float64
		)
		if err := rows.Scan(&id, &fp, &ordinal, &snip, &score); err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		// bm25() is negative-better in SQLite; negate so higher = better,
		// matching the ContentHit contract. Rows already arrive best-first.
		hits = append(hits, graph.ContentHit{
			NodeID:   id,
			FilePath: fp,
			Ordinal:  ordinal,
			Score:    -score,
			Snippet:  snip,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return hits, nil
}
