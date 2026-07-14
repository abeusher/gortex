package indexer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/crashpool"
)

const (
	sourceSemanticFingerprintMeta = "source_semantic_fingerprint"
	sourceMetadataFingerprintMeta = "source_metadata_fingerprint"
	sourceCoreFingerprintMeta     = "source_core_fingerprint"
)

type fileDeltaFingerprints struct {
	semantic string
	metadata string
	core     string
}

// preparedExtraction is the parse result produced by the watcher's delta
// probe. A structural edit consumes it in indexFile so the same bytes are not
// parsed twice. src is the transformed source, matching indexFile's input to
// extractFile.
type preparedExtraction struct {
	absPath string
	relPath string
	lang    string
	src     []byte
	result  *parser.ExtractionResult
}

// fileDeltaProbe exposes phase timings and the three delta boundaries used by
// the watcher: metadata-only, artifact-only, and semantic topology.
type fileDeltaProbe struct {
	fingerprints    fileDeltaFingerprints
	derived         derivedFingerprints
	read            time.Duration
	extract         time.Duration
	coverage        time.Duration
	fingerprintTime time.Duration
}

// prepareFileDelta parses the current file once and caches that exact
// extraction for either the bounded refresh or the structural reindex. Cold
// indexing deliberately does not pay this fingerprint cost: an old/missing
// fingerprint gets one conservative structural patch, which stamps the file
// for subsequent edits.
func (idx *Indexer) prepareFileDelta(filePath string) (fileDeltaProbe, bool) {
	var probe fileDeltaProbe
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return probe, false
	}
	relPath := idx.relKey(absPath)

	started := time.Now()
	src, err := os.ReadFile(absPath)
	probe.read = time.Since(started)
	if err != nil {
		return probe, false
	}
	lang, ok := idx.effectiveLanguage(absPath, src)
	if !ok {
		return probe, false
	}
	ext, _ := idx.registry.GetByLanguage(lang)
	if ext == nil {
		return probe, false
	}
	if maxSize := idx.config.MaxFileSize; maxSize > 0 && int64(len(src)) > maxSize {
		return probe, false
	}
	if _, skip := idx.newContentAdmissionGate().skip(lang, int64(len(src))); skip {
		return probe, false
	}
	src = idx.transforms.run(relPath, src)

	var pool *crashpool.Pool
	var quarantine *crashpool.Quarantine
	if idx.crashIsolationEnabled() {
		pool, quarantine = idx.sharedParsePool()
	}
	started = time.Now()
	result, skipped, err := idx.extractFile(pool, quarantine, absPath, relPath, lang, ext, src)
	probe.extract = time.Since(started)
	if quarantine != nil && quarantine.Len() > 0 {
		_ = quarantine.Save()
	}
	if result == nil || skipped || err != nil {
		return probe, false
	}

	started = time.Now()
	idx.applyCoverageDomains(relPath, lang, src, result)
	probe.coverage = time.Since(started)

	started = time.Now()
	fingerprints, ok := extractionGraphFingerprints(result)
	derived, derivedOK := extractionDerivedFingerprints(result)
	probe.fingerprintTime = time.Since(started)
	if !ok || !derivedOK {
		return probe, false
	}
	probe.fingerprints = fingerprints
	probe.derived = derived
	stampExtractionGraphFingerprints(result, fingerprints)
	stampDerivedFingerprints(result, derived)

	idx.preparedMu.Lock()
	if idx.prepared == nil {
		idx.prepared = make(map[string]*preparedExtraction)
	}
	idx.prepared[absPath] = &preparedExtraction{
		absPath: absPath,
		relPath: relPath,
		lang:    lang,
		src:     append([]byte(nil), src...),
		result:  result,
	}
	idx.preparedMu.Unlock()
	return probe, true
}

func (idx *Indexer) takePreparedExtraction(absPath, relPath, lang string, src []byte) (*parser.ExtractionResult, bool) {
	idx.preparedMu.Lock()
	prepared := idx.prepared[absPath]
	delete(idx.prepared, absPath)
	idx.preparedMu.Unlock()
	if prepared == nil || prepared.relPath != relPath || prepared.lang != lang || !bytes.Equal(prepared.src, src) {
		return nil, false
	}
	return prepared.result, true
}

func (idx *Indexer) takePreparedRefresh(filePath string) (*preparedExtraction, bool) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, false
	}
	idx.preparedMu.Lock()
	prepared := idx.prepared[absPath]
	delete(idx.prepared, absPath)
	idx.preparedMu.Unlock()
	if prepared == nil {
		return nil, false
	}
	current, err := os.ReadFile(absPath)
	if err != nil {
		return nil, false
	}
	current = idx.transforms.run(prepared.relPath, current)
	if !bytes.Equal(current, prepared.src) {
		return nil, false
	}
	return prepared, true
}

func (idx *Indexer) discardPreparedExtraction(filePath string) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return
	}
	idx.preparedMu.Lock()
	delete(idx.prepared, absPath)
	idx.preparedMu.Unlock()
}

type fingerprintMode uint8

const (
	fingerprintMetadata fingerprintMode = iota
	fingerprintSemantic
	fingerprintCore
)

var presentationMetaKeys = map[string]struct{}{
	"body": {}, "comment": {}, "comments": {}, "doc": {},
	"documentation": {}, "search_doc": {}, "section_text": {},
	"snippet": {}, "source": {}, "source_text": {},
}

func isFingerprintMeta(key string) bool {
	switch key {
	case sourceSemanticFingerprintMeta, sourceMetadataFingerprintMeta, sourceCoreFingerprintMeta,
		sourceDerivedDeclFingerprintMeta, sourceDerivedImportFingerprintMeta,
		sourceDerivedRuntimeFingerprintMeta, sourceDerivedArtifactFingerprintMeta:
		return true
	default:
		return false
	}
}

func isArtifactNodeKind(kind graph.NodeKind) bool {
	switch kind {
	case graph.KindArtifact, graph.KindDoc, graph.KindLicense, graph.KindRelease, graph.KindTeam, graph.KindTodo:
		return true
	default:
		return false
	}
}

func filteredFingerprintMeta(meta map[string]any, mode fingerprintMode, keepPresentation bool) map[string]any {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]any, len(meta))
	for key, value := range meta {
		if isFingerprintMeta(key) {
			continue
		}
		if mode != fingerprintMetadata && !keepPresentation {
			if _, presentation := presentationMetaKeys[key]; presentation {
				continue
			}
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type edgeFingerprintRow struct {
	From            string         `json:"from"`
	To              string         `json:"to"`
	Kind            graph.EdgeKind `json:"kind"`
	FilePath        string         `json:"file_path"`
	Line            int            `json:"line"`
	Confidence      float64        `json:"confidence"`
	ConfidenceLabel string         `json:"confidence_label"`
	Origin          string         `json:"origin"`
	Tier            string         `json:"tier"`
	CrossRepo       bool           `json:"cross_repo"`
	Alias           string         `json:"alias"`
	Meta            map[string]any `json:"meta"`
}

func extractionFingerprint(result *parser.ExtractionResult, mode fingerprintMode, artifactIDs map[string]struct{}) (string, bool) {
	rows := make([]string, 0, len(result.Nodes)+len(result.Edges))
	for _, n := range result.Nodes {
		if n == nil {
			continue
		}
		if mode == fingerprintCore {
			if _, artifact := artifactIDs[n.ID]; artifact {
				continue
			}
		}
		cp := *n
		if mode != fingerprintMetadata {
			cp.StartLine, cp.EndLine, cp.StartColumn, cp.EndColumn = 0, 0, 0, 0
		}
		cp.Meta = filteredFingerprintMeta(n.Meta, mode, isArtifactNodeKind(n.Kind))
		encoded, err := json.Marshal(&cp)
		if err != nil {
			return "", false
		}
		rows = append(rows, "N\x00"+string(encoded))
	}
	for _, edge := range result.Edges {
		if edge == nil {
			continue
		}
		if mode == fingerprintCore {
			if _, artifact := artifactIDs[edge.From]; artifact {
				continue
			}
			if _, artifact := artifactIDs[edge.To]; artifact {
				continue
			}
		}
		row := edgeFingerprintRow{
			From: edge.From, To: edge.To, Kind: edge.Kind, FilePath: edge.FilePath,
			Line: edge.Line, Confidence: edge.Confidence, ConfidenceLabel: edge.ConfidenceLabel,
			Origin: edge.Origin, Tier: edge.Tier, CrossRepo: edge.CrossRepo, Alias: edge.Alias,
			Meta: filteredFingerprintMeta(edge.Meta, mode, false),
		}
		if mode != fingerprintMetadata {
			row.Line = 0
		}
		encoded, err := json.Marshal(&row)
		if err != nil {
			return "", false
		}
		rows = append(rows, "E\x00"+string(encoded))
	}
	sort.Strings(rows)
	h := sha256.New()
	for _, row := range rows {
		_, _ = h.Write([]byte(row))
		_, _ = h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil)), true
}

func extractionGraphFingerprints(result *parser.ExtractionResult) (fileDeltaFingerprints, bool) {
	if result == nil {
		return fileDeltaFingerprints{}, false
	}
	artifactIDs := make(map[string]struct{})
	for _, n := range result.Nodes {
		if n != nil && isArtifactNodeKind(n.Kind) {
			artifactIDs[n.ID] = struct{}{}
		}
	}
	metadata, ok := extractionFingerprint(result, fingerprintMetadata, artifactIDs)
	if !ok {
		return fileDeltaFingerprints{}, false
	}
	semantic, ok := extractionFingerprint(result, fingerprintSemantic, artifactIDs)
	if !ok {
		return fileDeltaFingerprints{}, false
	}
	core, ok := extractionFingerprint(result, fingerprintCore, artifactIDs)
	if !ok {
		return fileDeltaFingerprints{}, false
	}
	return fileDeltaFingerprints{semantic: semantic, metadata: metadata, core: core}, true
}

func extractionGraphFingerprint(result *parser.ExtractionResult) (string, bool) {
	fingerprints, ok := extractionGraphFingerprints(result)
	return fingerprints.metadata, ok
}

func stampExtractionGraphFingerprints(result *parser.ExtractionResult, fingerprints fileDeltaFingerprints) {
	for _, n := range result.Nodes {
		if n == nil || n.Kind != graph.KindFile {
			continue
		}
		if n.Meta == nil {
			n.Meta = make(map[string]any)
		}
		n.Meta[sourceSemanticFingerprintMeta] = fingerprints.semantic
		n.Meta[sourceMetadataFingerprintMeta] = fingerprints.metadata
		n.Meta[sourceCoreFingerprintMeta] = fingerprints.core
		return
	}
}

func stampExtractionGraphFingerprint(result *parser.ExtractionResult) string {
	fingerprints, ok := extractionGraphFingerprints(result)
	derived, derivedOK := extractionDerivedFingerprints(result)
	if !ok || !derivedOK {
		return ""
	}
	stampExtractionGraphFingerprints(result, fingerprints)
	stampDerivedFingerprints(result, derived)
	return fingerprints.metadata
}

func storedExtractionGraphFingerprints(nodes []*graph.Node) fileDeltaFingerprints {
	for _, n := range nodes {
		if n == nil || n.Kind != graph.KindFile || n.Meta == nil {
			continue
		}
		semantic, _ := n.Meta[sourceSemanticFingerprintMeta].(string)
		metadata, _ := n.Meta[sourceMetadataFingerprintMeta].(string)
		core, _ := n.Meta[sourceCoreFingerprintMeta].(string)
		return fileDeltaFingerprints{semantic: semantic, metadata: metadata, core: core}
	}
	return fileDeltaFingerprints{}
}

func storedExtractionGraphFingerprint(nodes []*graph.Node) string {
	return storedExtractionGraphFingerprints(nodes).metadata
}

func mergeRefreshMeta(oldMeta, freshMeta map[string]any) map[string]any {
	merged := make(map[string]any, len(oldMeta)+len(freshMeta))
	for key, value := range oldMeta {
		if isFingerprintMeta(key) {
			continue
		}
		if _, presentation := presentationMetaKeys[key]; presentation {
			continue
		}
		merged[key] = value
	}
	for key, value := range freshMeta {
		merged[key] = value
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

type edgeRefreshKey struct {
	from  string
	kind  graph.EdgeKind
	alias string
}

func metadataEdgeRefreshes(g graph.Store, graphPath string, priorNodes, freshNodes []*graph.Node, freshEdges []*graph.Edge) ([]graph.EdgeReindex, bool) {
	if len(freshNodes) == 0 {
		return nil, false
	}
	priorByID := make(map[string]*graph.Node, len(priorNodes))
	ids := make([]string, 0, len(priorNodes))
	for _, n := range priorNodes {
		if n == nil {
			continue
		}
		priorByID[n.ID] = n
		ids = append(ids, n.ID)
	}
	if len(priorByID) != len(freshNodes) {
		return nil, false
	}
	newIDs := make(map[string]struct{}, len(freshNodes))
	for _, n := range freshNodes {
		if n == nil || priorByID[n.ID] == nil {
			return nil, false
		}
		newIDs[n.ID] = struct{}{}
	}

	reuse, _ := captureIncrementalState(g, graphPath)
	applyResolvedOutEdges(g, freshEdges, reuse, newIDs)

	freshByKey := make(map[edgeRefreshKey][]*graph.Edge)
	for _, edge := range freshEdges {
		if edge == nil {
			continue
		}
		if _, local := priorByID[edge.From]; !local {
			return nil, false
		}
		key := edgeRefreshKey{from: edge.From, kind: edge.Kind, alias: edge.Alias}
		freshByKey[key] = append(freshByKey[key], edge)
	}
	oldByKey := make(map[edgeRefreshKey][]*graph.Edge)
	for _, edges := range graph.OutEdgesForNodes(g, ids) {
		for _, edge := range edges {
			if edge == nil {
				continue
			}
			key := edgeRefreshKey{from: edge.From, kind: edge.Kind, alias: edge.Alias}
			if _, needed := freshByKey[key]; needed {
				oldByKey[key] = append(oldByKey[key], edge)
			}
		}
	}

	updates := make([]graph.EdgeReindex, 0, len(freshEdges))
	for key, fresh := range freshByKey {
		old := oldByKey[key]
		if len(old) != len(fresh) {
			return nil, false
		}
		sort.Slice(old, func(i, j int) bool {
			if old[i].Line != old[j].Line {
				return old[i].Line < old[j].Line
			}
			return old[i].To < old[j].To
		})
		sort.Slice(fresh, func(i, j int) bool {
			if fresh[i].Line != fresh[j].Line {
				return fresh[i].Line < fresh[j].Line
			}
			return fresh[i].To < fresh[j].To
		})
		for i := range fresh {
			before := old[i]
			after := *before
			after.FilePath = fresh[i].FilePath
			after.Line = fresh[i].Line
			after.Alias = fresh[i].Alias
			after.Meta = mergeRefreshMeta(before.Meta, fresh[i].Meta)
			updates = append(updates, graph.EdgeReindex{
				Edge: &after, OldTo: before.To, OldFilePath: before.FilePath,
				OldLine: before.Line, RefreshIdentity: true,
			})
		}
	}
	return updates, true
}

// applyPreparedMetadataRefresh updates source-owned node metadata/locations and
// stable edge spans without evicting the file or invoking the resolver. A
// shape mismatch is conservative: the caller falls back to structural reindex.
func (idx *Indexer) applyPreparedMetadataRefresh(filePath string, priorNodes []*graph.Node) ([]*graph.Node, bool) {
	prepared, ok := idx.takePreparedRefresh(filePath)
	if !ok || prepared.result == nil {
		return nil, false
	}
	result := prepared.result
	idx.applyRepoPrefix(result.Nodes, result.Edges)
	graphPath := idx.prefixPath(prepared.relPath)

	priorByID := make(map[string]*graph.Node, len(priorNodes))
	for _, n := range priorNodes {
		if n != nil {
			priorByID[n.ID] = n
		}
	}
	if len(priorByID) != len(result.Nodes) {
		return nil, false
	}
	for i, fresh := range result.Nodes {
		if fresh == nil {
			return nil, false
		}
		old := priorByID[fresh.ID]
		if old == nil {
			return nil, false
		}
		cp := *fresh
		cp.Meta = mergeRefreshMeta(old.Meta, fresh.Meta)
		if cp.WorkspaceID == "" {
			cp.WorkspaceID = old.WorkspaceID
		}
		if cp.ProjectID == "" {
			cp.ProjectID = old.ProjectID
		}
		result.Nodes[i] = &cp
	}

	edgeUpdates, ok := metadataEdgeRefreshes(idx.graph, graphPath, priorNodes, result.Nodes, result.Edges)
	if !ok {
		return nil, false
	}
	if cs := idx.contentSearcher(); cs != nil {
		if fp := firstContentFilePath(result.Nodes); fp != "" {
			if err := cs.WipeContentFile(fp); err != nil {
				return nil, false
			}
		}
	}
	idx.streamContentSections(result.Nodes)
	idx.graph.AddBatch(result.Nodes, nil)
	idx.graph.ReindexEdges(edgeUpdates)
	idx.persistFileMeta(prepared.relPath, prepared.src, result)

	searcher, _ := idx.graph.(graph.SymbolSearcher)
	for _, n := range result.Nodes {
		if !idx.shouldIndexForSearch(n) {
			continue
		}
		if idx.search != nil {
			idx.search.Remove(n.ID)
			idx.search.Add(n.ID, searchIndexFields(n, idx.projectName)...)
		}
		if searcher != nil {
			if err := searcher.UpsertSymbolFTS(n.ID, ftsTokensFor(n, idx.projectName)); err != nil {
				return nil, false
			}
		}
	}
	idx.recordFileMtime(prepared.relPath, prepared.absPath)
	return idx.graph.GetFileNodes(graphPath), true
}
