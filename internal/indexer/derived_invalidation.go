package indexer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

const (
	sourceDerivedDeclFingerprintMeta     = "source_derived_decl_fingerprint"
	sourceDerivedImportFingerprintMeta   = "source_derived_import_fingerprint"
	sourceDerivedRuntimeFingerprintMeta  = "source_derived_runtime_fingerprint"
	sourceDerivedArtifactFingerprintMeta = "source_derived_artifact_fingerprint"
)

type DerivedInvalidationFlags uint32

const (
	DerivedInvalidatesDeclarations DerivedInvalidationFlags = 1 << iota
	DerivedInvalidatesImports
	DerivedInvalidatesRuntime
	DerivedInvalidatesArtifacts
	DerivedInvalidatesTests
	DerivedInvalidatesContracts
)

func (f DerivedInvalidationFlags) Has(flag DerivedInvalidationFlags) bool { return f&flag != 0 }

// DerivedInvalidationPlan is the bounded work contract carried from extraction
// through reconcile. Files and TypeIDs are exact frontiers, not repo-wide hints.
type DerivedInvalidationPlan struct {
	Flags             DerivedInvalidationFlags `json:"flags,omitempty"`
	Files             []string                 `json:"files,omitempty"`
	TypeIDs           []string                 `json:"type_ids,omitempty"`
	BodyOnlyFiles     int                      `json:"body_only_files,omitempty"`
	MetadataOnlyFiles int                      `json:"metadata_only_files,omitempty"`
	InertFiles        int                      `json:"inert_files,omitempty"`
	LegacyFallback    bool                     `json:"legacy_fallback,omitempty"`
}

func (p DerivedInvalidationPlan) Empty() bool {
	return p.Flags == 0 && len(p.Files) == 0 && p.BodyOnlyFiles == 0 && p.MetadataOnlyFiles == 0 && p.InertFiles == 0
}

func (p *DerivedInvalidationPlan) Merge(other DerivedInvalidationPlan) {
	if p == nil {
		return
	}
	p.Flags |= other.Flags
	p.BodyOnlyFiles += other.BodyOnlyFiles
	p.MetadataOnlyFiles += other.MetadataOnlyFiles
	p.InertFiles += other.InertFiles
	p.LegacyFallback = p.LegacyFallback || other.LegacyFallback
	p.Files = appendUniqueSorted(p.Files, other.Files...)
	p.TypeIDs = appendUniqueSorted(p.TypeIDs, other.TypeIDs...)
}

func appendUniqueSorted(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

type derivedFingerprints struct {
	declarations string
	imports      string
	runtime      string
	artifacts    string
}

func (f derivedFingerprints) complete() bool {
	return f.declarations != "" && f.imports != "" && f.runtime != "" && f.artifacts != ""
}

func extractionDerivedFingerprints(result *parser.ExtractionResult) (derivedFingerprints, bool) {
	if result == nil {
		return derivedFingerprints{}, false
	}
	artifactIDs := make(map[string]struct{})
	for _, node := range result.Nodes {
		if node != nil && isArtifactNodeKind(node.Kind) {
			artifactIDs[node.ID] = struct{}{}
		}
	}

	var declarations, imports, runtime, artifacts []string
	for _, node := range result.Nodes {
		if node == nil {
			continue
		}
		row, ok := derivedNodeFingerprintRow(node)
		if !ok {
			return derivedFingerprints{}, false
		}
		if isDeclarationNodeKind(node.Kind) {
			declarations = append(declarations, "N\x00"+row)
		}
		if isImportNodeKind(node.Kind) {
			imports = append(imports, "N\x00"+row)
		}
		if _, artifact := artifactIDs[node.ID]; artifact {
			artifacts = append(artifacts, "N\x00"+row)
		}
	}
	for _, edge := range result.Edges {
		if edge == nil {
			continue
		}
		row, ok := derivedEdgeFingerprintRow(edge)
		if !ok {
			return derivedFingerprints{}, false
		}
		if isDeclarationEdgeKind(edge.Kind) {
			declarations = append(declarations, "E\x00"+row)
		}
		if isImportEdgeKind(edge.Kind) {
			imports = append(imports, "E\x00"+row)
		}
		if isRuntimeDerivedEdgeKind(edge.Kind) {
			runtime = append(runtime, "E\x00"+row)
		}
		if _, fromArtifact := artifactIDs[edge.From]; fromArtifact {
			artifacts = append(artifacts, "E\x00"+row)
		} else if _, toArtifact := artifactIDs[edge.To]; toArtifact {
			artifacts = append(artifacts, "E\x00"+row)
		}
	}
	return derivedFingerprints{
		declarations: stableFingerprintRows(declarations),
		imports:      stableFingerprintRows(imports),
		runtime:      stableFingerprintRows(runtime),
		artifacts:    stableFingerprintRows(artifacts),
	}, true
}

func derivedNodeFingerprintRow(node *graph.Node) (string, bool) {
	cp := *node
	cp.StartLine, cp.EndLine, cp.StartColumn, cp.EndColumn = 0, 0, 0, 0
	cp.Meta = derivedFingerprintMeta(node.Meta)
	encoded, err := json.Marshal(&cp)
	return string(encoded), err == nil
}

func derivedEdgeFingerprintRow(edge *graph.Edge) (string, bool) {
	row := edgeFingerprintRow{
		From: edge.From, To: edge.To, Kind: edge.Kind, FilePath: edge.FilePath,
		Confidence: edge.Confidence, ConfidenceLabel: edge.ConfidenceLabel,
		Origin: edge.Origin, Tier: edge.Tier, CrossRepo: edge.CrossRepo, Alias: edge.Alias,
		Meta: derivedFingerprintMeta(edge.Meta),
	}
	encoded, err := json.Marshal(&row)
	return string(encoded), err == nil
}

func derivedFingerprintMeta(meta map[string]any) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		lower := strings.ToLower(key)
		if isFingerprintMeta(key) {
			continue
		}
		if _, presentation := presentationMetaKeys[key]; presentation {
			continue
		}
		switch lower {
		case "body", "body_hash", "body_text", "clone_sig", "content", "raw_source", "snippet", "source_text":
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stableFingerprintRows(rows []string) string {
	sort.Strings(rows)
	h := sha256.New()
	for _, row := range rows {
		_, _ = h.Write([]byte(row))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func isDeclarationNodeKind(kind graph.NodeKind) bool {
	switch strings.ToLower(string(kind)) {
	case "type", "interface", "class", "trait", "struct", "enum", "function", "method", "field":
		return true
	default:
		return false
	}
}

func isImportNodeKind(kind graph.NodeKind) bool {
	value := strings.ToLower(string(kind))
	return value == "import" || value == "module" || value == "package"
}

func edgeKindContains(kind graph.EdgeKind, needles ...string) bool {
	value := strings.ToLower(string(kind))
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func isDeclarationEdgeKind(kind graph.EdgeKind) bool {
	return edgeKindContains(kind, "extend", "implement", "override", "compose", "inherit", "satisf")
}

func isImportEdgeKind(kind graph.EdgeKind) bool {
	return edgeKindContains(kind, "import", "export", "depend", "module", "reexport")
}

func isRuntimeDerivedEdgeKind(kind graph.EdgeKind) bool {
	return edgeKindContains(kind,
		"call", "read", "write", "emit", "publish", "subscrib", "dispatch", "register",
		"route", "endpoint", "handler", "invoke", "message", "channel", "event", "config")
}

func stampDerivedFingerprints(result *parser.ExtractionResult, fingerprints derivedFingerprints) {
	if result == nil || !fingerprints.complete() {
		return
	}
	for _, node := range result.Nodes {
		if node == nil || node.Kind != graph.KindFile {
			continue
		}
		if node.Meta == nil {
			node.Meta = map[string]any{}
		}
		node.Meta[sourceDerivedDeclFingerprintMeta] = fingerprints.declarations
		node.Meta[sourceDerivedImportFingerprintMeta] = fingerprints.imports
		node.Meta[sourceDerivedRuntimeFingerprintMeta] = fingerprints.runtime
		node.Meta[sourceDerivedArtifactFingerprintMeta] = fingerprints.artifacts
		return
	}
}

func storedDerivedFingerprints(nodes []*graph.Node) derivedFingerprints {
	for _, node := range nodes {
		if node == nil || node.Kind != graph.KindFile || node.Meta == nil {
			continue
		}
		return derivedFingerprints{
			declarations: stringMeta(node.Meta, sourceDerivedDeclFingerprintMeta),
			imports:      stringMeta(node.Meta, sourceDerivedImportFingerprintMeta),
			runtime:      stringMeta(node.Meta, sourceDerivedRuntimeFingerprintMeta),
			artifacts:    stringMeta(node.Meta, sourceDerivedArtifactFingerprintMeta),
		}
	}
	return derivedFingerprints{}
}

func stringMeta(meta map[string]any, key string) string {
	value, _ := meta[key].(string)
	return value
}

func derivedPlanForDelta(prior, fresh derivedFingerprints, semanticChanged bool, graphPath string, priorNodes, freshNodes []*graph.Node) DerivedInvalidationPlan {
	plan := DerivedInvalidationPlan{Files: []string{graphPath}}
	if !prior.complete() || !fresh.complete() {
		plan.Flags = DerivedInvalidatesDeclarations | DerivedInvalidatesImports | DerivedInvalidatesRuntime | DerivedInvalidatesArtifacts
		plan.LegacyFallback = true
	} else {
		if prior.declarations != fresh.declarations {
			plan.Flags |= DerivedInvalidatesDeclarations
		}
		if prior.imports != fresh.imports {
			plan.Flags |= DerivedInvalidatesImports
		}
		if prior.runtime != fresh.runtime {
			plan.Flags |= DerivedInvalidatesRuntime
		}
		if prior.artifacts != fresh.artifacts {
			plan.Flags |= DerivedInvalidatesArtifacts
		}
	}
	if semanticChanged && looksLikeTestPath(graphPath) {
		plan.Flags |= DerivedInvalidatesTests
	}
	if semanticChanged && plan.Flags == 0 {
		plan.BodyOnlyFiles = 1
	}
	for _, nodes := range [][]*graph.Node{priorNodes, freshNodes} {
		for _, node := range nodes {
			if node != nil && isTypeFrontierNodeKind(node.Kind) {
				plan.TypeIDs = append(plan.TypeIDs, node.ID)
			}
		}
	}
	plan.TypeIDs = appendUniqueSorted(nil, plan.TypeIDs...)
	return plan
}

func isTypeFrontierNodeKind(kind graph.NodeKind) bool {
	switch strings.ToLower(string(kind)) {
	case "type", "interface", "class", "trait", "struct", "enum":
		return true
	default:
		return false
	}
}

func looksLikeTestPath(path string) bool {
	value := strings.ToLower(path)
	return strings.Contains(value, "/test/") || strings.Contains(value, "/tests/") ||
		strings.Contains(value, "_test.") || strings.Contains(value, ".test.") ||
		strings.Contains(value, ".spec.") || strings.HasSuffix(value, "test")
}
