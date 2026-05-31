package store_cobalt

import (
	"strconv"
	"strings"

	cobalt "github.com/cobaltdb/cobaltdb/pkg/engine"

	"github.com/zzet/gortex/internal/graph"
)

// Column projections. SELECT order is fixed and mirrored by scanNode /
// scanEdge; INSERT order is mirrored by nodeValues / edgeValues.
const (
	nodeSelectCols = "id, kind, name, qual_name, file_path, start_line, end_line, language, repo_prefix, workspace_id, project_id, meta"
	edgeSelectCols = "from_id, to_id, kind, file_path, line, confidence, confidence_label, origin, tier, cross_repo, meta"

	nodeInsertCols  = "id, kind, name, name_lower, qual_name, file_path, start_line, end_line, language, repo_prefix, workspace_id, project_id, meta"
	edgeInsertCols  = "edge_key, from_id, to_id, kind, file_path, line, confidence, confidence_label, origin, tier, cross_repo, meta"
	nodeInsertCount = 13
	edgeInsertCount = 12
)

// edgeKeyDelim joins the edge identity tuple into the edges PK. The
// unit-separator byte never appears in symbol IDs, kinds, or paths.
const edgeKeyDelim = "\x1f"

// edgeKeyFor builds the edges primary key from an identity tuple. Used
// directly by ReindexEdge to reconstruct the pre-mutation (old-To) key.
func edgeKeyFor(from, to string, kind graph.EdgeKind, file string, line int) string {
	return strings.Join([]string{
		from, to, string(kind), file, strconv.Itoa(line),
	}, edgeKeyDelim)
}

// edgeKeyOf is the deterministic identity used as the edges primary
// key: (from, to, kind, file_path, line). Re-adding the same logical
// edge produces the same key (idempotent upsert); a different line
// produces a different key (line-disambiguated, both rows kept).
func edgeKeyOf(e *graph.Edge) string {
	return edgeKeyFor(e.From, e.To, e.Kind, e.FilePath, e.Line)
}

// idChunkSize bounds IN-list / multi-row statements so a single
// statement never carries an unbounded parameter count.
const idChunkSize = 500

// chunkStrings splits ids into sub-slices of at most size elements.
func chunkStrings(ids []string, size int) [][]string {
	if size <= 0 {
		size = idChunkSize
	}
	var out [][]string
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

// dedupeStrings returns the input with duplicates and empties removed,
// preserving first-seen order.
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	var out []string
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// asFloat64 coerces a value scanned through `any` to float64. CobaltDB
// may surface a REAL column as int64 (whole numbers) or float64; both
// flow through here.
func asFloat64(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int64:
		return float64(x)
	case int:
		return float64(x)
	case int32:
		return float64(x)
	default:
		return 0
	}
}

// strArgs widens a string slice to the []any an Exec/Query call expects.
func strArgs(ss []string) []any {
	args := make([]any, len(ss))
	for i, v := range ss {
		args[i] = v
	}
	return args
}

// scanNode reads one row projected as nodeSelectCols into a *graph.Node.
func scanNode(rows *cobalt.Rows) *graph.Node {
	var (
		id, kind, name, qual, file, lang, repo, ws, proj, meta string
		start, end                                             int64
	)
	if err := rows.Scan(&id, &kind, &name, &qual, &file, &start, &end, &lang, &repo, &ws, &proj, &meta); err != nil {
		return nil
	}
	return &graph.Node{
		ID:          id,
		Kind:        graph.NodeKind(kind),
		Name:        name,
		QualName:    qual,
		FilePath:    file,
		StartLine:   int(start),
		EndLine:     int(end),
		Language:    lang,
		RepoPrefix:  repo,
		WorkspaceID: ws,
		ProjectID:   proj,
		Meta:        decodeMeta(meta),
	}
}

// scanEdge reads one row projected as edgeSelectCols into a *graph.Edge.
//
// confidence is scanned through `any`: CobaltDB stores a whole-number
// REAL (e.g. 1.0) as an integer and surfaces it as int64, and the
// engine's Scan refuses a direct int64→*float64 conversion. Reading it
// untyped and coercing with asFloat64 tolerates both representations.
func scanEdge(rows *cobalt.Rows) *graph.Edge {
	var (
		from, to, kind, file, clabel, origin, tier, meta string
		line, cross                                      int64
		conf                                             any
	)
	if err := rows.Scan(&from, &to, &kind, &file, &line, &conf, &clabel, &origin, &tier, &cross, &meta); err != nil {
		return nil
	}
	return &graph.Edge{
		From:            from,
		To:              to,
		Kind:            graph.EdgeKind(kind),
		FilePath:        file,
		Line:            int(line),
		Confidence:      asFloat64(conf),
		ConfidenceLabel: clabel,
		Origin:          origin,
		Tier:            tier,
		CrossRepo:       cross != 0,
		Meta:            decodeMeta(meta),
	}
}

// nodeValues returns the INSERT argument slice for a node in
// nodeInsertCols order. name_lower powers case-insensitive substring
// search; meta is JSON. No value is ever nil/NULL.
func nodeValues(n *graph.Node) []any {
	return []any{
		n.ID,
		string(n.Kind),
		n.Name,
		strings.ToLower(n.Name),
		n.QualName,
		n.FilePath,
		n.StartLine,
		n.EndLine,
		n.Language,
		n.RepoPrefix,
		n.WorkspaceID,
		n.ProjectID,
		encodeMeta(n.Meta),
	}
}

// edgeValues returns the INSERT argument slice for an edge in
// edgeInsertCols order.
func edgeValues(e *graph.Edge) []any {
	cross := 0
	if e.CrossRepo {
		cross = 1
	}
	return []any{
		edgeKeyOf(e),
		e.From,
		e.To,
		string(e.Kind),
		e.FilePath,
		e.Line,
		e.Confidence,
		e.ConfidenceLabel,
		e.Origin,
		e.Tier,
		cross,
		encodeMeta(e.Meta),
	}
}

// buildInsert assembles a multi-row "INSERT OR REPLACE" statement with
// rowCount value tuples of perRow placeholders each.
func buildInsert(table, cols string, perRow, rowCount int) string {
	var b strings.Builder
	b.WriteString("INSERT OR REPLACE INTO ")
	b.WriteString(table)
	b.WriteByte('(')
	b.WriteString(cols)
	b.WriteString(") VALUES ")
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?,", perRow), ",") + ")"
	for i := 0; i < rowCount; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(tuple)
	}
	return b.String()
}

// placeholders returns "?, ?, ?" for n parameters — for IN (...) lists.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
