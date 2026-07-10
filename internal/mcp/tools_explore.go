package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/elide"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/rerank"
)

// exploreToolDescription is the one-shot localization verb's advertised
// contract — engineered to be the obvious opening move for any
// task-shaped request. It promises the whole first exploration phase in
// a single call so the agent never decomposes localization into a string
// of granular search/read/callers turns (the measured turn-economy loss).
const exploreToolDescription = "Start here for any task, bug report, or " +
	"\"where is / how does X work\" question. Describe the request in plain " +
	"words (paste the issue, name the area) and get the localized neighborhood " +
	"in ONE call: the ranked likely-involved symbols with their source and call " +
	"paths (callers + callees), plus the files to change — the whole exploration " +
	"phase (5-10 search/read/callers calls) folded into one. Answer or edit " +
	"straight from it; it states when the neighborhood is complete."

// explore tuning. These are generic retrieval parameters — fan-out
// widths and a token ceiling — with no dependence on any particular
// corpus, query vocabulary, or benchmark. The verb takes arbitrary free
// text; nothing here is derived from a fixed task set.
const (
	exploreDefaultBudgetTokens = 9000
	exploreMinBudgetTokens     = 2000
	exploreMaxBudgetTokens     = 24000
	exploreDefaultMaxSymbols   = 10
	exploreMaxMaxSymbols       = 30
	exploreRingCap             = 5 // callers / callees shown per target
	exploreCharsPerToken       = 4 // coarse token estimate for budgeting
	// exploreBodyBudgetShare caps any single full body at this fraction of
	// the total budget, so one huge top-ranked symbol cannot starve the
	// rest of the neighborhood of their bodies.
	exploreBodyBudgetShare = 3
)

// registerExploreTool wires the one-shot localization verb into the tool
// surface. It ships eagerly in the coding-agent + core presets (see the
// preset roster in tool_presets.go) so it is the first thing a task-shaped
// session reaches for.
func (s *Server) registerExploreTool() {
	s.addTool(
		mcp.NewTool("explore",
			mcp.WithDescription(exploreToolDescription),
			mcp.WithString("task", mcp.Required(), mcp.Description("Natural-language description of the task, bug report, or question to localize (e.g. paste an issue body, or 'the retry backoff never triggers on a 429').")),
			mcp.WithNumber("max_symbols", mcp.Description("Max ranked candidate symbols (default 10).")),
			mcp.WithNumber("token_budget", mcp.Description("Response token ceiling (default 9000). Bodies pack until it fills, then demote to signatures; every candidate location is always listed.")),
			mcp.WithString("repo", mcp.Description("Filter results to a specific repository prefix")),
			mcp.WithString("path", mcp.Description("Restrict the neighborhood to one or more sub-paths (comma-separated), anchored at the repo root — a monorepo-service slice.")),
		),
		s.handleExplore,
	)
}

// exploreTarget is one ranked candidate plus its 1-hop neighborhood,
// gathered before rendering so the renderer can honour the token budget.
type exploreTarget struct {
	node    *graph.Node
	score   float64
	callers []*graph.Node
	callees []*graph.Node
	source  string // full body (may be empty for non-source kinds)
}

// handleExplore is the one-shot localization verb: free text in, a ranked
// neighborhood (symbols + source + call paths + file map + completeness
// cue) out, bounded by a token budget, in a single response.
func (s *Server) handleExplore(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task := strings.TrimSpace(req.GetString("task", ""))
	if task == "" {
		return mcp.NewToolResultError("task is required"), nil
	}
	maxSymbols := clampInt(req.GetInt("max_symbols", exploreDefaultMaxSymbols), 1, exploreMaxMaxSymbols)
	budget := clampInt(req.GetInt("token_budget", exploreDefaultBudgetTokens), exploreMinBudgetTokens, exploreMaxBudgetTokens)

	resolved, errResult := s.resolveScope(ctx, req, IntentLocate)
	if errResult != nil {
		return errResult, nil
	}
	eng := s.engineFor(ctx)
	if eng == nil {
		return mcp.NewToolResultError("no indexed repository is available; run index_repository first"), nil
	}
	opts := query.QueryOptions{
		WorkspaceID: resolved.WorkspaceID,
		ProjectID:   resolved.ProjectID,
		RepoAllow:   resolved.RepoAllow,
	}
	rctx := s.buildRerankContext(ctx, task)
	// Over-fetch, then keep the top maxSymbols that are real localization
	// targets — params / locals / closures / imports are never a place a
	// developer edits to fix a report, and they otherwise consume ranking
	// slots and clutter the file map. Test-source symbols are demoted, not
	// dropped: production code is where a report is resolved, but a task
	// genuinely about tests still gets them when production hits run out.
	fetch := clampInt(maxSymbols*4, maxSymbols, 80)
	ranked := eng.SearchSymbolsRanked(task, fetch, opts, rctx)
	var prod, test []*rerank.Candidate
	for _, c := range ranked {
		if c == nil || c.Node == nil || !exploreLocalizableKind(c.Node.Kind) {
			continue
		}
		isTest, _ := c.Node.Meta["is_test"].(bool)
		if isTest || !exploreCodeDefinitionKind(c.Node.Kind) {
			test = append(test, c)
		} else {
			prod = append(prod, c)
		}
	}
	// Bounded per-file diversification (the same demote-only mechanism the
	// ranked search head uses): a localization neighborhood that spans
	// files beats one file's cluster of sibling shims crowding out every
	// other candidate. Nothing is dropped — capped files' extra hits move
	// below not-yet-capped files.
	prodNodes := make([]*graph.Node, len(prod))
	for i, c := range prod {
		prodNodes[i] = c.Node
	}
	_, prod = diversifyByFile(prodNodes, prod, defaultMaxPerFile)
	cands := prod
	if len(cands) > maxSymbols {
		cands = cands[:maxSymbols]
	} else if len(cands) < maxSymbols {
		room := maxSymbols - len(cands)
		if room > len(test) {
			room = len(test)
		}
		cands = append(cands, test[:room]...)
	}
	if len(cands) == 0 {
		return mcp.NewToolResultText(fmt.Sprintf(
			"EXPLORE — %s\n\nNo ranked symbols matched this request. The graph found nothing on the ranked path — widen the wording, or drop to search_text / find_files for a literal or filename lead.",
			truncateOneLine(task, 200))), nil
	}

	ringOpts := query.QueryOptions{Depth: 1, Limit: exploreRingCap * 3, Detail: "brief", WorkspaceID: resolved.WorkspaceID}
	targets := make([]exploreTarget, 0, len(cands))
	for _, c := range cands {
		if c == nil || c.Node == nil {
			continue
		}
		n := c.Node
		t := exploreTarget{node: n, score: c.Score}
		if callers := eng.GetCallers(n.ID, ringOpts); callers != nil {
			t.callers = ringNeighbors(callers.Nodes, n.ID, exploreRingCap)
		}
		if callees := eng.GetCallChain(n.ID, ringOpts); callees != nil {
			t.callees = ringNeighbors(callees.Nodes, n.ID, exploreRingCap)
		}
		t.source = s.manifestSymbolSource(ctx, n)
		targets = append(targets, t)
	}

	return mcp.NewToolResultText(s.renderExplore(task, targets, budget)), nil
}

// renderExplore lays out the ranked neighborhood as a compact, agent-facing
// text block: likely targets (with call paths + source), a file map, and a
// trailing completeness cue — the measured antidote to the cross-check turn.
// Source bodies are packed newest-first until the token budget fills, then
// demoted to signature stubs; every candidate location is always listed.
func (s *Server) renderExplore(task string, targets []exploreTarget, budget int) string {
	var b strings.Builder
	files := map[string][]string{}
	fileOrder := []string{}
	addFile := func(path, sym string) {
		if _, ok := files[path]; !ok {
			fileOrder = append(fileOrder, path)
		}
		files[path] = append(files[path], sym)
	}

	fmt.Fprintf(&b, "EXPLORE — %s\n\n", truncateOneLine(task, 200))
	b.WriteString("Ranked localization neighborhood (graph-verified). Likely targets first; each carries its call paths and source.\n\n")
	b.WriteString("## Likely targets (most-relevant first)\n")

	used := estimateTokens(b.String())
	truncated := false
	for i, t := range targets {
		n := t.node
		path := nodeDisplayPath(n)
		addFile(path, n.Name)

		var head strings.Builder
		fmt.Fprintf(&head, "\n%d. %s  %s  ·  %s  ·  id: %s\n", i+1, n.Name, n.Kind, nodeLoc(n), n.ID)
		if len(t.callers) > 0 {
			fmt.Fprintf(&head, "   ^ callers: %s\n", joinNeighbors(t.callers))
		}
		if len(t.callees) > 0 {
			fmt.Fprintf(&head, "   v calls:   %s\n", joinNeighbors(t.callees))
		}
		b.WriteString(head.String())
		used += estimateTokens(head.String())

		// Source body: full while the budget holds (rank decides order, the
		// budget decides where full source stops; no single body may take
		// more than 1/exploreBodyBudgetShare of the whole budget), signature
		// stub otherwise. The header/locations above are always emitted so
		// file-hit / symbol-hit never depend on budget.
		body := ""
		if t.source != "" {
			cost := estimateTokens(t.source)
			if used+cost <= budget && cost <= budget/exploreBodyBudgetShare {
				body = t.source
			} else {
				if sig, err := elide.CompressString(t.source, n.Language); err == nil && sig != "" {
					body = sig
				} else {
					body = firstLines(t.source, 3)
				}
				if used+estimateTokens(body) > budget {
					body = ""
				}
				truncated = true
			}
		}
		if body != "" {
			fmt.Fprintf(&b, "```%s\n%s\n```\n", fenceLang(n.Language), strings.TrimRight(body, "\n"))
			used += estimateTokens(body)
		}
	}

	b.WriteString("\n## Files to change\n")
	for _, f := range fileOrder {
		fmt.Fprintf(&b, "- %s  ·  %s\n", f, strings.Join(dedupStrings(files[f]), ", "))
	}

	fmt.Fprintf(&b, "\n— Completeness: %d candidate symbol(s) across %d file(s); callers/callees resolved server-side from the graph. This is the ranked neighborhood for the request — a location not listed here is not on the ranked path. Answer (FILES / SYMBOLS / EVIDENCE) or start editing directly from this; the paths and line numbers above are real and citeable.\n",
		len(targets), len(fileOrder))
	if truncated {
		fmt.Fprintf(&b, "  (Some bodies are elided under the %d-token budget; every candidate's location is still listed above — fetch an elided body with get_symbol_source / batch_symbols using the exact `id:` shown on its line.)\n", budget)
	}
	return b.String()
}

// exploreCodeDefinitionKind reports whether a node kind is a code
// definition a developer edits to resolve a report. Non-code graph
// nodes (doc sections, packages, resources, contracts, ...) can rank —
// they are demoted to the fallback pool alongside test symbols rather
// than dropped, so a genuinely docs-shaped task still reaches them.
func exploreCodeDefinitionKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindFunction, graph.KindMethod, graph.KindType,
		graph.KindInterface, graph.KindField, graph.KindConstant,
		graph.KindVariable, graph.KindEnumMember, graph.KindMacro:
		return true
	default:
		return false
	}
}

// exploreLocalizableKind reports whether a node kind is a place a
// developer would actually edit to resolve a report — the localization
// targets. Params, locals, closures, generic params, imports and file
// nodes are structurally never edit targets, so they are dropped from
// both the ranked candidate set and the call-path rings.
func exploreLocalizableKind(k graph.NodeKind) bool {
	switch k {
	case graph.KindParam, graph.KindLocal, graph.KindClosure,
		graph.KindGenericParam, graph.KindImport, graph.KindFile:
		return false
	default:
		return true
	}
}

// ringNeighbors filters a traversal result's nodes to real neighbors (not
// the focus node itself, not param/local/import noise), capped.
func ringNeighbors(nodes []*graph.Node, selfID string, cap int) []*graph.Node {
	out := make([]*graph.Node, 0, cap)
	for _, n := range nodes {
		if n == nil || n.ID == selfID || !exploreLocalizableKind(n.Kind) {
			continue
		}
		out = append(out, n)
		if len(out) >= cap {
			break
		}
	}
	return out
}

// joinNeighbors renders a neighbor ring as "name (path:line), name (path:line)".
func joinNeighbors(nodes []*graph.Node) string {
	parts := make([]string, 0, len(nodes))
	for _, n := range nodes {
		parts = append(parts, fmt.Sprintf("%s (%s)", n.Name, nodeLoc(n)))
	}
	return strings.Join(parts, ", ")
}

// nodeLoc is the citeable "path:startLine-endLine" (or "path:line") location.
func nodeLoc(n *graph.Node) string {
	path := nodeDisplayPath(n)
	if n.EndLine > n.StartLine {
		return fmt.Sprintf("%s:%d-%d", path, n.StartLine, n.EndLine)
	}
	if n.StartLine > 0 {
		return fmt.Sprintf("%s:%d", path, n.StartLine)
	}
	return path
}

// nodeDisplayPath is the repo-relative file path (the scorer's suffix-match
// target and the agent's citeable path).
func nodeDisplayPath(n *graph.Node) string {
	if n.FilePath != "" {
		return n.FilePath
	}
	return n.AbsoluteFilePath
}

// fenceLang maps a node language to a Markdown fence label (best-effort).
func fenceLang(lang string) string {
	if lang == "" {
		return ""
	}
	return lang
}

func estimateTokens(s string) int { return len(s) / exploreCharsPerToken }

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func truncateOneLine(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func dedupStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
