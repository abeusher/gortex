package mcp

import (
	"sort"
	"strings"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// FacadeSurfaceVersion is the negotiated name of the compact, operation-
// dispatched MCP surface. Legacy tool names remain registered; this surface is
// a stable compatibility facade over those handlers.
const FacadeSurfaceVersion = "facade-v1"

type facadeEffect string

const (
	facadeEffectRead          facadeEffect = "read"
	facadeEffectLocalWrite    facadeEffect = "local_write"
	facadeEffectControlWrite  facadeEffect = "control_write"
	facadeEffectSessionWrite  facadeEffect = "session_write"
	facadeEffectExternalWrite facadeEffect = "external_write"
)

// facadeOperationSpec is the canonical mapping between a stable facade
// operation and its legacy implementation. Fixed arguments are injected after
// user arguments and therefore cannot be overridden (used for effect-safe
// extraction such as analyze(kind=temporal_verify)).
type facadeOperationSpec struct {
	Facade    string
	Operation string
	Legacy    string
	Effect    facadeEffect
	Fixed     map[string]any
	Hidden    bool
}

type capturedFacadeTool struct {
	tool    mcpgo.Tool
	handler server.ToolHandlerFunc
}

// facadeRegistry is per-server because optional handlers (LLM, overlays,
// proxy controls) can be registered after NewServer. The operation table is
// immutable; captured availability is protected for those late registrations.
type facadeRegistry struct {
	mu       sync.RWMutex
	byFacade map[string]map[string]facadeOperationSpec
	byLegacy map[string][]facadeOperationSpec
	captured map[string]capturedFacadeTool
}

func newFacadeRegistry() *facadeRegistry {
	r := &facadeRegistry{
		byFacade: make(map[string]map[string]facadeOperationSpec),
		byLegacy: make(map[string][]facadeOperationSpec),
		captured: make(map[string]capturedFacadeTool),
	}
	for _, spec := range facadeOperationSpecs() {
		if !spec.Hidden {
			ops := r.byFacade[spec.Facade]
			if ops == nil {
				ops = make(map[string]facadeOperationSpec)
				r.byFacade[spec.Facade] = ops
			}
			if _, exists := ops[spec.Operation]; exists {
				panic("duplicate MCP facade operation: " + spec.Facade + "." + spec.Operation)
			}
			ops[spec.Operation] = spec
		}
		r.byLegacy[spec.Legacy] = append(r.byLegacy[spec.Legacy], spec)
	}
	return r
}

func (r *facadeRegistry) capture(tool mcpgo.Tool, handler server.ToolHandlerFunc) {
	if r == nil || handler == nil || len(r.byLegacy[tool.Name]) == 0 {
		return
	}
	r.mu.Lock()
	r.captured[tool.Name] = capturedFacadeTool{tool: tool, handler: handler}
	r.mu.Unlock()
}

func (r *facadeRegistry) operation(facade, operation string) (facadeOperationSpec, bool) {
	if r == nil {
		return facadeOperationSpec{}, false
	}
	spec, ok := r.byFacade[facade][operation]
	return spec, ok
}

func (r *facadeRegistry) legacy(name string) (capturedFacadeTool, bool) {
	if r == nil {
		return capturedFacadeTool{}, false
	}
	r.mu.RLock()
	tool, ok := r.captured[name]
	r.mu.RUnlock()
	return tool, ok
}

func (r *facadeRegistry) operations(facade string) []facadeOperationSpec {
	if r == nil {
		return nil
	}
	ops := r.byFacade[facade]
	out := make([]facadeOperationSpec, 0, len(ops))
	for _, spec := range ops {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Operation < out[j].Operation })
	return out
}

func (r *facadeRegistry) facades() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.byFacade)+1)
	for name := range r.byFacade {
		out = append(out, name)
	}
	// capabilities is implemented directly rather than forwarding to a
	// legacy handler. Its legacy introspection operations are still recorded
	// in the registry for migration completeness.
	if _, ok := r.byFacade["capabilities"]; !ok {
		out = append(out, "capabilities")
	}
	sort.Strings(out)
	return out
}

func (r *facadeRegistry) mapsLegacy(name string) bool {
	return r != nil && len(r.byLegacy[name]) > 0
}

func facadeToolNames() []string {
	return []string{
		"analyze", "ask", "capabilities", "change", "edit", "explore",
		"overlay", "pr", "publish_review", "read", "recall", "refactor",
		"relations", "remember", "response", "review", "search", "session",
		"trace", "workspace", "workspace_admin",
	}
}

func isFacadeToolName(name string) bool {
	for _, candidate := range facadeToolNames() {
		if candidate == name {
			return true
		}
	}
	return false
}

func isDedicatedFacadeTool(name string) bool {
	// These four names pre-date facade-v1 and retain their legacy behavior
	// outside a facade session. Every other facade name is new and hidden from
	// legacy surfaces.
	switch name {
	case "analyze", "ask", "explore", "review":
		return false
	default:
		return isFacadeToolName(name)
	}
}

func addFacadeGroup(dst *[]facadeOperationSpec, facade string, effect facadeEffect, ops map[string]string) {
	keys := make([]string, 0, len(ops))
	for op := range ops {
		keys = append(keys, op)
	}
	sort.Strings(keys)
	for _, op := range keys {
		*dst = append(*dst, facadeOperationSpec{Facade: facade, Operation: op, Legacy: ops[op], Effect: effect})
	}
}

// facadeOperationSpecs is the single v1 migration table. Every legacy MCP
// tool maps to exactly one ordinary facade operation, except deliberate
// effect splits such as analyze(kind=temporal_verify), which has a second,
// mutating entry under workspace_admin.
func facadeOperationSpecs() []facadeOperationSpec {
	var specs []facadeOperationSpec
	addFacadeGroup(&specs, "explore", facadeEffectRead, map[string]string{
		"context": "smart_context", "closure": "context_closure", "outline": "get_repo_outline",
		"plan": "plan_turn", "prefetch": "prefetch_context", "suggest": "suggest_queries",
		"task": "explore", "wakeup": "gortex_wakeup",
	})
	addFacadeGroup(&specs, "search", facadeEffectRead, map[string]string{
		"artifacts": "search_artifacts", "ast": "search_ast", "completion": "graph_completion_search",
		"files": "find_files", "symbols": "search_symbols", "text": "search_text", "winnow": "winnow_symbols",
	})
	addFacadeGroup(&specs, "read", facadeEffectRead, map[string]string{
		"artifact": "get_artifact", "editing_context": "get_editing_context", "file": "read_file",
		"history": "get_symbol_history", "source": "get_symbol_source", "summary": "get_file_summary",
		"symbol": "get_symbol", "symbols": "batch_symbols",
	})
	addFacadeGroup(&specs, "relations", facadeEffectRead, map[string]string{
		"callers": "get_callers", "cluster": "get_cluster", "declaration": "find_declaration",
		"dependencies": "get_dependencies", "dependents": "get_dependents", "hierarchy": "get_class_hierarchy",
		"implementations": "find_implementations", "import_path": "find_import_path", "overrides": "find_overrides",
		"references": "check_references", "usages": "find_usages",
	})
	addFacadeGroup(&specs, "trace", facadeEffectRead, map[string]string{
		"call_chain": "get_call_chain", "cfg": "get_cfg", "cursor": "nav", "flow": "flow_between",
		"graph": "graph_query", "path": "trace_path", "taint": "taint_paths", "walk": "walk_graph",
	})
	addFacadeGroup(&specs, "analyze", facadeEffectRead, map[string]string{
		"agent_config": "audit_agent_config", "architecture": "get_architecture", "citation": "verify_citation",
		"clones": "find_clones", "co_change": "find_co_changing_symbols", "communities": "get_communities",
		"contracts": "contracts", "coupling": "get_coupling_metrics", "extraction": "get_extraction_candidates",
		"graph": "analyze", "health": "audit_health", "inspections": "run_inspections",
		"inspection_catalog": "list_inspections", "knowledge_gaps": "get_knowledge_gaps", "lint": "lint_file",
		"processes": "get_processes", "recent_changes": "get_recent_changes", "replay": "replay_episode",
		"surprising_connections": "get_surprising_connections", "untested": "get_untested_symbols", "why": "why",
		"churn": "get_churn_rate",
	})
	addFacadeGroup(&specs, "ask", facadeEffectRead, map[string]string{"research": "ask"})
	addFacadeGroup(&specs, "change", facadeEffectRead, map[string]string{
		"api_impact": "api_impact", "code_actions": "get_code_actions", "compare_branches": "compare_branches",
		"compare_overlay": "compare_with_overlay", "contract": "change_contract", "detect": "detect_changes",
		"diagnostics": "get_diagnostics", "edit_plan": "get_edit_plan", "guards": "check_guards",
		"impact": "explain_change_impact", "overlay_branches": "overlay_branches", "overlay_state": "overlay_list",
		"pattern": "suggest_pattern", "preview": "preview_edit", "ranges": "symbols_for_ranges",
		"tests": "get_test_targets", "verify": "verify_change",
	})
	// simulate_chain can persist an overlay when keep=true. The read-only
	// change facade fixes keep=false; persistent simulations belong to overlay.
	specs = append(specs, facadeOperationSpec{
		Facade: "change", Operation: "simulate", Legacy: "simulate_chain",
		Effect: facadeEffectRead, Fixed: map[string]any{"keep": false},
	})
	addFacadeGroup(&specs, "edit", facadeEffectLocalWrite, map[string]string{
		"batch": "batch_edit", "docs": "generate_docs", "export_graph": "export_graph", "file": "edit_file", "scaffold": "scaffold",
		"skill": "generate_skill", "symbol": "edit_symbol", "wiki": "generate_wiki", "write": "write_file",
	})
	specs = append(specs, facadeOperationSpec{
		Facade: "edit", Operation: "apply_overlay", Legacy: "overlay_merge",
		Effect: facadeEffectLocalWrite, Fixed: map[string]any{"to_disk": true},
	})
	addFacadeGroup(&specs, "refactor", facadeEffectLocalWrite, map[string]string{
		"apply_code_action": "apply_code_action", "delete": "safe_delete_symbol", "fix_all": "fix_all_in_file",
		"inline": "inline_symbol", "move": "move_symbol", "rename": "rename_symbol",
	})
	addFacadeGroup(&specs, "review", facadeEffectRead, map[string]string{
		"critique": "critique_review", "diff_context": "diff_context", "pack": "review_pack",
		"pr_context": "pr_review_context", "questions": "suggested_review_questions", "run": "review",
		"sibling_context": "sibling_diff_context",
	})
	addFacadeGroup(&specs, "publish_review", facadeEffectExternalWrite, map[string]string{"post": "post_review"})
	addFacadeGroup(&specs, "pr", facadeEffectRead, map[string]string{
		"conflicts": "conflicts_prs", "impact": "get_pr_impact", "list": "list_prs",
		"reviewers": "suggest_reviewers", "risk": "pr_risk", "triage": "triage_prs",
	})
	addFacadeGroup(&specs, "recall", facadeEffectRead, map[string]string{
		"distill": "distill_session", "memories": "query_memories", "notebook_find": "notebook_find",
		"notebook_list": "notebook_list", "notebook_show": "notebook_show", "notes": "query_notes",
		"onboarding": "check_onboarding_performed", "surface": "surface_memories",
	})
	addFacadeGroup(&specs, "remember", facadeEffectLocalWrite, map[string]string{
		"edit_memory": "edit_memory", "memory": "store_memory", "note": "save_note",
		"notebook": "notebook_save", "notebook_used": "notebook_used", "rename_memory": "rename_memory",
		"suppress_finding": "suppress_finding",
	})
	addFacadeGroup(&specs, "workspace", facadeEffectRead, map[string]string{
		"active_project": "get_active_project", "graph": "graph_stats", "index": "index_health",
		"info": "workspace_info", "project": "query_project", "proxy": "proxy_status",
		"repos": "list_repos", "scopes": "list_scopes",
	})
	addFacadeGroup(&specs, "workspace_admin", facadeEffectControlWrite, map[string]string{
		"delete_scope": "delete_scope", "enrich_churn": "enrich_churn",
		"enrich_releases": "enrich_releases", "feedback": "feedback", "index": "index_repository",
		"reindex": "reindex_repository", "save_scope": "save_scope", "set_active_project": "set_active_project",
		"track": "track_repository", "untrack": "untrack_repository",
	})
	addFacadeGroup(&specs, "session", facadeEffectSessionWrite, map[string]string{
		"agents": "agent_registry", "planning_mode": "set_planning_mode", "proxy_disable": "proxy_disable",
		"proxy_enable": "proxy_enable", "subscribe_daemon_health": "subscribe_daemon_health",
		"subscribe_diagnostics": "subscribe_diagnostics", "subscribe_graph_invalidated": "subscribe_graph_invalidated",
		"subscribe_stale_refs": "subscribe_stale_refs", "subscribe_workspace_readiness": "subscribe_workspace_readiness",
		"unsubscribe_daemon_health": "unsubscribe_daemon_health", "unsubscribe_diagnostics": "unsubscribe_diagnostics",
		"unsubscribe_graph_invalidated": "unsubscribe_graph_invalidated", "unsubscribe_stale_refs": "unsubscribe_stale_refs",
		"unsubscribe_workspace_readiness": "unsubscribe_workspace_readiness", "workflow": "workflow",
	})
	// temporal_verify writes a cache and persists graph verdicts. It is
	// deliberately extracted from the otherwise read-only analyze facade.
	specs = append(specs, facadeOperationSpec{
		Facade: "workspace_admin", Operation: "temporal_verify", Legacy: "analyze",
		Effect: facadeEffectControlWrite, Fixed: map[string]any{"kind": "temporal_verify"},
	})
	addFacadeGroup(&specs, "overlay", facadeEffectSessionWrite, map[string]string{
		"delete": "overlay_delete", "drop": "overlay_drop", "drop_branch": "overlay_drop_branch",
		"fork": "overlay_fork", "keepalive": "overlay_keepalive", "push": "overlay_push",
		"register": "overlay_register", "switch": "overlay_switch",
	})
	specs = append(specs, facadeOperationSpec{
		Facade: "overlay", Operation: "simulate", Legacy: "simulate_chain",
		Effect: facadeEffectSessionWrite, Fixed: map[string]any{"keep": true},
	})
	specs = append(specs, facadeOperationSpec{
		Facade: "overlay", Operation: "merge", Legacy: "overlay_merge",
		Effect: facadeEffectSessionWrite, Fixed: map[string]any{"to_disk": false},
	})
	addFacadeGroup(&specs, "response", facadeEffectRead, map[string]string{
		"export_context": "export_context", "grep": "ctx_grep", "peek": "ctx_peek", "slice": "ctx_slice", "stats": "ctx_stats",
	})
	// Exact compatibility aliases intentionally map to the same canonical
	// operations. They remain callable by legacy name but never become new
	// facade operations.
	specs = append(specs,
		facadeOperationSpec{Facade: "capabilities", Operation: "legacy_profile", Legacy: "tool_profile", Effect: facadeEffectRead, Hidden: true},
		facadeOperationSpec{Facade: "capabilities", Operation: "legacy_search", Legacy: "tools_search", Effect: facadeEffectRead, Hidden: true},
		facadeOperationSpec{Facade: "response", Operation: "grep_compat", Legacy: "grep_results", Effect: facadeEffectRead, Hidden: true},
		facadeOperationSpec{Facade: "response", Operation: "head_compat", Legacy: "head_results", Effect: facadeEffectRead, Hidden: true},
	)
	return specs
}

func normalizeFacadeOperation(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	return value
}
