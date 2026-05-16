package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/persistence"
)

// registerMemoriesTools wires the cross-session development-memory
// triplet:
//
//   - store_memory     — create or update a memory (auto-linked to
//                        symbols mentioned in the body)
//   - query_memories   — list / search memories by symbol / file /
//                        tag / kind / source / author / text
//   - surface_memories — proactively rank memories for a given
//                        working set (anchor symbols / files + task
//                        description); the memory analogue of
//                        smart_context
//
// Memories are workspace-wide and have no session boundary, so
// every result is filtered through the session's workspace scope
// (mirroring notes), but a memory created in session A is visible
// to session B as soon as it lands on disk.
func (s *Server) registerMemoriesTools() {
	s.addTool(
		mcp.NewTool("store_memory",
			mcp.WithDescription("Persist a cross-session development memory: an invariant, gotcha, decision, convention, or reference fact that future agents in this workspace should know. Memories are anchored to symbols and/or files, survive across sessions, and are surfaced automatically when their anchors enter an agent's working set. Pass `id` to update an existing memory."),
			mcp.WithString("body", mcp.Description("Free-form memory text. Symbol IDs (file/path.go::Name) and bare identifier names are auto-linked when they resolve in the graph.")),
			mcp.WithString("title", mcp.Description("Short caption (one-liner) used as the headline in surface_memories output.")),
			mcp.WithString("symbol_ids", mcp.Description("Comma-separated primary symbol anchors (e.g. pkg/foo.go::Bar,pkg/foo.go::Baz). At least one of symbol_ids / file_paths is recommended for high-quality surfacing.")),
			mcp.WithString("file_paths", mcp.Description("Comma-separated primary file anchors.")),
			mcp.WithString("tags", mcp.Description("Comma-separated labels — e.g. 'invariant,gotcha,decision'.")),
			mcp.WithString("kind", mcp.Description("Memory kind: invariant | constraint | convention | gotcha | decision | incident | reference (default: reference).")),
			mcp.WithString("source", mcp.Description("Where this came from: manual | distilled | incident | review (default: manual).")),
			mcp.WithNumber("importance", mcp.Description("1..5 — operator-assigned weight; surfaced higher in ranking (default: 3).")),
			mcp.WithNumber("confidence", mcp.Description("0.0..1.0 — how sure we are this still holds (default: 1.0). Decays the surfacing score.")),
			mcp.WithBoolean("pinned", mcp.Description("Pinned memories are never evicted by the store cap and float to the top of surfacing.")),
			mcp.WithString("supersedes", mcp.Description("Comma-separated memory IDs that this new memory replaces. Each older entry is marked SupersededBy=<new id> and hidden from surface_memories by default.")),
			mcp.WithString("superseded_by", mcp.Description("(Update-only) explicitly mark this memory as superseded by another memory ID — hides it from surface_memories.")),
			mcp.WithString("id", mcp.Description("Existing memory ID — passing it switches the call from create to update.")),
			mcp.WithBoolean("no_autolink", mcp.Description("Skip the body→symbol auto-linker.")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx, or toon")),
		),
		s.handleStoreMemory,
	)

	s.addTool(
		mcp.NewTool("query_memories",
			mcp.WithDescription("Search the cross-session memory store by symbol, file, tag, kind, source, author, free-text, or recency. Returns matches sorted pinned-first, then importance-DESC, then UpdatedAt-DESC. Unlike query_notes, memories are workspace-wide and have no session boundary."),
			mcp.WithString("symbol_id", mcp.Description("Return memories anchored or auto-linked to this symbol.")),
			mcp.WithString("file_path", mcp.Description("Return memories anchored to this file path.")),
			mcp.WithString("tag", mcp.Description("Return memories carrying this tag (case-insensitive).")),
			mcp.WithString("kind", mcp.Description("Filter by memory kind: invariant | constraint | convention | gotcha | decision | incident | reference.")),
			mcp.WithString("source", mcp.Description("Filter by memory source: manual | distilled | incident | review.")),
			mcp.WithString("author", mcp.Description("Filter by author agent (MCP clientInfo.name).")),
			mcp.WithString("text", mcp.Description("Case-insensitive substring filter on body or title.")),
			mcp.WithString("since", mcp.Description("Only return memories updated at or after this RFC-3339 timestamp.")),
			mcp.WithNumber("min_importance", mcp.Description("Only return memories with importance >= this value (1..5).")),
			mcp.WithBoolean("pinned_only", mcp.Description("Return only pinned memories.")),
			mcp.WithBoolean("include_superseded", mcp.Description("Include superseded memories (default false).")),
			mcp.WithNumber("limit", mcp.Description("Cap the result set (default: 50).")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx, or toon")),
		),
		s.handleQueryMemories,
	)

	s.addTool(
		mcp.NewTool("surface_memories",
			mcp.WithDescription("Proactively surface relevant development memories for a task or working set. Given anchor symbols / files / a task description, returns memories ranked by symbol overlap, file overlap, keyword hits, importance, pinning, recency, and confidence — deterministic scoring, no LLM. Use at task start (the memory analogue of smart_context) or before editing a symbol with a known history."),
			mcp.WithString("task", mcp.Description("Natural-language task description — keywords matched against memory body / title.")),
			mcp.WithString("symbol_ids", mcp.Description("Comma-separated anchor symbols (the working set).")),
			mcp.WithString("file_paths", mcp.Description("Comma-separated anchor files.")),
			mcp.WithNumber("limit", mcp.Description("Cap the surfaced set (default: 10).")),
			mcp.WithNumber("excerpt_chars", mcp.Description("Body excerpt cap in bytes (default: 320).")),
			mcp.WithNumber("min_score", mcp.Description("Drop hits below this score (default: 0).")),
			mcp.WithBoolean("include_superseded", mcp.Description("Include superseded memories (default false).")),
			mcp.WithBoolean("mark_accessed", mcp.Description("Increment AccessCount + LastAccessed on returned memories (default true).")),
			mcp.WithString("format", mcp.Description("Output format: json (default), gcx, or toon")),
		),
		s.handleSurfaceMemories,
	)
}

// handleStoreMemory — create-or-update entry point. Update mode is
// selected by passing an existing `id`; everything else is a create.
func (s *Server) handleStoreMemory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.memories == nil {
		return mcp.NewToolResultError("memories storage not initialised"), nil
	}

	id := strings.TrimSpace(req.GetString("id", ""))
	body := req.GetString("body", "")
	title := strings.TrimSpace(req.GetString("title", ""))
	symbolIDs := splitCSV(req.GetString("symbol_ids", ""))
	filePaths := splitCSV(req.GetString("file_paths", ""))
	tags := splitCSV(req.GetString("tags", ""))
	kind := strings.TrimSpace(req.GetString("kind", ""))
	source := strings.TrimSpace(req.GetString("source", ""))
	importance := req.GetInt("importance", 0)
	confidence := float32(req.GetFloat("confidence", 0))
	pinned := req.GetBool("pinned", false)
	pinnedSet := requestHasArg(req, "pinned")
	supersedes := splitCSV(req.GetString("supersedes", ""))
	supersededBy := strings.TrimSpace(req.GetString("superseded_by", ""))
	noAutolink := req.GetBool("no_autolink", false)

	if id == "" && body == "" && len(symbolIDs) == 0 && len(filePaths) == 0 {
		return mcp.NewToolResultError("store_memory requires at least one of: body, symbol_ids, file_paths"), nil
	}

	// Update path.
	if id != "" {
		patch := MemoryPatch{}
		if body != "" {
			patch.Body = &body
		}
		if title != "" {
			patch.Title = &title
		}
		if kind != "" {
			patch.Kind = &kind
		}
		if source != "" {
			patch.Source = &source
		}
		if importance != 0 {
			patch.Importance = &importance
		}
		if confidence > 0 {
			patch.Confidence = &confidence
		}
		if tags != nil {
			patch.Tags = tags
		}
		if symbolIDs != nil {
			patch.SymbolIDs = symbolIDs
		}
		if filePaths != nil {
			patch.FilePaths = filePaths
		}
		if pinnedSet {
			patch.Pinned = &pinned
		}
		if supersededBy != "" {
			patch.SupersededBy = &supersededBy
		}
		if !noAutolink && body != "" {
			patch.AddLinks = autoLinkBody(body, s.graph, sessionWorkspaceIDOrEmpty(s, ctx), defaultAutoLinkOptions())
		}
		updated, err := s.memories.Update(id, patch)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("update memory: %v", err)), nil
		}
		// Backfill: when the update names `supersedes`, retro-mark
		// the older entries even on an update call.
		for _, oldID := range supersedes {
			if oldID == id {
				continue
			}
			if _, ok := s.memories.Get(oldID); !ok {
				continue
			}
			newID := id
			_, _ = s.memories.Update(oldID, MemoryPatch{SupersededBy: &newID})
		}
		return s.respondJSONOrTOON(ctx, req, memoryEntryToWire(updated))
	}

	// Create path. Stamp scope from session + (when available) the
	// first attached symbol's node, so memories inherit the workspace
	// boundary without forcing the caller to think about it.
	workspaceID, projectID, _ := s.sessionScope(ctx)
	repoPrefix, _ := s.sessionLocality(ctx)

	if len(symbolIDs) > 0 && s.graph != nil {
		if node := s.graph.GetNode(symbolIDs[0]); node != nil {
			if workspaceID == "" {
				workspaceID = node.WorkspaceID
			}
			if projectID == "" {
				projectID = node.ProjectID
			}
			if repoPrefix == "" {
				repoPrefix = node.RepoPrefix
			}
		}
	}

	var autoLinks []string
	if !noAutolink && body != "" {
		autoLinks = autoLinkBody(body, s.graph, workspaceID, defaultAutoLinkOptions())
	}

	entry := persistence.MemoryEntry{
		Body:        body,
		Title:       title,
		Kind:        kind,
		Source:      source,
		Importance:  importance,
		Confidence:  confidence,
		AuthorAgent: sessionClientName(s, ctx),
		SymbolIDs:   symbolIDs,
		FilePaths:   filePaths,
		AutoLinks:   autoLinks,
		Tags:        tags,
		WorkspaceID: workspaceID,
		ProjectID:   projectID,
		RepoPrefix:  repoPrefix,
		Pinned:      pinned,
	}

	newID, err := s.memories.Save(entry)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("save memory: %v", err)), nil
	}

	// Stamp each named older memory as superseded by this new one.
	for _, oldID := range supersedes {
		if oldID == newID {
			continue
		}
		if _, ok := s.memories.Get(oldID); !ok {
			continue
		}
		nid := newID
		_, _ = s.memories.Update(oldID, MemoryPatch{SupersededBy: &nid})
	}

	saved, _ := s.memories.Get(newID)
	return s.respondJSONOrTOON(ctx, req, memoryEntryToWire(saved))
}

// handleQueryMemories — multi-filter listing. Honours the session
// workspace boundary: every result lives inside the session's
// workspace (or carries no workspace when the session is unbound).
func (s *Server) handleQueryMemories(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.memories == nil {
		return mcp.NewToolResultError("memories storage not initialised"), nil
	}

	limit := req.GetInt("limit", 50)
	filter := MemoryQueryFilter{
		SymbolID:          strings.TrimSpace(req.GetString("symbol_id", "")),
		FilePath:          strings.TrimSpace(req.GetString("file_path", "")),
		Tag:               strings.TrimSpace(req.GetString("tag", "")),
		Kind:              strings.TrimSpace(req.GetString("kind", "")),
		Source:            strings.TrimSpace(req.GetString("source", "")),
		AuthorAgent:       strings.TrimSpace(req.GetString("author", "")),
		TextSearch:        req.GetString("text", ""),
		MinImportance:     req.GetInt("min_importance", 0),
		IncludeSuperseded: req.GetBool("include_superseded", false),
		Limit:             limit,
	}

	if since := strings.TrimSpace(req.GetString("since", "")); since != "" {
		if ts, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = ts
		} else {
			return mcp.NewToolResultError(fmt.Sprintf("invalid `since` timestamp: %v", err)), nil
		}
	}
	if req.GetBool("pinned_only", false) {
		yes := true
		filter.Pinned = &yes
	}
	if workspaceID, _, bound := s.sessionScope(ctx); bound {
		filter.WorkspaceID = workspaceID
	}

	memories := s.memories.Query(filter)
	wire := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		wire = append(wire, memoryEntryToWire(m))
	}
	return s.respondJSONOrTOON(ctx, req, map[string]any{
		"memories": wire,
		"total":    len(wire),
	})
}

// handleSurfaceMemories — proactively retrieve memories for a
// working set. Defaults to mark_accessed=true so the access stats
// stay accurate.
func (s *Server) handleSurfaceMemories(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.memories == nil {
		return mcp.NewToolResultError("memories storage not initialised"), nil
	}

	opts := SurfaceOptions{
		Task:              strings.TrimSpace(req.GetString("task", "")),
		SymbolIDs:         splitCSV(req.GetString("symbol_ids", "")),
		FilePaths:         splitCSV(req.GetString("file_paths", "")),
		Limit:             req.GetInt("limit", 10),
		ExcerptCap:        req.GetInt("excerpt_chars", 320),
		MinScore:          float32(req.GetFloat("min_score", 0)),
		IncludeSuperseded: req.GetBool("include_superseded", false),
		MarkAccessed:      requestBoolDefault(req, "mark_accessed", true),
	}
	if workspaceID, projectID, bound := s.sessionScope(ctx); bound {
		opts.WorkspaceID = workspaceID
		opts.ProjectID = projectID
	}

	res := s.memories.Surface(opts, func(id string) *graph.Node {
		if s.graph == nil {
			return nil
		}
		return s.graph.GetNode(id)
	})
	return s.respondJSONOrTOON(ctx, req, res)
}

// memoryEntryToWire shapes a stored memory for inclusion in
// JSON / GCX / TOON responses.
func memoryEntryToWire(e persistence.MemoryEntry) map[string]any {
	m := map[string]any{
		"id":         e.ID,
		"timestamp":  e.Timestamp.UTC().Format(time.RFC3339),
		"updated_at": e.UpdatedAt.UTC().Format(time.RFC3339),
		"body":       e.Body,
	}
	if e.Title != "" {
		m["title"] = e.Title
	}
	if e.Kind != "" {
		m["kind"] = e.Kind
	}
	if e.Source != "" {
		m["source"] = e.Source
	}
	if e.Confidence > 0 {
		m["confidence"] = e.Confidence
	}
	if e.Importance > 0 {
		m["importance"] = e.Importance
	}
	if e.AuthorAgent != "" {
		m["author_agent"] = e.AuthorAgent
	}
	if len(e.SymbolIDs) > 0 {
		m["symbol_ids"] = e.SymbolIDs
	}
	if len(e.FilePaths) > 0 {
		m["file_paths"] = e.FilePaths
	}
	if len(e.AutoLinks) > 0 {
		m["links"] = e.AutoLinks
	}
	if len(e.Tags) > 0 {
		m["tags"] = e.Tags
	}
	if e.WorkspaceID != "" {
		m["workspace_id"] = e.WorkspaceID
	}
	if e.ProjectID != "" {
		m["project_id"] = e.ProjectID
	}
	if e.RepoPrefix != "" {
		m["repo_prefix"] = e.RepoPrefix
	}
	if e.Pinned {
		m["pinned"] = true
	}
	if e.SupersededBy != "" {
		m["superseded_by"] = e.SupersededBy
	}
	if e.AccessCount > 0 {
		m["access_count"] = e.AccessCount
	}
	if !e.LastAccessed.IsZero() {
		m["last_accessed"] = e.LastAccessed.UTC().Format(time.RFC3339)
	}
	return m
}

// requestHasArg reports whether the caller explicitly supplied a
// named argument. Needed for tri-state booleans (pinned: set vs.
// unset vs. false) on update.
func requestHasArg(req mcp.CallToolRequest, key string) bool {
	args, ok := req.Params.Arguments.(map[string]any)
	if !ok {
		return false
	}
	_, present := args[key]
	return present
}

// requestBoolDefault returns the bool at key, or def when the
// caller didn't supply the argument at all. (GetBool returns the
// zero value for "not present", which conflates absent with false.)
func requestBoolDefault(req mcp.CallToolRequest, key string, def bool) bool {
	if !requestHasArg(req, key) {
		return def
	}
	return req.GetBool(key, def)
}
