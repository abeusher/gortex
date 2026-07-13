package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const (
	localizationStateInactive          = ""
	localizationStateNeedsExactRead    = "needs_exact_read"
	localizationStateExactReadInFlight = "exact_read_in_flight"
	localizationStateAnswerReady       = "answer_ready"
)

// localizationCompletion is the host-neutral terminality contract returned by
// explore(operation:"localize"). Hosts may stop the turn from this payload;
// the server also enforces it for later Gortex navigation calls in the same
// MCP session.
type localizationCompletion struct {
	State            string `json:"state"`
	Scope            string `json:"scope"`
	RequiredAction   string `json:"required_action"`
	AllowedToolCalls int    `json:"allowed_tool_calls"`
	ExactSymbol      string `json:"exact_symbol,omitempty"`
}

func newLocalizationCompletion(answerReady bool, exactSymbol string) localizationCompletion {
	if answerReady {
		return localizationCompletion{
			State:            localizationStateAnswerReady,
			Scope:            "localization",
			RequiredAction:   "respond",
			AllowedToolCalls: 0,
		}
	}
	return localizationCompletion{
		State:            localizationStateNeedsExactRead,
		Scope:            "localization",
		RequiredAction:   "read_exact",
		AllowedToolCalls: 1,
		ExactSymbol:      exactSymbol,
	}
}

// localizationTerminalState is intentionally session-local. It never affects
// mutation, analysis, workspace, or memory tools; it only prevents an agent
// from reopening localization after an explicit localization-only request.
type localizationTerminalState struct {
	mu              sync.Mutex
	state           string
	exactSymbol     string
	taskFingerprint string
}

func newLocalizationTerminalState() *localizationTerminalState {
	return &localizationTerminalState{}
}

func (s *localizationTerminalState) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.state = localizationStateInactive
	s.exactSymbol = ""
	s.taskFingerprint = ""
	s.mu.Unlock()
}

func (s *localizationTerminalState) arm(completion localizationCompletion) {
	s.armForTask(completion, "")
}

func (s *localizationTerminalState) armForTask(completion localizationCompletion, task string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.state = completion.State
	s.exactSymbol = completion.ExactSymbol
	s.taskFingerprint = localizationTaskFingerprint(task)
	s.mu.Unlock()
}

// beginLocalize admits a new explicit localization request without mutating the
// prior completion contract. Repeating the same normalized task is a navigation
// loop; a successful different task atomically replaces the contract in armForTask.
func (s *localizationTerminalState) beginLocalize(task string) *mcpgo.CallToolResult {
	if s == nil {
		return nil
	}
	fingerprint := localizationTaskFingerprint(task)
	if fingerprint == "" {
		return nil
	}
	s.mu.Lock()
	if s.state == localizationStateInactive {
		s.mu.Unlock()
		return nil
	}
	if fingerprint != "" && fingerprint == s.taskFingerprint {
		completion := newLocalizationCompletion(s.state == localizationStateAnswerReady, s.exactSymbol)
		s.mu.Unlock()
		return NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeLocalizationComplete,
			Message:   "this localization request already has a completion contract; follow it instead of repeating localize",
			Data: map[string]any{
				"completion": completion,
				"facade":     "explore",
				"operation":  "localize",
			},
		})
	}
	s.mu.Unlock()
	return nil
}

func localizationTaskFingerprint(task string) string {
	return strings.Join(strings.Fields(task), " ")
}

// authorize checks a navigation call and reserves the one exact-read slot when
// applicable. The caller must finish the reservation after invocation so a
// failed read restores the allowance instead of silently consuming it.
func (s *localizationTerminalState) authorize(facade, operation string, arguments map[string]any) (*mcpgo.CallToolResult, bool) {
	if s == nil || !localizationNavigationFacade(facade) {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == localizationStateInactive {
		return nil, false
	}
	if s.state == localizationStateNeedsExactRead && facade == "read" && operation == "source" && exactLocalizationSymbol(arguments) == s.exactSymbol {
		s.state = localizationStateExactReadInFlight
		return nil, true
	}

	answerReady := s.state == localizationStateAnswerReady
	completion := newLocalizationCompletion(answerReady, s.exactSymbol)
	message := "localization is complete; return the existing evidence without another Gortex navigation call"
	switch s.state {
	case localizationStateNeedsExactRead:
		message = fmt.Sprintf("localization needs exactly one read(operation:\"source\") for %q; other navigation calls are blocked", s.exactSymbol)
	case localizationStateExactReadInFlight:
		message = "the permitted exact localization read is already in progress"
	}
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeLocalizationComplete,
		Message:   message,
		Data: map[string]any{
			"completion": completion,
			"facade":     facade,
			"operation":  operation,
		},
	}), false
}

func (s *localizationTerminalState) finishExactRead(success bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != localizationStateExactReadInFlight {
		return
	}
	if success {
		s.state = localizationStateAnswerReady
		s.exactSymbol = ""
		return
	}
	s.state = localizationStateNeedsExactRead
}

// block is retained for direct state checks; production dispatch uses
// authorize so it can finish a reserved exact read after handler completion.
func (s *localizationTerminalState) block(facade, operation string, arguments map[string]any) *mcpgo.CallToolResult {
	blocked, _ := s.authorize(facade, operation, arguments)
	return blocked
}

func localizationNavigationFacade(facade string) bool {
	switch facade {
	case "explore", "search", "read", "relations", "trace":
		return true
	default:
		return false
	}
}

func exactLocalizationSymbol(arguments map[string]any) string {
	if target, ok := arguments["target"].(map[string]any); ok {
		return strings.TrimSpace(fmt.Sprint(target["symbol"]))
	}
	return strings.TrimSpace(fmt.Sprint(arguments["symbol"]))
}

func (s *Server) localizationFor(ctx context.Context) *localizationTerminalState {
	id := SessionIDFromContext(ctx)
	if id == "" || s.sessions == nil {
		return s.localization
	}
	return s.sessions.get(id).localization
}
