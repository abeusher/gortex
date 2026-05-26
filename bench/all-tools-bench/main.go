// all-tools-bench: drives the gortex daemon's MCP-over-HTTP transport
// through a wide tool battery — every non-mutating MCP tool we know
// how to call with sensible defaults. Used to compare backends
// (memory vs ladybug) end-to-end from a separate process — no
// in-process shortcuts.
//
// The bench mirrors daemon-bench's MCP plumbing but expands the
// case list from ~20 search-focused tools to ~70 covering discovery,
// search, navigation, analyze dispatcher, context assembly, verify,
// suggest, notes / memories, and misc structural surfaces.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"time"
)

const sessionHeader = "Mcp-Session-Id"

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

type client struct {
	base    string
	token   string
	session string
	http    *http.Client
	id      int
}

func newClient(base, token string) *client {
	return &client{
		base:  base,
		token: token,
		http:  &http.Client{Timeout: 540 * time.Second},
	}
}

func (c *client) nextID() int {
	c.id++
	return c.id
}

func (c *client) post(body []byte) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.base+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.session != "" {
		req.Header.Set(sessionHeader, c.session)
	}
	return c.http.Do(req)
}

func (c *client) call(method string, params any) (*rpcResp, error) {
	body, err := json.Marshal(rpcReq{JSONRPC: "2.0", ID: c.nextID(), Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	resp, err := c.post(body)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if sid := resp.Header.Get(sessionHeader); sid != "" {
		c.session = sid
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}
	var r rpcResp
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("decode: %w (body=%s)", err, string(raw))
	}
	if r.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", r.Error.Code, r.Error.Message)
	}
	return &r, nil
}

func (c *client) initialize() error {
	_, err := c.call("initialize", map[string]any{
		"protocolVersion": "2026-03-26",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "all-tools-bench", "version": "1.0.0"},
	})
	return err
}

type callRecord struct {
	Label       string `json:"label"`
	Category    string `json:"category"`
	Tool        string `json:"tool"`
	ElapsedMS   int64  `json:"elapsed_ms"`
	OutputBytes int    `json:"output_bytes"`
	Status      string `json:"status"` // "ok" | "error" | "empty"
	Error       string `json:"error,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

type benchCase struct {
	Label    string
	Category string
	Tool     string
	Args     map[string]any
}

// classifyResult inspects a tool's reply text for heuristic
// classification. Returns one of "ok" / "empty" / "argerror".
// "argerror" catches the daemon convention of returning
// `"<param> is required"` or `"<verb> requires …"` text in `content`
// while leaving `isError` false — that's still a failed call from
// the caller's POV but it doesn't look like a transport error.
func classifyResult(text string) string {
	if text == "" {
		return "empty"
	}
	stripped := text
	if len(stripped) > 4096 {
		stripped = stripped[:4096]
	}

	// Bare-error string replies — the daemon convention for "your
	// args were wrong".
	low := stripped
	for _, marker := range []string{
		" is required",
		" requires ",
		"either `pattern`",
		"path is not absolute",
		"symbol not found",
		"no symbols found for file",
		"overlay tools require",
		"unknown ",
	} {
		if bytes.Contains([]byte(low), []byte(marker)) && len(stripped) < 600 {
			return "argerror"
		}
	}

	// Empty list / zero-row replies.
	for _, marker := range []string{
		`"items":[]`,
		`"results":[]`,
		`"symbols":[]`,
		`"records":[]`,
		`"nodes":[]`,
		`"edges":[]`,
		`"matches":[]`,
		`"hits":[]`,
		`"data":[]`,
		`"rows":[]`,
		`"groups":[]`,
		`"clusters":[]`,
		`"communities":[]`,
		`"callers":[]`,
		`"chain":[]`,
		`"paths":[]`,
		`"flows":[]`,
		`"usages":[]`,
		`"implementations":[]`,
		`"references":[]`,
		`"changes":null`,
		`"flags":null`,
		`"orphans":null`,
		`"unreferenced":null`,
		`"events":[]`,
		`"strings":[]`,
		`"topics":[]`,
		`"models":null`,
		`"kustomizations":null`,
		`"wasm_users":null`,
		`"dbt_models":null`,
		`"stale":null`,
		`"gaps":null`,
		`"throwers":[]`,
		`"total":0`,
		`"total_nodes":0,"total_edges":0`,
	} {
		if bytes.Contains([]byte(stripped), []byte(marker)) {
			return "empty"
		}
	}

	trimmed := bytes.TrimSpace([]byte(stripped))
	if bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("{}")) {
		return "empty"
	}
	return "ok"
}

func (c *client) tool(tc benchCase) callRecord {
	rec := callRecord{Label: tc.Label, Category: tc.Category, Tool: tc.Tool}
	start := time.Now()
	resp, err := c.call("tools/call", map[string]any{"name": tc.Tool, "arguments": tc.Args})
	rec.ElapsedMS = time.Since(start).Milliseconds()
	if err != nil {
		rec.Status = "error"
		rec.Error = err.Error()
		return rec
	}
	rec.OutputBytes = len(resp.Result)
	var tr toolCallResult
	if err := json.Unmarshal(resp.Result, &tr); err == nil {
		if len(tr.Content) > 0 {
			s := tr.Content[0].Text
			summary := s
			if len(summary) > 160 {
				summary = summary[:160] + "…"
			}
			rec.Summary = summary
			if tr.IsError {
				rec.Status = "error"
				rec.Error = "tool returned isError=true"
				return rec
			}
			switch classifyResult(s) {
			case "empty":
				rec.Status = "empty"
				return rec
			case "argerror":
				rec.Status = "argerror"
				rec.Error = summary
				return rec
			}
		} else {
			rec.Status = "empty"
			return rec
		}
	}
	rec.Status = "ok"
	return rec
}

// cases returns the curated tool battery. Each case carries a
// category tag so the post-run report can group rows visually.
func cases() []benchCase {
	// Verified seeds (exist in the gortex workspace) — note the
	// "gortex/" repo prefix and the dot-separated method form.
	const (
		knownSym   = "gortex/internal/indexer/indexer.go::Indexer.RepoPrefix"
		knownMeth  = "gortex/internal/indexer/multi.go::MultiIndexer.IndexAll"
		knownSrv   = "gortex/internal/mcp/server.go::NewServer"
		knownType  = "gortex/internal/indexer/indexer.go::Indexer"
		knownFile  = "gortex/cmd/gortex/daemon.go"
		knownFile2 = "gortex/cmd/gortex/server.go"
		repoTag    = "gortex"
	)

	cs := []benchCase{
		// Discovery — no args.
		{Category: "discovery", Label: "graph_stats", Tool: "graph_stats", Args: map[string]any{}},
		{Category: "discovery", Label: "list_repos", Tool: "list_repos", Args: map[string]any{}},
		{Category: "discovery", Label: "list_scopes", Tool: "list_scopes", Args: map[string]any{}},
		{Category: "discovery", Label: "workspace_info", Tool: "workspace_info", Args: map[string]any{}},
		{Category: "discovery", Label: "get_active_project", Tool: "get_active_project", Args: map[string]any{}},
		{Category: "discovery", Label: "index_health", Tool: "index_health", Args: map[string]any{}},
		{Category: "discovery", Label: "tool_profile", Tool: "tool_profile", Args: map[string]any{}},

		// Overview — light args.
		{Category: "overview", Label: "get_repo_outline", Tool: "get_repo_outline", Args: map[string]any{}},
		{Category: "overview", Label: "get_architecture", Tool: "get_architecture", Args: map[string]any{}},
		{Category: "overview", Label: "get_processes", Tool: "get_processes", Args: map[string]any{}},
		{Category: "overview", Label: "gortex_wakeup", Tool: "gortex_wakeup", Args: map[string]any{}},

		// Search.
		{Category: "search", Label: "search_symbols(NewServer)", Tool: "search_symbols", Args: map[string]any{"query": "NewServer", "limit": 10}},
		{Category: "search", Label: "search_symbols(daemon controller)", Tool: "search_symbols", Args: map[string]any{"query": "daemon controller", "limit": 8}},
		{Category: "search", Label: "search_symbols(handler list)", Tool: "search_symbols", Args: map[string]any{"query": "handler list", "limit": 8}},
		{Category: "search", Label: "search_text(buildDaemonStreamable)", Tool: "search_text", Args: map[string]any{"query": "buildDaemonStreamableHandler", "limit": 5}},
		{Category: "search", Label: "search_text(IndexAll)", Tool: "search_text", Args: map[string]any{"query": "IndexAll", "limit": 5}},
		{Category: "search", Label: "search_artifacts(spec)", Tool: "search_artifacts", Args: map[string]any{"query": "spec", "limit": 5}},
		{Category: "search", Label: "search_ast(go-func)", Tool: "search_ast", Args: map[string]any{"pattern": "(function_declaration name: (identifier) @name)", "language": "go", "limit": 5}},
		{Category: "search", Label: "graph_completion_search(NewS)", Tool: "graph_completion_search", Args: map[string]any{"query": "NewS", "limit": 10}},

		// Read-by-id.
		{Category: "read", Label: "get_symbol(NewServer)", Tool: "get_symbol", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "read", Label: "get_symbol_source(NewServer)", Tool: "get_symbol_source", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "read", Label: "get_symbol_history(NewServer)", Tool: "get_symbol_history", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "read", Label: "get_file_summary(daemon.go)", Tool: "get_file_summary", Args: map[string]any{"path": knownFile}},
		{Category: "read", Label: "get_editing_context(server.go)", Tool: "get_editing_context", Args: map[string]any{"path": knownFile2}},
		{Category: "read", Label: "read_file(daemon.go)", Tool: "read_file", Args: map[string]any{"path": knownFile}},
		{Category: "read", Label: "batch_symbols", Tool: "batch_symbols", Args: map[string]any{"ids": knownSrv + "," + knownSym + "," + knownMeth}},

		// Navigation.
		{Category: "nav", Label: "find_usages(Indexer.RepoPrefix)", Tool: "find_usages", Args: map[string]any{"symbol_id": knownSym}},
		{Category: "nav", Label: "find_declaration(NewServer)", Tool: "find_declaration", Args: map[string]any{"use_site": knownSrv, "limit": 5}},
		{Category: "nav", Label: "find_implementations(NewServer)", Tool: "find_implementations", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "find_overrides(NewServer)", Tool: "find_overrides", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "get_callers(MultiIndexer.IndexAll)", Tool: "get_callers", Args: map[string]any{"symbol_id": knownMeth}},
		{Category: "nav", Label: "get_call_chain(MultiIndexer.IndexAll)", Tool: "get_call_chain", Args: map[string]any{"symbol_id": knownMeth, "depth": 2}},
		{Category: "nav", Label: "get_dependencies(NewServer)", Tool: "get_dependencies", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "get_dependents(NewServer)", Tool: "get_dependents", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "get_class_hierarchy(Indexer)", Tool: "get_class_hierarchy", Args: map[string]any{"symbol_id": knownType}},
		{Category: "nav", Label: "get_cluster(NewServer)", Tool: "get_cluster", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "find_import_path(Indexer)", Tool: "find_import_path", Args: map[string]any{"name": "Indexer", "path": "gortex/internal/indexer"}},
		{Category: "nav", Label: "find_clones(MultiIndexer.IndexAll)", Tool: "find_clones", Args: map[string]any{"symbol_id": knownMeth}},
		{Category: "nav", Label: "find_co_changing_symbols(NewServer)", Tool: "find_co_changing_symbols", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "nav", Label: "taint_paths(os.Args→exec)", Tool: "taint_paths", Args: map[string]any{"source_pattern": "os.Args", "sink_pattern": "exec.Command", "limit": 5}},
		{Category: "nav", Label: "flow_between(NewServer→IndexAll)", Tool: "flow_between", Args: map[string]any{"source_id": knownSrv, "sink_id": knownMeth, "max_paths": 3}},
		{Category: "nav", Label: "nav(goto:NewServer)", Tool: "nav", Args: map[string]any{"action": "goto", "id": knownSrv}},
		{Category: "nav", Label: "walk_graph(NewServer)", Tool: "walk_graph", Args: map[string]any{"id": knownSrv, "max_depth": 2}},
		{Category: "nav", Label: "graph_query(kind=type)", Tool: "graph_query", Args: map[string]any{"query": "nodes kind=type", "limit": 10}},

		// Analyze dispatcher.
		{Category: "analyze", Label: "analyze(dead_code)", Tool: "analyze", Args: map[string]any{"kind": "dead_code", "limit": 10}},
		{Category: "analyze", Label: "analyze(hotspots)", Tool: "analyze", Args: map[string]any{"kind": "hotspots", "limit": 10}},
		{Category: "analyze", Label: "analyze(cycles)", Tool: "analyze", Args: map[string]any{"kind": "cycles", "limit": 10}},
		{Category: "analyze", Label: "analyze(todos)", Tool: "analyze", Args: map[string]any{"kind": "todos", "limit": 10}},
		{Category: "analyze", Label: "analyze(pagerank)", Tool: "analyze", Args: map[string]any{"kind": "pagerank", "limit": 10}},
		{Category: "analyze", Label: "analyze(louvain)", Tool: "analyze", Args: map[string]any{"kind": "louvain", "limit": 10}},
		{Category: "analyze", Label: "analyze(wcc)", Tool: "analyze", Args: map[string]any{"kind": "wcc", "limit": 10}},
		{Category: "analyze", Label: "analyze(scc)", Tool: "analyze", Args: map[string]any{"kind": "scc", "limit": 10}},
		{Category: "analyze", Label: "analyze(kcore)", Tool: "analyze", Args: map[string]any{"kind": "kcore", "limit": 10}},
		{Category: "analyze", Label: "analyze(named)", Tool: "analyze", Args: map[string]any{"kind": "named", "limit": 10}},
		{Category: "analyze", Label: "analyze(impact)", Tool: "analyze", Args: map[string]any{"kind": "impact", "limit": 10}},
		{Category: "analyze", Label: "analyze(health_score)", Tool: "analyze", Args: map[string]any{"kind": "health_score", "limit": 10}},
		{Category: "analyze", Label: "analyze(sast)", Tool: "analyze", Args: map[string]any{"kind": "sast", "limit": 10}},
		{Category: "analyze", Label: "analyze(hygiene)", Tool: "analyze", Args: map[string]any{"kind": "hygiene", "limit": 10}},
		{Category: "analyze", Label: "analyze(channel_ops)", Tool: "analyze", Args: map[string]any{"kind": "channel_ops", "limit": 10}},
		{Category: "analyze", Label: "analyze(goroutine_spawns)", Tool: "analyze", Args: map[string]any{"kind": "goroutine_spawns", "limit": 10}},
		{Category: "analyze", Label: "analyze(race_writes)", Tool: "analyze", Args: map[string]any{"kind": "race_writes", "limit": 10}},
		{Category: "analyze", Label: "analyze(unsafe_patterns)", Tool: "analyze", Args: map[string]any{"kind": "unsafe_patterns", "limit": 10}},
		{Category: "analyze", Label: "analyze(error_surface)", Tool: "analyze", Args: map[string]any{"kind": "error_surface", "limit": 10}},
		{Category: "analyze", Label: "analyze(log_events)", Tool: "analyze", Args: map[string]any{"kind": "log_events", "limit": 10}},
		{Category: "analyze", Label: "analyze(connectivity_health)", Tool: "analyze", Args: map[string]any{"kind": "connectivity_health", "limit": 10}},
		{Category: "analyze", Label: "analyze(coverage_summary)", Tool: "analyze", Args: map[string]any{"kind": "coverage_summary", "limit": 10}},
		{Category: "analyze", Label: "analyze(coverage_gaps)", Tool: "analyze", Args: map[string]any{"kind": "coverage_gaps", "limit": 10}},
		// analyze(blame) skipped — runs git blame across every indexed file;
		//                 routinely >540s on ladybug, not bench-safe.
		// analyze(coverage) skipped — requires a `profile` arg pointing at a
		//                   real `go test -cover` output.
		{Category: "analyze", Label: "analyze(stale_code)", Tool: "analyze", Args: map[string]any{"kind": "stale_code", "limit": 10}},
		{Category: "analyze", Label: "analyze(ownership)", Tool: "analyze", Args: map[string]any{"kind": "ownership", "limit": 10}},
		{Category: "analyze", Label: "analyze(stale_flags)", Tool: "analyze", Args: map[string]any{"kind": "stale_flags", "limit": 10}},
		{Category: "analyze", Label: "analyze(releases)", Tool: "analyze", Args: map[string]any{"kind": "releases", "limit": 10}},
		{Category: "analyze", Label: "analyze(cgo_users)", Tool: "analyze", Args: map[string]any{"kind": "cgo_users", "limit": 10}},
		{Category: "analyze", Label: "analyze(wasm_users)", Tool: "analyze", Args: map[string]any{"kind": "wasm_users", "limit": 10}},
		{Category: "analyze", Label: "analyze(orphan_tables)", Tool: "analyze", Args: map[string]any{"kind": "orphan_tables", "limit": 10}},
		{Category: "analyze", Label: "analyze(unreferenced_tables)", Tool: "analyze", Args: map[string]any{"kind": "unreferenced_tables", "limit": 10}},
		{Category: "analyze", Label: "analyze(annotation_users)", Tool: "analyze", Args: map[string]any{"kind": "annotation_users", "limit": 10}},
		{Category: "analyze", Label: "analyze(config_readers)", Tool: "analyze", Args: map[string]any{"kind": "config_readers", "limit": 10}},
		{Category: "analyze", Label: "analyze(event_emitters)", Tool: "analyze", Args: map[string]any{"kind": "event_emitters", "limit": 10}},
		{Category: "analyze", Label: "analyze(tests_as_edges)", Tool: "analyze", Args: map[string]any{"kind": "tests_as_edges", "limit": 10}},
		{Category: "analyze", Label: "analyze(components)", Tool: "analyze", Args: map[string]any{"kind": "components", "limit": 10}},
		{Category: "analyze", Label: "analyze(k8s_resources)", Tool: "analyze", Args: map[string]any{"kind": "k8s_resources", "limit": 10}},
		{Category: "analyze", Label: "analyze(images)", Tool: "analyze", Args: map[string]any{"kind": "images", "limit": 10}},
		{Category: "analyze", Label: "analyze(kustomize)", Tool: "analyze", Args: map[string]any{"kind": "kustomize", "limit": 10}},
		{Category: "analyze", Label: "analyze(string_emitters)", Tool: "analyze", Args: map[string]any{"kind": "string_emitters", "limit": 10}},
		// analyze(sql_rebuild) skipped — it *writes* SQL edges into the graph.
		{Category: "analyze", Label: "analyze(external_calls)", Tool: "analyze", Args: map[string]any{"kind": "external_calls", "limit": 10}},
		{Category: "analyze", Label: "analyze(cross_repo)", Tool: "analyze", Args: map[string]any{"kind": "cross_repo", "limit": 10}},
		{Category: "analyze", Label: "analyze(dbt_models)", Tool: "analyze", Args: map[string]any{"kind": "dbt_models", "limit": 10}},
		{Category: "analyze", Label: "analyze(pubsub)", Tool: "analyze", Args: map[string]any{"kind": "pubsub", "limit": 10}},
		{Category: "analyze", Label: "analyze(models)", Tool: "analyze", Args: map[string]any{"kind": "models", "limit": 10}},
		{Category: "analyze", Label: "analyze(routes)", Tool: "analyze", Args: map[string]any{"kind": "routes", "limit": 10}},

		// Context assembly.
		{Category: "context", Label: "smart_context(daemon http)", Tool: "smart_context", Args: map[string]any{"task": "wire daemon http auth", "limit": 8}},
		{Category: "context", Label: "prefetch_context(daemon)", Tool: "prefetch_context", Args: map[string]any{"limit": 6}},
		{Category: "context", Label: "export_context(daemon)", Tool: "export_context", Args: map[string]any{"task": "daemon http transport wiring", "max_symbols": 8}},
		{Category: "context", Label: "ctx_grep(NewServer)", Tool: "ctx_grep", Args: map[string]any{"pattern": "NewServer"}},
		{Category: "context", Label: "ctx_peek(daemon.go)", Tool: "ctx_peek", Args: map[string]any{"path": knownFile}},
		{Category: "context", Label: "ctx_slice(daemon.go)", Tool: "ctx_slice", Args: map[string]any{"path": knownFile, "start": 1, "end": 30}},
		{Category: "context", Label: "ctx_stats", Tool: "ctx_stats", Args: map[string]any{}},
		{Category: "context", Label: "contracts(NewServer)", Tool: "contracts", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "context", Label: "plan_turn(daemon http)", Tool: "plan_turn", Args: map[string]any{"task": "expose new MCP tool"}},

		// Verify / check.
		{Category: "verify", Label: "verify_change(NewServer)", Tool: "verify_change", Args: map[string]any{"changes": `[{"symbol_id":"` + knownSrv + `","new_signature":"func NewServer(addr string) *Server"}]`}},
		{Category: "verify", Label: "check_guards(NewServer)", Tool: "check_guards", Args: map[string]any{"ids": knownSrv}},
		{Category: "verify", Label: "check_references(NewServer)", Tool: "check_references", Args: map[string]any{"symbol_id": knownSrv}},
		{Category: "verify", Label: "get_test_targets(NewServer)", Tool: "get_test_targets", Args: map[string]any{"ids": knownSrv}},
		{Category: "verify", Label: "get_untested_symbols", Tool: "get_untested_symbols", Args: map[string]any{"limit": 10}},
		{Category: "verify", Label: "detect_changes", Tool: "detect_changes", Args: map[string]any{}},
		{Category: "verify", Label: "get_diagnostics(daemon.go)", Tool: "get_diagnostics", Args: map[string]any{"path": knownFile}},
		{Category: "verify", Label: "verify_citation(daemon.go)", Tool: "verify_citation", Args: map[string]any{"file_path": knownFile, "span": "package main"}},
		{Category: "verify", Label: "diff_context", Tool: "diff_context", Args: map[string]any{}},

		// Suggest / generate.
		{Category: "suggest", Label: "suggest_pattern(NewServer)", Tool: "suggest_pattern", Args: map[string]any{"id": knownSrv}},
		{Category: "suggest", Label: "suggest_queries(daemon)", Tool: "suggest_queries", Args: map[string]any{"hint": "daemon http"}},
		{Category: "suggest", Label: "generate_docs(NewServer)", Tool: "generate_docs", Args: map[string]any{"symbol_id": knownSrv}},

		// Notes & memories.
		{Category: "memory", Label: "save_note(decision)", Tool: "save_note", Args: map[string]any{"body": "all-tools-bench scratch note", "tags": []string{"decision"}}},
		{Category: "memory", Label: "query_notes", Tool: "query_notes", Args: map[string]any{"limit": 5}},
		{Category: "memory", Label: "distill_session", Tool: "distill_session", Args: map[string]any{"limit": 10}},
		{Category: "memory", Label: "store_memory(invariant)", Tool: "store_memory", Args: map[string]any{
			"kind": "invariant", "body": "all-tools-bench scratch memory", "importance": 1,
		}},
		{Category: "memory", Label: "query_memories", Tool: "query_memories", Args: map[string]any{"limit": 5}},
		{Category: "memory", Label: "surface_memories(daemon)", Tool: "surface_memories", Args: map[string]any{"task": "daemon http transport", "limit": 5}},

		// Misc structural.
		{Category: "misc", Label: "get_communities", Tool: "get_communities", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_knowledge_gaps", Tool: "get_knowledge_gaps", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_surprising_connections", Tool: "get_surprising_connections", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_recent_changes", Tool: "get_recent_changes", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_extraction_candidates", Tool: "get_extraction_candidates", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_churn_rate", Tool: "get_churn_rate", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "get_coupling_metrics", Tool: "get_coupling_metrics", Args: map[string]any{"limit": 10}},
		{Category: "misc", Label: "explain_change_impact(NewServer)", Tool: "explain_change_impact", Args: map[string]any{"ids": knownSrv}},
		{Category: "misc", Label: "query_project(" + repoTag + ")", Tool: "query_project", Args: map[string]any{"project": repoTag, "query": "daemon"}},
	}
	return cs
}

func main() {
	addr := flag.String("addr", "http://127.0.0.1:7090", "daemon HTTP base URL")
	token := flag.String("token", "x", "bearer auth token")
	label := flag.String("label", "memory", "tag the run with this backend label")
	jsonOut := flag.String("json", "", "write JSON record to this path")
	flag.Parse()

	c := newClient(*addr, *token)
	if err := c.initialize(); err != nil {
		fmt.Fprintf(os.Stderr, "initialize: %v\n", err)
		os.Exit(2)
	}

	cs := cases()
	total := time.Now()
	out := struct {
		Label   string       `json:"label"`
		Started string       `json:"started"`
		Records []callRecord `json:"records"`
		TotalMS int64        `json:"total_ms"`
	}{Label: *label, Started: time.Now().Format(time.RFC3339)}

	fmt.Printf("== all-tools-bench: %s (target=%s, n=%d) ==\n", *label, *addr, len(cs))
	fmt.Printf("%-12s %-46s %10s %10s %-6s %s\n", "category", "label", "ms", "bytes", "stat", "summary")
	for _, tc := range cs {
		rec := c.tool(tc)
		out.Records = append(out.Records, rec)
		stat := rec.Status
		fmt.Printf("%-12s %-46s %10d %10d %-6s %s\n",
			rec.Category, rec.Label, rec.ElapsedMS, rec.OutputBytes, stat, rec.Summary)
		if rec.Status == "error" {
			fmt.Printf("    ↳ error: %s\n", rec.Error)
		}
	}
	out.TotalMS = time.Since(total).Milliseconds()

	// Category roll-up.
	type catStat struct {
		count, ok, empty, argerr, errs int
		totalMS                        int64
	}
	byCat := map[string]*catStat{}
	for _, r := range out.Records {
		c := byCat[r.Category]
		if c == nil {
			c = &catStat{}
			byCat[r.Category] = c
		}
		c.count++
		c.totalMS += r.ElapsedMS
		switch r.Status {
		case "ok":
			c.ok++
		case "empty":
			c.empty++
		case "argerror":
			c.argerr++
		case "error":
			c.errs++
		}
	}
	cats := make([]string, 0, len(byCat))
	for k := range byCat {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	fmt.Printf("\n-- per-category (%s) --\n", *label)
	fmt.Printf("%-12s %5s %5s %5s %5s %5s %10s\n", "category", "n", "ok", "empty", "argE", "err", "sum_ms")
	for _, k := range cats {
		c := byCat[k]
		fmt.Printf("%-12s %5d %5d %5d %5d %5d %10d\n", k, c.count, c.ok, c.empty, c.argerr, c.errs, c.totalMS)
	}

	okN, emN, aeN, erN := 0, 0, 0, 0
	for _, r := range out.Records {
		switch r.Status {
		case "ok":
			okN++
		case "empty":
			emN++
		case "argerror":
			aeN++
		case "error":
			erN++
		}
	}
	fmt.Printf("\ntotal_wall_ms=%d  ok=%d empty=%d argerror=%d error=%d / %d\n",
		out.TotalMS, okN, emN, aeN, erN, len(out.Records))

	if *jsonOut != "" {
		body, _ := json.MarshalIndent(out, "", "  ")
		if err := os.WriteFile(*jsonOut, body, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s: %v\n", *jsonOut, err)
		}
	}
}
