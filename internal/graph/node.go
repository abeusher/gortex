package graph

type NodeKind string

const (
	KindFile      NodeKind = "file"
	KindPackage   NodeKind = "package"
	KindFunction  NodeKind = "function"
	KindMethod    NodeKind = "method"
	KindType      NodeKind = "type"
	KindInterface NodeKind = "interface"
	KindVariable  NodeKind = "variable"
	KindImport    NodeKind = "import"
	KindContract  NodeKind = "contract"
	// KindField represents a struct field, class property, or record
	// field — anything addressable as `owner.field`. ID convention:
	// `<file>::<owner>.<field>`. EdgeMemberOf links the field to its
	// owning type. Languages that already emitted class properties as
	// KindVariable (TypeScript, PHP) keep doing so for backwards
	// compatibility — KindField is reserved for languages that
	// previously emitted only type-ref edges from fields (Go, Rust,
	// Java, C#).
	KindField NodeKind = "field"
)

var validNodeKinds = map[NodeKind]bool{
	KindFile: true, KindPackage: true, KindFunction: true,
	KindMethod: true, KindType: true, KindInterface: true,
	KindVariable: true, KindImport: true, KindContract: true,
	KindField: true,
}

type Node struct {
	ID        string   `json:"id"`
	Kind      NodeKind `json:"kind"`
	Name      string   `json:"name"`
	QualName  string   `json:"qual_name,omitempty"`
	FilePath  string   `json:"file_path"`
	StartLine int      `json:"start_line"`
	// EndLine is omitted when zero — File-kind nodes don't have ranges.
	EndLine    int            `json:"end_line,omitempty"`
	Language   string         `json:"language"`
	Meta       map[string]any `json:"meta,omitempty"`
	RepoPrefix string         `json:"repo_prefix,omitempty"`
	// WorkspaceID is the hard graph boundary slug. Two nodes with
	// different WorkspaceIDs are not allowed to be matched as contract
	// provider/consumer pairs and queries scope by it by default.
	// Defaults at warmup time to the per-repo `.gortex.yaml::workspace`
	// setting; falls back to RepoPrefix when no workspace is declared
	// (so old configs keep working) and to "" only for snapshot
	// records written before the field existed (gob decodes unknown
	// fields as zero — warmup backfills these from config).
	WorkspaceID string `json:"workspace_id,omitempty"`
	// ProjectID is the soft sub-boundary inside a workspace. One
	// project per repo by default; monorepos can declare projects[] in
	// .gortex.yaml. Contract pairing is bounded to a single
	// (workspace_id, project_id); cross-project contracts become orphans.
	// Defaults to the repo name when no projects[] mapping matches.
	ProjectID string `json:"project_id,omitempty"`
}

// Brief returns a compact representation with only the fields needed for listing.
func (n *Node) Brief() map[string]any {
	b := map[string]any{
		"id":         n.ID,
		"name":       n.Name,
		"kind":       n.Kind,
		"file_path":  n.FilePath,
		"start_line": n.StartLine,
	}
	if n.RepoPrefix != "" {
		b["repo_prefix"] = n.RepoPrefix
	}
	if n.WorkspaceID != "" {
		b["workspace_id"] = n.WorkspaceID
	}
	if n.ProjectID != "" {
		b["project_id"] = n.ProjectID
	}
	// Surface visibility and a short doc snippet when present — Brief
	// is the listing projection used by search_symbols and find_usages,
	// where these two fields meaningfully sharpen the result so the
	// agent can decide without a follow-up get_symbol_source call.
	if v, ok := n.Meta["visibility"].(string); ok && v != "" {
		b["visibility"] = v
	}
	if d, ok := n.Meta["doc"].(string); ok && d != "" {
		// Truncate doc to 80 chars in Brief — the full doc is on the
		// node, this is just the listing teaser.
		const briefDocCap = 80
		if len(d) > briefDocCap {
			d = d[:briefDocCap] + "…"
		}
		b["doc"] = d
	}
	return b
}

func ValidNodeKind(k NodeKind) bool {
	return validNodeKinds[k]
}
