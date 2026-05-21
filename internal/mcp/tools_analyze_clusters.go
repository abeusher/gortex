package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
)

// handleAnalyzeClusters returns the cached community-detection
// result as clusters with richer per-cluster stats than the raw
// `get_communities` surface: density (intra-cluster edges /
// possible-pairs), file-spread (distinct files touched), language
// mix, and a hub identifier.
//
// "Offline clustering" in the spec refers to k-means / DBSCAN over
// embeddings. Per-node embeddings aren't a public API of the indexer
// today, so the analyzer clusters the call graph instead — these ARE
// offline clusters with strong topology-grounded labels, and they
// serve the downstream "show me the conceptual areas of this
// codebase" use case the L12 spec listed.
//
// The `algorithm` argument selects the mechanism: leiden (default,
// the cached modularity communities), louvain (the legacy modularity
// detector), or spectral (recursive Fiedler-vector bisection — pairs
// better with similarity edges where modularity's resolution limit
// blurs boundaries). The wire response echoes the algorithm used.
func (s *Server) handleAnalyzeClusters(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	minSize := max(req.GetInt("min_size", 3), 1)
	limit := max(req.GetInt("limit", 50), 1)
	pathPrefix := strings.TrimSpace(req.GetString("path_prefix", ""))
	algorithm := strings.ToLower(strings.TrimSpace(req.GetString("algorithm", "leiden")))

	var cr *analysis.CommunityResult
	// incrStats is populated only on the Leiden path; it records
	// whether the partition was recomputed incrementally (only the
	// packages that changed since the last call) or in full.
	var incrStats analysis.IncrementalCommunityStats
	switch algorithm {
	case "", "leiden":
		algorithm = "leiden"
		cr, incrStats = s.incrementalCommunities()
	case "louvain":
		cr = analysis.DetectCommunitiesLouvain(s.graph)
	case "spectral":
		cr = analysis.SpectralClusters(s.graph)
	default:
		return mcp.NewToolResultError("analyze clusters: unknown algorithm " + algorithm +
			" (expected: leiden, louvain, spectral)"), nil
	}

	if cr == nil || len(cr.Communities) == 0 {
		return s.respondJSONOrTOON(ctx, req, map[string]any{
			"clusters":  []map[string]any{},
			"total":     0,
			"algorithm": algorithm,
			"note":      "no communities detected on this graph",
		})
	}

	scoped := s.scopedNodes(ctx)
	scopedSet := make(map[string]*graph.Node, len(scoped))
	for _, n := range scoped {
		scopedSet[n.ID] = n
	}

	type clusterRow struct {
		ID           string         `json:"id"`
		Label        string         `json:"label"`
		Hub          string         `json:"hub,omitempty"`
		Size         int            `json:"size"`
		Files        int            `json:"files"`
		FileSpread   float64        `json:"file_spread"`
		Density      float64        `json:"density"`
		Languages    map[string]int `json:"languages"`
		TopFiles     []string       `json:"top_files,omitempty"`
		MemberSample []string       `json:"member_sample,omitempty"`
	}

	rows := make([]clusterRow, 0, len(cr.Communities))
	for _, c := range cr.Communities {
		if c.Size < minSize {
			continue
		}
		if pathPrefix != "" {
			match := false
			for _, f := range c.Files {
				if strings.HasPrefix(f, pathPrefix) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		row := clusterRow{
			ID: c.ID, Label: c.Label, Hub: c.Hub, Size: c.Size,
			Files:     len(c.Files),
			Languages: map[string]int{},
		}

		// File-spread = files-per-member; 1.0 means every member
		// lives in its own file (boundary-heavy), close to 0 means
		// many members per file (file-bound cluster).
		if c.Size > 0 {
			row.FileSpread = roundScore(float64(len(c.Files)) / float64(c.Size))
		}

		// Density requires the intra-cluster edge count. Use the
		// member set + graph in-place; cheap on cluster-sized
		// node lists.
		memberSet := make(map[string]bool, len(c.Members))
		for _, m := range c.Members {
			memberSet[m] = true
		}
		intra := 0
		for _, m := range c.Members {
			for _, e := range s.graph.GetOutEdges(m) {
				if e.Kind != graph.EdgeCalls && e.Kind != graph.EdgeReferences {
					continue
				}
				if memberSet[e.To] {
					intra++
				}
			}
		}
		// Density = intra-edges / possible-directed-pairs.
		if c.Size > 1 {
			possible := c.Size * (c.Size - 1)
			row.Density = roundScore(float64(intra) / float64(possible))
		}

		// Language mix + top files.
		fileCounts := map[string]int{}
		for _, m := range c.Members {
			n := scopedSet[m]
			if n == nil {
				continue
			}
			if n.Language != "" {
				row.Languages[n.Language]++
			}
			if n.FilePath != "" {
				fileCounts[n.FilePath]++
			}
		}
		row.TopFiles = topN(fileCounts, 3)
		row.MemberSample = sliceFirstN(c.Members, 5)

		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Size != rows[j].Size {
			return rows[i].Size > rows[j].Size
		}
		return rows[i].ID < rows[j].ID
	})
	truncated := false
	if len(rows) > limit {
		rows = rows[:limit]
		truncated = true
	}

	resp := map[string]any{
		"clusters":  rows,
		"total":     len(rows),
		"truncated": truncated,
		"algorithm": algorithm,
	}
	// On the Leiden path, report whether the partition was recomputed
	// incrementally (only the changed packages) or in full, so a
	// caller can see the cache working.
	if algorithm == "leiden" {
		recompute := "incremental"
		if !incrStats.Incremental {
			recompute = "full"
		}
		detection := map[string]any{
			"recompute":           recompute,
			"changed_packages":    incrStats.ChangedPackages,
			"total_packages":      incrStats.TotalPackages,
			"repartitioned_nodes": incrStats.RepartitionedNodes,
		}
		if incrStats.FullRecomputeReason != "" {
			detection["full_recompute_reason"] = incrStats.FullRecomputeReason
		}
		resp["detection"] = detection
	}
	return s.respondJSONOrTOON(ctx, req, resp)
}

// handleAnalyzeConcepts labels each cluster with a human-readable
// theme via the wired LLM service. Falls back to deterministic
// path-prefix-based naming when no LLM is available so the tool
// still produces useful output offline.
func (s *Server) handleAnalyzeConcepts(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	minSize := max(req.GetInt("min_size", 3), 1)
	limit := max(req.GetInt("limit", 30), 1)
	maxTokens := max(req.GetInt("max_tokens", 80), 16)
	useLLM := requestBoolDefault(req, "use_llm", s.llmService != nil && s.llmService.Enabled())

	cr := s.getCommunities()
	if cr == nil || len(cr.Communities) == 0 {
		return s.respondJSONOrTOON(ctx, req, map[string]any{
			"concepts": []map[string]any{},
			"total":    0,
			"source":   "communities-empty",
		})
	}

	type conceptRow struct {
		ClusterID  string   `json:"cluster_id"`
		Theme      string   `json:"theme"`
		Files      []string `json:"files,omitempty"`
		MemberSize int      `json:"member_size"`
		Source     string   `json:"source"` // "llm" or "heuristic"
	}

	clusters := append([]analysis.Community(nil), cr.Communities...)
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Size > clusters[j].Size })

	out := make([]conceptRow, 0, len(clusters))
	for _, c := range clusters {
		if c.Size < minSize {
			continue
		}
		if len(out) >= limit {
			break
		}
		theme, source := s.labelConcept(ctx, c, useLLM, maxTokens)
		out = append(out, conceptRow{
			ClusterID:  c.ID,
			Theme:      theme,
			Files:      sliceFirstN(c.Files, 3),
			MemberSize: c.Size,
			Source:     source,
		})
	}

	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"concepts": out,
		"total":    len(out),
	})
}

// labelConcept returns a theme label for the cluster + the source
// the label came from ("llm" or "heuristic"). LLM failures fall
// back to the heuristic — the tool never errors on a single
// cluster's label.
func (s *Server) labelConcept(ctx context.Context, c analysis.Community, useLLM bool, maxTokens int) (string, string) {
	if useLLM && s.llmService != nil && s.llmService.Enabled() {
		prompt := buildConceptPrompt(c)
		ans, err := s.llmService.Generate(ctx, prompt, maxTokens)
		if err == nil {
			t := strings.TrimSpace(ans)
			if t != "" {
				return shortenLabel(t), "llm"
			}
		}
	}
	return heuristicConceptLabel(c), "heuristic"
}

// buildConceptPrompt asks the LLM for a 3-6 word theme covering
// every member file. Anchored to specific evidence so the label
// stays grounded.
func buildConceptPrompt(c analysis.Community) string {
	files := sliceFirstN(c.Files, 8)
	hub := c.Hub
	if hub == "" {
		hub = "(none)"
	}
	return fmt.Sprintf(
		"Name this code cluster with a 3-6 word topic label. Hub: %s. Files: %s. "+
			"Respond with the label only, no quotes, no punctuation.",
		hub, strings.Join(files, ", "))
}

// heuristicConceptLabel returns a deterministic label derived from
// the cluster's hub + common file-path prefix. Used when LLM is
// disabled or fails.
func heuristicConceptLabel(c analysis.Community) string {
	if c.Label != "" {
		return c.Label
	}
	prefix := commonFilePrefix(c.Files)
	if prefix == "" && c.Hub != "" {
		return c.Hub
	}
	if prefix != "" && c.Hub != "" {
		return prefix + " · " + c.Hub
	}
	if prefix != "" {
		return prefix
	}
	return "cluster-" + c.ID
}

// commonFilePrefix returns the longest leading directory shared by
// every file in the list — typically "internal/auth" or similar.
// Empty when files diverge at the root.
func commonFilePrefix(files []string) string {
	if len(files) == 0 {
		return ""
	}
	prefix := filepath.Dir(filepath.ToSlash(files[0]))
	for _, f := range files[1:] {
		dir := filepath.Dir(filepath.ToSlash(f))
		for !strings.HasPrefix(dir+"/", prefix+"/") && prefix != "." && prefix != "/" {
			prefix = filepath.Dir(prefix)
		}
		if prefix == "." || prefix == "/" {
			return ""
		}
	}
	if prefix == "." {
		return ""
	}
	return prefix
}

// shortenLabel trims an LLM response to a single line + ≤80 chars
// so a verbose model doesn't produce a paragraph in the theme field.
func shortenLabel(s string) string {
	s = strings.SplitN(s, "\n", 2)[0]
	s = strings.Trim(s, `"'.`)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// topN returns the n highest-count keys from the map, sorted by
// count DESC then key ASC. Used for top_files in cluster rows.
func topN(counts map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(counts))
	for k, v := range counts {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if len(all) > n {
		all = all[:n]
	}
	out := make([]string, 0, len(all))
	for _, kvp := range all {
		out = append(out, kvp.k)
	}
	return out
}

// sliceFirstN returns the first n elements of s, or s itself if
// shorter. Returns a nil slice for empty input.
func sliceFirstN(s []string, n int) []string {
	if len(s) == 0 {
		return nil
	}
	if len(s) <= n {
		return append([]string(nil), s...)
	}
	return append([]string(nil), s[:n]...)
}
