package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/zzet/gortex/internal/telemetry"
)

var facadeDescriptions = map[string]string{
	"explore":         "Localize a task and return the most relevant code neighborhood.",
	"search":          "Search symbols, text, files, AST, or artifacts. Choose with operation.",
	"read":            "Read a file, symbol, symbol batch, summary, history, or editing context.",
	"relations":       "Query usages, callers, dependencies, implementations, or other symbol relations.",
	"trace":           "Trace call chains, graph paths, data flow, taint, CFG, or an expert graph query.",
	"analyze":         "Run deterministic graph analysis selected by kind.",
	"ask":             "Ask the configured research agent an open-ended codebase question.",
	"change":          "Assess a proposed or existing change without modifying source.",
	"edit":            "Apply a guarded source or file mutation. This tool writes to disk.",
	"refactor":        "Apply a semantic refactor such as rename, move, inline, delete, or code action.",
	"review":          "Build or critique a read-only code review selected by operation.",
	"publish_review":  "Publish a review to an external forge. This tool has external side effects.",
	"pr":              "Inspect pull requests, risk, impact, reviewers, triage, or conflicts.",
	"recall":          "Read session notes, durable memories, or repository notebooks.",
	"remember":        "Persist notes, memories, notebook entries, or review suppressions.",
	"workspace":       "Inspect repository, project, scope, index, graph, or proxy state.",
	"workspace_admin": "Change repository, project, index, scope, workflow, or daemon control state.",
	"session":         "Change volatile agent, planning, workflow, or proxy state for this session.",
	"overlay":         "Change only the current session's speculative overlay state.",
	"response":        "Inspect, slice, grep, or export a buffered Gortex response.",
	"capabilities":    "List facade operations or return the exact legacy schema behind one operation.",
}

func boolPointer(v bool) *bool { return &v }

func facadeAnnotation(name string) mcpgo.ToolAnnotation {
	readOnly := true
	destructive := false
	openWorld := false
	switch name {
	case "ask", "pr", "review":
		openWorld = true
	case "edit", "refactor", "remember", "workspace_admin":
		readOnly = false
		destructive = true
		if name == "workspace_admin" {
			openWorld = true
		}
	case "overlay", "session":
		readOnly = false
	case "publish_review":
		readOnly = false
		destructive = true
		openWorld = true
	}
	return mcpgo.ToolAnnotation{
		ReadOnlyHint:    boolPointer(readOnly),
		DestructiveHint: boolPointer(destructive),
		OpenWorldHint:   boolPointer(openWorld),
	}
}

func facadeTargetProperty() mcpgo.PropertyOption {
	return mcpgo.Properties(map[string]any{
		"file":     map[string]any{"type": "string"},
		"symbol":   map[string]any{"type": "string"},
		"symbols":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"query":    map[string]any{"type": "string"},
		"artifact": map[string]any{"type": "string"},
		"repo":     map[string]any{"type": "string"},
	})
}

func facadeOutputProperty() mcpgo.PropertyOption {
	return mcpgo.Properties(map[string]any{
		"format":    map[string]any{"type": "string"},
		"max_bytes": map[string]any{"type": "integer"},
		"limit":     map[string]any{"type": "integer"},
		"cursor":    map[string]any{"type": "string"},
		"fields":    map[string]any{"type": "string"},
	})
}

func facadeToolDefinition(name string) mcpgo.Tool {
	desc := facadeDescriptions[name]
	annotation := mcpgo.WithToolAnnotation(facadeAnnotation(name))
	freeObject := func(field, description string) mcpgo.ToolOption {
		return mcpgo.WithObject(field, mcpgo.Description(description), mcpgo.AdditionalProperties(true))
	}
	operation := mcpgo.WithString("operation", mcpgo.Description("Operation; see capabilities."))
	options := freeObject("options", "Operation-specific options validated by Gortex.")
	output := mcpgo.WithObject("output", mcpgo.Description("Response shaping."), facadeOutputProperty(), mcpgo.AdditionalProperties(false))
	target := mcpgo.WithObject("target", mcpgo.Description("Exactly one primary target selector."), facadeTargetProperty(), mcpgo.AdditionalProperties(false))

	var opts []mcpgo.ToolOption
	switch name {
	case "explore":
		opts = []mcpgo.ToolOption{
			operation,
			mcpgo.WithString("task", mcpgo.Description("Task, bug, or question to localize.")),
			mcpgo.WithString("path"), options, output,
		}
	case "search":
		opts = []mcpgo.ToolOption{operation, mcpgo.WithString("query"), options, output}
	case "read":
		opts = []mcpgo.ToolOption{operation, target, freeObject("context", "Read window or source-context controls."), options, output}
	case "relations", "trace":
		opts = []mcpgo.ToolOption{operation, freeObject("target", "Primary file or symbol target."), freeObject("to", "Optional destination target."), options, output}
	case "analyze":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("kind", mcpgo.Required(), mcpgo.Description("Analysis kind or facade operation.")),
			freeObject("target", "Optional analysis target."), options, output,
		}
	case "ask":
		opts = []mcpgo.ToolOption{mcpgo.WithString("question", mcpgo.Required()), options, output}
	case "change", "review":
		opts = []mcpgo.ToolOption{operation, freeObject("source", "Diff, working tree, ranges, symbols, or review source."), options, output}
	case "edit":
		opts = []mcpgo.ToolOption{
			operation, target, mcpgo.WithString("match"), mcpgo.WithString("replacement"),
			mcpgo.WithString("content"), freeObject("guard", "Stale-write and occurrence guards."),
			mcpgo.WithArray("changes", mcpgo.Description("Batch file or symbol edits."), mcpgo.Items(map[string]any{"type": "object", "additionalProperties": true})),
			mcpgo.WithBoolean("dry_run"), options, output,
		}
	case "refactor":
		opts = []mcpgo.ToolOption{
			operation, target, mcpgo.WithString("new_name"), mcpgo.WithString("destination"),
			mcpgo.WithBoolean("dry_run"), options, output,
		}
	case "publish_review", "pr", "recall", "remember", "workspace", "workspace_admin", "overlay", "response":
		// Cold domain facades keep only the stable discriminator plus a
		// runtime-validated payload. capabilities returns the exact operation
		// schema on demand without changing tools/list.
		opts = []mcpgo.ToolOption{operation, freeObject("arguments", "Operation arguments.")}
	case "session":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("operation", mcpgo.Description("Session operation; see capabilities. Use subscribe or unsubscribe with channel.")),
			mcpgo.WithString("channel", mcpgo.Description("daemon_health, diagnostics, graph_invalidated, stale_refs, or workspace_readiness")),
			freeObject("arguments", "Optional session arguments."),
		}
	case "capabilities":
		opts = []mcpgo.ToolOption{
			mcpgo.WithString("domain", mcpgo.Description("Facade name; omit to list all facades.")),
			mcpgo.WithString("operation", mcpgo.Description("Operation name; omit to list the domain.")),
			mcpgo.WithString("detail", mcpgo.Description("summary or schema")),
		}
	default:
		opts = []mcpgo.ToolOption{operation, target, options, output}
	}
	opts = append([]mcpgo.ToolOption{mcpgo.WithDescription(desc), annotation}, opts...)
	return mcpgo.NewTool(name, opts...)
}

// registerFacadeTools installs every facade name directly into the live MCP
// server. Session filtering keeps them out of legacy surfaces, while a
// facade-v1 session receives all names from its first tools/list and never
// depends on deferred promotion or tools/list_changed.
func (s *Server) registerFacadeTools() {
	for _, name := range facadeToolNames() {
		if _, alreadyLegacy := s.facades.legacy(name); alreadyLegacy {
			continue // explore/analyze/review (and ask when configured)
		}
		facade := name
		tool := facadeToolDefinition(facade)
		var handler server.ToolHandlerFunc
		if facade == "capabilities" {
			handler = s.handleCapabilities
		} else {
			handler = func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
				return s.handleFacade(ctx, facade, req)
			}
		}
		// Deliberately bypass addTool/lazy routing. The per-session surface
		// filter hides these from legacy clients; facade clients need every
		// dispatcher callable immediately.
		scrubToolText(&tool)
		s.mcpServer.AddTool(tool, s.wrapControlToolHandler(handler))
	}
}

func (s *Server) wrapLegacyFacade(name string, raw server.ToolHandlerFunc) server.ToolHandlerFunc {
	if !isFacadeToolName(name) {
		return raw
	}
	return func(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		args := req.GetArguments()
		_, explicitOperation := args["operation"]
		facadeSession := s.effectiveSessionPolicy(ctx).preset == FacadeSurfaceVersion
		if !facadeSession && !explicitOperation {
			return raw(ctx, req)
		}
		if name == "analyze" {
			kind := normalizeFacadeOperation(req.GetString("kind", ""))
			if facadeSession && kind == "temporal_verify" {
				return NewStructuredErrorResult(StructuredError{
					ErrorCode: ErrCodeToolBlockedByMode,
					Message:   "analyze(kind=temporal_verify) persists verification state; use workspace_admin(operation=temporal_verify)",
					Data:      map[string]any{"facade": "workspace_admin", "operation": "temporal_verify"},
				}), nil
			}
			if _, ok := s.facades.operation("analyze", kind); !ok {
				// Native analyze kinds remain behind its graph dispatcher.
				return s.invokeFacadeSpec(ctx, req, facadeOperationSpec{
					Facade: "analyze", Operation: "graph", Legacy: "analyze", Effect: facadeEffectRead,
				})
			}
		}
		return s.handleFacade(ctx, name, req)
	}
}

func (s *Server) handleFacade(ctx context.Context, facade string, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	started := time.Now()
	operation := normalizeFacadeOperation(req.GetString("operation", ""))
	if facade == "analyze" {
		operation = normalizeFacadeOperation(req.GetString("kind", "graph"))
		if _, ok := s.facades.operation(facade, operation); !ok {
			operation = "graph"
		}
	}
	if facade == "session" && (operation == "subscribe" || operation == "unsubscribe") {
		channel := normalizeFacadeOperation(req.GetString("channel", ""))
		operation += "_" + channel
	}
	if operation == "" {
		operation = defaultFacadeOperation(facade)
	}
	spec, ok := s.facades.operation(facade, operation)
	if !ok {
		valid := make([]string, 0)
		for _, candidate := range s.facades.operations(facade) {
			valid = append(valid, candidate.Operation)
		}
		result := NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf("unknown %s operation %q", facade, operation),
			Data:      map[string]any{"facade": facade, "operation": operation, "valid_operations": valid},
		})
		// Never put the caller-provided operation in telemetry. All unresolved
		// values collapse to the fixed sentinel "unknown".
		s.recordFacadeTelemetry(facade, "unknown", facadeOutcomeInvalidOperation, time.Since(started))
		return result, nil
	}
	return s.invokeFacadeSpec(ctx, req, spec)
}

func defaultFacadeOperation(facade string) string {
	switch facade {
	case "explore":
		return "task"
	case "search":
		return "symbols"
	case "read":
		return "source"
	case "relations":
		return "usages"
	case "trace":
		return "call_chain"
	case "analyze":
		return "graph"
	case "ask":
		return "research"
	case "change":
		return "contract"
	case "edit":
		return "file"
	case "refactor":
		return "rename"
	case "review":
		return "run"
	case "publish_review":
		return "post"
	case "pr":
		return "list"
	case "recall":
		return "surface"
	case "remember":
		return "memory"
	case "workspace":
		return "info"
	case "response":
		return "stats"
	default:
		return ""
	}
}

func (s *Server) invokeFacadeSpec(ctx context.Context, req mcpgo.CallToolRequest, spec facadeOperationSpec) (result *mcpgo.CallToolResult, err error) {
	started := time.Now()
	outcome := ""
	defer func() {
		if outcome == "" {
			outcome = classifyFacadeOutcome(result, err)
		}
		s.recordFacadeTelemetry(spec.Facade, spec.Operation, outcome, time.Since(started))
	}()
	legacy, ok := s.facades.legacy(spec.Legacy)
	if !ok {
		outcome = facadeOutcomeUnavailable
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument,
			Message:   fmt.Sprintf("%s.%s is unavailable in this server configuration", spec.Facade, spec.Operation),
			Data:      map[string]any{"facade": spec.Facade, "operation": spec.Operation, "legacy_tool": spec.Legacy},
		}), nil
	}
	if invalid := validateFacadeInput(spec, req.GetArguments()); invalid != nil {
		outcome = facadeOutcomeInvalidArgument
		return invalid, nil
	}
	normalized := normalizeFacadeArguments(spec, req.GetArguments())
	if spec.Facade == "analyze" && spec.Operation == "graph" &&
		normalizeFacadeOperation(fmt.Sprint(normalized["kind"])) == "temporal_verify" {
		outcome = facadeOutcomeBlocked
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeToolBlockedByMode,
			Message:   "analyze(kind=temporal_verify) persists verification state; use workspace_admin(operation=temporal_verify)",
			Data:      map[string]any{"facade": "workspace_admin", "operation": "temporal_verify"},
		}), nil
	}
	if OverlayViewFromContext(ctx) == nil && !facadeLegacyManagesOwnOverlay(spec.Legacy) {
		view, viewErr := s.buildOverlayViewForCtx(ctx)
		if viewErr != nil {
			outcome = facadeOutcomeToolError
			return mcpgo.NewToolResultError(viewErr.Error()), nil
		}
		if view != nil {
			ctx = WithOverlayView(ctx, view)
		}
	}
	forwarded := req
	forwarded.Params.Name = spec.Legacy
	forwarded.Params.Arguments = normalized
	forwarded.Params.RawArguments = nil
	result, err = legacy.handler(ctx, forwarded)
	if err == nil {
		result = s.decorateFacadeFreshness(spec.Legacy, forwarded, result)
	}
	if result != nil {
		if result.Meta == nil {
			result.Meta = &mcpgo.Meta{}
		}
		if result.Meta.AdditionalFields == nil {
			result.Meta.AdditionalFields = make(map[string]any)
		}
		result.Meta.AdditionalFields["gortex_facade"] = map[string]any{
			"surface_version": FacadeSurfaceVersion,
			"facade":          spec.Facade,
			"operation":       spec.Operation,
			"canonical_tool":  spec.Legacy,
		}
	}
	return result, err
}

// decorateFacadeFreshness runs the existing legacy freshness policy after a
// facade operation has resolved to its canonical tool and normalized request.
// The outer facade middleware only sees compact names/targets (read,
// relations, target.file, ...), so applying the policy there would miss the
// legacy path/id fields the rider is deliberately keyed to.
func (s *Server) decorateFacadeFreshness(legacy string, req mcpgo.CallToolRequest, result *mcpgo.CallToolResult) *mcpgo.CallToolResult {
	if rider := s.freshnessRiderFor(legacy, req); rider != nil {
		return decorateResultWithFreshness(result, rider)
	}
	if isFreshnessListTool(legacy) {
		return s.decorateListResultWithFreshness(result)
	}
	return result
}

func facadeLegacyManagesOwnOverlay(name string) bool {
	if strings.HasPrefix(name, "overlay_") || strings.HasPrefix(name, "subscribe_") ||
		strings.HasPrefix(name, "unsubscribe_") || strings.HasPrefix(name, "proxy_") {
		return true
	}
	switch name {
	case "preview_edit", "simulate_chain", "compare_with_overlay", "compare_branches", "agent_registry", "set_planning_mode", "workflow":
		return true
	default:
		return false
	}
}

func validateFacadeInput(spec facadeOperationSpec, input map[string]any) *mcpgo.CallToolResult {
	for _, field := range []string{"arguments", "options", "source", "context", "guard", "output"} {
		value, present := input[field]
		if !present || value == nil {
			continue
		}
		if _, ok := value.(map[string]any); !ok {
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument,
				Message:   fmt.Sprintf("%s must be an object", field),
				Data:      map[string]any{"field": field},
			})
		}
	}
	for _, field := range []string{"target", "to"} {
		if raw, present := input[field]; present && raw != nil {
			if invalid := validateFacadeSelector(field, raw); invalid != nil {
				return invalid
			}
		}
	}
	if spec.Facade == "search" {
		switch spec.Operation {
		case "symbols", "text", "completion":
			query := strings.TrimSpace(fmt.Sprint(input["query"]))
			if query == "" || query == "<nil>" {
				return NewStructuredErrorResult(StructuredError{
					ErrorCode: ErrCodeInvalidArgument,
					Message:   fmt.Sprintf("search.%s requires query", spec.Operation),
					Data:      map[string]any{"field": "query", "operation": spec.Operation},
				})
			}
		}
	}
	task, _ := input["task"].(string)
	if spec.Facade == "explore" && spec.Operation == "task" && strings.TrimSpace(task) == "" {
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: "explore.task requires task",
			Data: map[string]any{"field": "task", "operation": spec.Operation},
		})
	}
	return nil
}

func validateFacadeSelector(field string, raw any) *mcpgo.CallToolResult {
	target, ok := raw.(map[string]any)
	if !ok {
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: field + " must be an object",
			Data: map[string]any{"field": field},
		})
	}
	allowed := map[string]bool{"file": true, "symbol": true, "symbols": true, "query": true, "artifact": true, "repo": true}
	selectors := make([]string, 0, len(target))
	for key, value := range target {
		if !allowed[key] {
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown %s selector %q", field, key),
				Data: map[string]any{"field": field, "valid_selectors": []string{"file", "symbol", "symbols", "query", "artifact", "repo"}},
			})
		}
		if facadeSelectorPresent(value) {
			selectors = append(selectors, key)
		}
	}
	if len(selectors) != 1 {
		sort.Strings(selectors)
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: field + " must contain exactly one selector",
			Data: map[string]any{"field": field, "selectors": selectors},
		})
	}
	return nil
}

func facadeSelectorPresent(value any) bool {
	if value == nil {
		return false
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed) != ""
	case []any:
		return len(typed) > 0
	case []string:
		return len(typed) > 0
	default:
		return fmt.Sprint(value) != ""
	}
}

const (
	facadeOutcomeSuccess          = "success"
	facadeOutcomeInvalidOperation = "invalid_operation"
	facadeOutcomeInvalidArgument  = "invalid_argument"
	facadeOutcomeBlocked          = "blocked"
	facadeOutcomeUnavailable      = "unavailable"
	facadeOutcomeToolError        = "tool_error"
	facadeOutcomeHandlerError     = "handler_error"
	facadeOutcomeEmptyResult      = "empty_result"
)

func facadeTelemetryDimension(spec facadeOperationSpec) string {
	return boundedFacadeTelemetryDimension(spec.Facade, spec.Operation)
}

// boundedFacadeTelemetryDimension joins fixed, low-cardinality tokens and
// deterministically folds long combinations under telemetry's 32-byte guard.
// Callers must pass registry values or fixed sentinels, never request values.
func boundedFacadeTelemetryDimension(parts ...string) string {
	dim := strings.Join(parts, ".")
	if len(dim) <= 32 {
		return dim
	}
	sum := sha256.Sum256([]byte(dim))
	return dim[:23] + "." + hex.EncodeToString(sum[:4])
}

func classifyFacadeOutcome(result *mcpgo.CallToolResult, err error) string {
	if err != nil {
		return facadeOutcomeHandlerError
	}
	if result == nil {
		return facadeOutcomeEmptyResult
	}
	if !result.IsError {
		return facadeOutcomeSuccess
	}
	body, ok := singleTextContent(result)
	if !ok {
		return facadeOutcomeToolError
	}
	var structured struct {
		ErrorCode ErrorCode `json:"error_code"`
	}
	if json.Unmarshal([]byte(body), &structured) != nil {
		return facadeOutcomeToolError
	}
	switch structured.ErrorCode {
	case ErrCodeInvalidArgument:
		return facadeOutcomeInvalidArgument
	case ErrCodeToolBlockedByMode, ErrCodeToolOutOfPhase:
		return facadeOutcomeBlocked
	case ErrCodeWorkspaceUnknown, ErrCodeProjectUnknown, ErrCodeRepoNotTracked, ErrCodeRouteUnresolved:
		return facadeOutcomeUnavailable
	default:
		return facadeOutcomeToolError
	}
}

func validFacadeOutcome(outcome string) string {
	switch outcome {
	case facadeOutcomeSuccess, facadeOutcomeInvalidOperation, facadeOutcomeInvalidArgument,
		facadeOutcomeBlocked, facadeOutcomeUnavailable, facadeOutcomeToolError,
		facadeOutcomeHandlerError, facadeOutcomeEmptyResult:
		return outcome
	default:
		return facadeOutcomeToolError
	}
}

// facadeTelemetryIdentity admits only registry-backed operations and four
// fixed capabilities buckets. This is the privacy boundary that prevents a
// caller-provided operation/domain from becoming even a hashed dimension.
func (s *Server) facadeTelemetryIdentity(facade, operation string) (string, string) {
	if !isFacadeToolName(facade) {
		return "unknown", "unknown"
	}
	if facade == "capabilities" {
		switch operation {
		case "list", "domain", "operation", "unknown":
			return facade, operation
		default:
			return facade, "unknown"
		}
	}
	if operation == "unknown" {
		return facade, operation
	}
	if _, ok := s.facades.operation(facade, operation); !ok {
		return facade, "unknown"
	}
	return facade, operation
}

func (s *Server) recordFacadeTelemetry(facade, operation, outcome string, elapsed time.Duration) {
	facade, operation = s.facadeTelemetryIdentity(facade, operation)
	outcome = validFacadeOutcome(outcome)
	status := "error"
	if outcome == facadeOutcomeSuccess {
		status = "ok"
	}
	s.recorder.Record("mcp_facade_call", boundedFacadeTelemetryDimension(facade, operation))
	s.recorder.Record("mcp_facade_status", boundedFacadeTelemetryDimension(facade, operation, status))
	s.recorder.Record("mcp_facade_outcome", boundedFacadeTelemetryDimension(facade, operation, outcome))
	s.recorder.Record("mcp_facade_latency", boundedFacadeTelemetryDimension(facade, operation, telemetry.BucketDuration(elapsed)))
	if outcome == facadeOutcomeInvalidOperation || outcome == facadeOutcomeInvalidArgument {
		s.recorder.Record("mcp_facade_invalid", boundedFacadeTelemetryDimension(facade, operation, string(ErrCodeInvalidArgument)))
	}
}

func normalizeFacadeArguments(spec facadeOperationSpec, input map[string]any) map[string]any {
	out := make(map[string]any)
	mergeFacadeObject(out, input["arguments"])
	mergeFacadeObject(out, input["options"])
	mergeFacadeObject(out, input["source"])
	mergeFacadeObject(out, input["context"])
	mergeFacadeObject(out, input["guard"])
	mergeFacadeObject(out, input["output"])
	for key, value := range input {
		switch key {
		case "operation", "arguments", "options", "source", "context", "guard", "output", "target", "to":
			continue
		}
		out[key] = value
	}
	if target, ok := input["target"].(map[string]any); ok {
		applyFacadeTarget(spec.Legacy, out, target)
	}
	if to, ok := input["to"].(map[string]any); ok {
		for key, value := range to {
			out["to_"+key] = value
		}
	}
	// Friendly edit aliases become the exact legacy vocabulary.
	if match, ok := out["match"]; ok {
		if spec.Legacy == "edit_symbol" {
			out["old_source"] = match
		} else {
			out["old_string"] = match
		}
		delete(out, "match")
	}
	if replacement, ok := out["replacement"]; ok {
		if spec.Legacy == "edit_symbol" {
			out["new_source"] = replacement
		} else {
			out["new_string"] = replacement
		}
		delete(out, "replacement")
	}
	normalizeFacadeAliases(spec, input, out)
	for key, value := range spec.Fixed {
		out[key] = value
	}
	return out
}

func normalizeFacadeAliases(spec facadeOperationSpec, input, out map[string]any) {
	alias := func(from, to string) {
		if value, ok := out[from]; ok {
			out[to] = value
			if from != to {
				delete(out, from)
			}
		}
	}
	switch spec.Facade + "." + spec.Operation {
	case "search.ast":
		alias("query", "pattern")
	case "search.winnow":
		alias("query", "text_match")
	case "relations.declaration":
		alias("query", "use_site")
	case "edit.batch":
		alias("changes", "edits")
	case "refactor.move":
		alias("destination", "target_file")
	case "trace.flow", "trace.path":
		if source := facadeSelector(input["target"], "symbol", "query"); source != nil {
			out["source_id"] = source
		}
		if sink := facadeSelector(input["to"], "symbol", "query"); sink != nil {
			out["sink_id"] = sink
		}
		delete(out, "id")
	case "trace.taint":
		if source := facadeSelector(input["target"], "query", "symbol"); source != nil {
			out["source_pattern"] = source
		}
		if sink := facadeSelector(input["to"], "query", "symbol"); sink != nil {
			out["sink_pattern"] = sink
		}
		delete(out, "id")
	}
}

func facadeSelector(raw any, keys ...string) any {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range keys {
		if value, exists := obj[key]; exists && value != nil && fmt.Sprint(value) != "" {
			return value
		}
	}
	return nil
}

func mergeFacadeObject(dst map[string]any, raw any) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for key, value := range obj {
		dst[key] = value
	}
}

func applyFacadeTarget(legacy string, out, target map[string]any) {
	set := func(key string, value any) {
		if value != nil {
			out[key] = value
		}
	}
	if file := target["file"]; file != nil {
		key := "path"
		switch legacy {
		case "find_co_changing_symbols":
			key = "file_path"
		}
		set(key, file)
	}
	if symbol := target["symbol"]; symbol != nil {
		key := "id"
		switch legacy {
		case "check_references", "find_co_changing_symbols":
			key = "symbol_id"
		case "find_import_path":
			key = "name"
		}
		set(key, symbol)
	}
	if symbols := target["symbols"]; symbols != nil {
		if values, ok := symbols.([]any); ok {
			parts := make([]string, 0, len(values))
			for _, value := range values {
				parts = append(parts, fmt.Sprint(value))
			}
			set("ids", strings.Join(parts, ","))
		} else if values, ok := symbols.([]string); ok {
			set("ids", strings.Join(values, ","))
		} else {
			set("ids", symbols)
		}
	}
	if query := target["query"]; query != nil {
		set("query", query)
	}
	if artifact := target["artifact"]; artifact != nil {
		set("id", artifact)
	}
	if repo := target["repo"]; repo != nil {
		set("repo", repo)
	}
}

func (s *Server) handleCapabilities(_ context.Context, req mcpgo.CallToolRequest) (result *mcpgo.CallToolResult, err error) {
	started := time.Now()
	telemetryOperation := "list"
	outcome := ""
	defer func() {
		if outcome == "" {
			outcome = classifyFacadeOutcome(result, err)
		}
		s.recordFacadeTelemetry("capabilities", telemetryOperation, outcome, time.Since(started))
	}()
	domain := normalizeFacadeOperation(req.GetString("domain", ""))
	operation := normalizeFacadeOperation(req.GetString("operation", ""))
	detail := normalizeFacadeOperation(req.GetString("detail", "summary"))
	if domain == "" {
		facades := make([]map[string]any, 0, len(facadeToolNames()))
		for _, name := range facadeToolNames() {
			facades = append(facades, map[string]any{
				"name": name, "description": facadeDescriptions[name], "operations": len(s.facades.operations(name)),
			})
		}
		return mcpgo.NewToolResultJSON(map[string]any{
			"surface_version": FacadeSurfaceVersion, "facades": facades,
		})
	}
	telemetryOperation = "domain"
	if !isFacadeToolName(domain) {
		telemetryOperation = "unknown"
		outcome = facadeOutcomeInvalidOperation
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown facade %q", domain),
			Data: map[string]any{"valid_facades": facadeToolNames()},
		}), nil
	}
	if operation != "" {
		telemetryOperation = "operation"
		spec, ok := s.facades.operation(domain, operation)
		if !ok {
			telemetryOperation = "unknown"
			outcome = facadeOutcomeInvalidOperation
			return NewStructuredErrorResult(StructuredError{
				ErrorCode: ErrCodeInvalidArgument, Message: fmt.Sprintf("unknown %s operation %q", domain, operation),
			}), nil
		}
		return mcpgo.NewToolResultJSON(s.facadeCapability(spec, detail == "schema"))
	}
	ops := make([]map[string]any, 0)
	for _, spec := range s.facades.operations(domain) {
		ops = append(ops, s.facadeCapability(spec, detail == "schema"))
	}
	return mcpgo.NewToolResultJSON(map[string]any{
		"surface_version": FacadeSurfaceVersion, "facade": domain, "operations": ops,
	})
}

func (s *Server) facadeCapability(spec facadeOperationSpec, includeSchema bool) map[string]any {
	legacy, available := s.facades.legacy(spec.Legacy)
	out := map[string]any{
		"surface_version": FacadeSurfaceVersion, "operation": spec.Operation, "effect": spec.Effect, "available": available,
	}
	if available {
		out["summary"] = firstSentence(legacy.tool.Description)
		if includeSchema {
			out["input_schema"] = legacy.tool.InputSchema
			if raw, err := json.Marshal(legacy.tool.InputSchema); err == nil {
				sum := sha256.Sum256(raw)
				out["schema_hash"] = hex.EncodeToString(sum[:])
			}
		}
	}
	return out
}

// applyFacadeSurface provides session-level surface negotiation. Legacy
// clients never see the new dedicated facade names. facade-v1 clients see
// exactly the 21 compact definitions, including reused names whose global
// registration still carries a legacy schema.
func (s *Server) applyFacadeSurface(ctx context.Context, tools []mcpgo.Tool) []mcpgo.Tool {
	p := s.effectiveSessionPolicy(ctx)
	if p == nil || p.preset != FacadeSurfaceVersion {
		out := tools[:0]
		for _, tool := range tools {
			if isDedicatedFacadeTool(tool.Name) {
				continue
			}
			if tool.Name == "ask" {
				if _, available := s.facades.legacy("ask"); !available {
					continue
				}
			}
			out = append(out, tool)
		}
		return out
	}
	byName := make(map[string]mcpgo.Tool, len(facadeToolNames()))
	for _, tool := range tools {
		if isFacadeToolName(tool.Name) {
			byName[tool.Name] = facadeToolDefinition(tool.Name)
		}
	}
	out := make([]mcpgo.Tool, 0, len(facadeToolNames()))
	for _, name := range facadeToolNames() {
		if tool, ok := byName[name]; ok {
			out = append(out, tool)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
