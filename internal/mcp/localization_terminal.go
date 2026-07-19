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
	localizationStateNeedsRefinement   = "needs_refinement"
	localizationStateRefineInFlight    = "refinement_in_flight"
	localizationStateExactReadInFlight = "exact_read_in_flight"
	localizationStateAnswerReady       = "answer_ready"
	localizationTerminalContractV2     = 2
)

// localizationCompletion is the host-neutral terminality contract returned by
// explore(operation:"localize"). Hosts may stop the turn from this payload;
// the server also enforces it for later Gortex navigation calls in the same
// MCP session.
type localizationCompletion struct {
	State            string   `json:"state"`
	Scope            string   `json:"scope"`
	RequiredAction   string   `json:"required_action"`
	AllowedToolCalls int      `json:"allowed_tool_calls"`
	ContractVersion  int      `json:"contract_version"`
	Enforceable      bool     `json:"enforceable"`
	ExactSymbol      string   `json:"exact_symbol,omitempty"`
	AllowedSymbols   []string `json:"allowed_symbols,omitempty"`

	// Route hops stay session-only, while AllowedSymbols exposes the exact
	// bounded authorization set carried by the wire contract.
	refinementSymbol  string
	refinementSymbols []string
	refinementRoutes  map[string]localizationRefinementRoute
	// enforceableOnAnswerReady is session-only provenance. A non-terminal
	// completion may carry a prevalidated future verdict through its one
	// authorized read without claiming that the current response is terminal.
	// It defaults false until the evidence policy explicitly opts in.
	enforceableOnAnswerReady bool

	// digest is the bounded evidence projection carried session-only through
	// reservation staging (see localization_digest.go). Post-terminal results
	// expose it only through host-only MCP _meta. It rides the
	// completion through reservation staging into commitLocalizationLocked,
	// which covers the direct-arm and facade finishLocalize paths alike.
	digest *localizationEvidenceDigest
}

// localizationRefinementRoute is session-only. A zero implementation symbol
// marks a concrete refinement candidate; a non-empty symbol is the one exact
// concrete hop prevalidated for a generic forwarder.
type localizationRefinementRoute struct {
	implementationSymbol string
	// enforceable is set only by the centralized evidence policy after it has
	// proved the entire route. A successful read alone never upgrades trust.
	enforceable bool
}

// localizationTerminalContract is the single wire shape used in visible MCP
// payloads and authoritative host-only metadata. Hosts must treat _meta as the
// authority; the visible copy remains useful to agents and legacy harnesses.
type localizationTerminalContract struct {
	Completion localizationCompletion `json:"completion"`
	Terminal   bool                   `json:"terminal"`
}

func localizationContractFor(completion localizationCompletion) localizationTerminalContract {
	if completion.ContractVersion == 0 {
		completion.ContractVersion = localizationTerminalContractV2
	}
	if completion.State != localizationStateAnswerReady {
		completion.Enforceable = false
	}
	return localizationTerminalContract{
		Completion: completion,
		Terminal:   completion.State == localizationStateAnswerReady,
	}
}

func newLocalizationCompletion(answerReady bool, exactSymbol string) localizationCompletion {
	if answerReady {
		return localizationCompletion{
			State:            localizationStateAnswerReady,
			Scope:            "localization",
			RequiredAction:   "respond",
			AllowedToolCalls: 0,
			ContractVersion:  localizationTerminalContractV2,
		}
	}
	return localizationCompletion{
		State:            localizationStateNeedsExactRead,
		Scope:            "localization",
		RequiredAction:   "read_exact",
		AllowedToolCalls: 1,
		ContractVersion:  localizationTerminalContractV2,
		ExactSymbol:      exactSymbol,
	}
}

func newLocalizationOpenCompletion() localizationCompletion {
	return localizationCompletion{
		State:            localizationStateInactive,
		Scope:            "localization",
		RequiredAction:   "continue",
		AllowedToolCalls: 0,
		ContractVersion:  localizationTerminalContractV2,
	}
}

// newLocalizationRefinementCompletion keeps uncertain localization successful
// and bounded. The ranked evidence remains usable, while the server permits
// exactly one source read selected from the explicit wire authorization set.
func newLocalizationRefinementCompletion(preferredSymbol string) localizationCompletion {
	return newLocalizationRefinementCompletionForSymbols(preferredSymbol, []string{preferredSymbol})
}

func newLocalizationRefinementCompletionForSymbols(preferredSymbol string, allowedSymbols []string) localizationCompletion {
	preferredSymbol = strings.TrimSpace(preferredSymbol)
	allowedSymbols = append([]string(nil), allowedSymbols...)
	return localizationCompletion{
		State:             localizationStateNeedsRefinement,
		Scope:             "localization",
		RequiredAction:    fmt.Sprintf(localizationRefinementRequiredActionFormat, preferredSymbol),
		AllowedToolCalls:  1,
		ContractVersion:   localizationTerminalContractV2,
		AllowedSymbols:    allowedSymbols,
		refinementSymbol:  preferredSymbol,
		refinementSymbols: append([]string(nil), allowedSymbols...),
	}
}

// localizationTerminalState is intentionally session-local. It bounds only
// localization navigation; mutation, workspace, session, memory, and
// capability tools remain usable after answer_ready and across later work.
type localizationTerminalState struct {
	mu                sync.Mutex
	state             string
	exactSymbol       string
	refinementSymbol  string
	refinementSymbols []string
	refinementRoutes  map[string]localizationRefinementRoute
	// inFlightImplementationSymbol is selected from refinementRoutes when the
	// actual requested candidate is authorized. It is never inferred from the
	// read result.
	inFlightImplementationSymbol string
	inFlightEnforceable          bool
	// enforceableOnAnswerReady persists a proven verdict across an authorized
	// exact/refinement read. Its zero value is deliberately advisory.
	enforceableOnAnswerReady bool
	taskFingerprint          string
	generation               uint64
	nextReservation          uint64
	reservation              *localizationReservation
	// digest is the evidence retained for the live contract; nil when the
	// contract is inactive or predates digest capture. Promotions through
	// finishReservedRead keep it — the evidence was stashed when the
	// contract was armed, before the permitted read ran.
	digest *localizationEvidenceDigest
}

type localizationReservation struct {
	token                  uint64
	generation             uint64
	pendingCompletion      localizationCompletion
	pendingTaskFingerprint string
	staged                 bool
}

func newLocalizationTerminalState() *localizationTerminalState {
	return &localizationTerminalState{}
}

func (s *localizationTerminalState) reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.generation++
	s.state = localizationStateInactive
	s.exactSymbol = ""
	s.refinementSymbol = ""
	s.refinementSymbols = nil
	s.refinementRoutes = nil
	s.inFlightImplementationSymbol = ""
	s.inFlightEnforceable = false
	s.enforceableOnAnswerReady = false
	s.taskFingerprint = ""
	s.digest = nil
	// Keep an in-flight reservation until its owner finishes. Its captured
	// generation is now stale, so finishLocalize cannot commit it, while a
	// second localization cannot race ahead of the still-running handler.
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
	defer s.mu.Unlock()
	fingerprint := localizationTaskFingerprint(task)
	if s.reservation != nil {
		s.reservation.pendingCompletion = completion
		s.reservation.pendingTaskFingerprint = fingerprint
		s.reservation.staged = true
		return
	}
	s.commitLocalizationLocked(completion, fingerprint)
}

// keepOpenForTask transactionally replaces any prior terminal contract with
// inactive navigation state. Under facade dispatch the inactive state is
// staged until the localization response succeeds; direct handlers commit it
// immediately.
func (s *localizationTerminalState) keepOpenForTask(task string) {
	s.armForTask(newLocalizationOpenCompletion(), task)
}

func (s *localizationTerminalState) armRefinementForTask(task, preferredSymbol string, symbols []string, digest *localizationEvidenceDigest) {
	routes := make(map[string]localizationRefinementRoute, len(symbols))
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol != "" {
			routes[symbol] = localizationRefinementRoute{}
		}
	}
	s.armRefinementRoutesForTask(task, preferredSymbol, symbols, routes, digest)
}

func (s *localizationTerminalState) armRefinementRoutesForTask(
	task, preferredSymbol string,
	symbols []string,
	routes map[string]localizationRefinementRoute,
	digest *localizationEvidenceDigest,
) {
	preferredSymbol = strings.TrimSpace(preferredSymbol)
	seen := make(map[string]struct{}, min(len(symbols), localizationRefinementAllowedSymbolCap))
	refinementSymbols := make([]string, 0, min(len(symbols), localizationRefinementAllowedSymbolCap))
	refinementRoutes := make(map[string]localizationRefinementRoute, min(len(symbols), localizationRefinementAllowedSymbolCap))
	for _, symbol := range symbols {
		symbol = strings.TrimSpace(symbol)
		if symbol == "" {
			continue
		}
		route, authorized := routes[symbol]
		if !authorized {
			continue
		}
		route.implementationSymbol = strings.TrimSpace(route.implementationSymbol)
		if route.implementationSymbol == symbol {
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			continue
		}
		seen[symbol] = struct{}{}
		refinementSymbols = append(refinementSymbols, symbol)
		refinementRoutes[symbol] = route
		if len(refinementSymbols) == localizationRefinementAllowedSymbolCap {
			break
		}
	}
	if len(refinementSymbols) == 0 {
		s.keepOpenForTask(task)
		return
	}
	if _, exists := seen[preferredSymbol]; !exists {
		s.keepOpenForTask(task)
		return
	}
	completion := newLocalizationRefinementCompletionForSymbols(preferredSymbol, refinementSymbols)
	completion.refinementRoutes = refinementRoutes
	// Stashed now, not at promotion: when the permitted read succeeds,
	// finishReservedRead flips this contract to answer_ready and the
	// evidence must already be retained for replay.
	completion.digest = digest
	s.armForTask(completion, task)
}

func (s *localizationTerminalState) commitLocalizationLocked(completion localizationCompletion, fingerprint string) {
	s.generation++
	s.state = completion.State
	s.exactSymbol = completion.ExactSymbol
	s.refinementSymbol = ""
	s.refinementSymbols = nil
	s.refinementRoutes = nil
	s.inFlightImplementationSymbol = ""
	s.inFlightEnforceable = false
	s.enforceableOnAnswerReady = completion.enforceableOnAnswerReady
	if completion.State == localizationStateAnswerReady {
		s.enforceableOnAnswerReady = completion.Enforceable
	}
	if completion.State == localizationStateNeedsRefinement {
		s.refinementSymbol = completion.refinementSymbol
		s.refinementSymbols = append([]string(nil), completion.refinementSymbols...)
		s.refinementRoutes = cloneLocalizationRefinementRoutes(completion.refinementRoutes)
	}
	s.taskFingerprint = fingerprint
	// The digest follows the contract: an inactive commit (keepOpenForTask)
	// carries nil and clears it; every localize commit replaces it.
	s.digest = completion.digest
}

func (s *localizationTerminalState) completionLocked() localizationCompletion {
	var completion localizationCompletion
	switch s.state {
	case localizationStateNeedsRefinement, localizationStateRefineInFlight:
		completion = newLocalizationRefinementCompletionForSymbols(s.refinementSymbol, s.refinementSymbols)
		completion.refinementRoutes = cloneLocalizationRefinementRoutes(s.refinementRoutes)
		if s.state == localizationStateRefineInFlight {
			completion.State = localizationStateRefineInFlight
			completion.AllowedToolCalls = 0
		}
	case localizationStateNeedsExactRead, localizationStateExactReadInFlight:
		completion = newLocalizationCompletion(false, s.exactSymbol)
	default:
		completion = newLocalizationCompletion(true, "")
	}
	completion.enforceableOnAnswerReady = s.enforceableOnAnswerReady
	if completion.State == localizationStateAnswerReady {
		completion.Enforceable = s.enforceableOnAnswerReady
	}
	completion.digest = s.digest
	return completion
}

// interceptAnswerReady is the cheap pre-validation gate used by facade
// dispatch. It makes localization terminality independent of operation
// validity while deliberately leaving non-navigation facades untouched.
func (s *localizationTerminalState) interceptAnswerReady(facade, operation string) *mcpgo.CallToolResult {
	if s == nil || !localizationNavigationFacade(facade) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != localizationStateAnswerReady {
		return nil
	}
	return localizationTerminalResult(s.completionLocked(), facade, operation)
}

func (s *localizationTerminalState) refinementAllowsLocked(symbol string) bool {
	if symbol == "" {
		return false
	}
	_, authorized := s.refinementRoutes[symbol]
	return authorized
}

// beginLocalize reserves the only localization handler slot for this session.
// An inactive session admits its first localization without a boundary flag.
// Once a contract exists, only the first explore call for a genuinely new user
// request may cross it, and the caller must say so explicitly. Localize stages
// its returned completion; task stages inactive navigation. The old contract
// remains live until finishLocalize commits the successful replacement.
func (s *localizationTerminalState) beginLocalize(task string, newUserTask bool) (uint64, *mcpgo.CallToolResult) {
	if s == nil {
		return 0, nil
	}
	fingerprint := localizationTaskFingerprint(task)
	if fingerprint == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservation != nil {
		return 0, localizationInProgressResult()
	}
	if s.state != localizationStateInactive && !newUserTask {
		completion := s.completionLocked()
		// A repeat localize against a terminal contract gets the same compact,
		// typed non-retriable signal as every other post-terminal navigation
		// call. The original successful result already holds the evidence.
		if s.state == localizationStateAnswerReady {
			return 0, localizationTerminalResult(completion, "explore", "localize")
		}
		return 0, NewStructuredErrorResult(StructuredError{
			ErrorCode: ErrCodeLocalizationComplete,
			Message:   "this user request already has a localization completion contract; follow it instead of starting another localize call",
			Data: map[string]any{
				"completion": completion,
				"facade":     "explore",
				"operation":  "localize",
			},
		})
	}
	s.nextReservation++
	if s.nextReservation == 0 {
		s.nextReservation++
	}
	token := s.nextReservation
	s.reservation = &localizationReservation{token: token, generation: s.generation}
	return token, nil
}

// finishLocalize commits only the completion staged by the matching reservation
// and only if no reset changed its generation. Errors and panics pass success=false
// and leave the prior contract untouched. A stale finisher can never clear or
// overwrite a newer reservation.
func (s *localizationTerminalState) finishLocalize(token uint64, success bool) bool {
	if s == nil || token == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reservation := s.reservation
	if reservation == nil || reservation.token != token {
		return false
	}
	s.reservation = nil
	if !success || !reservation.staged || reservation.generation != s.generation {
		return false
	}
	s.commitLocalizationLocked(reservation.pendingCompletion, reservation.pendingTaskFingerprint)
	return true
}

func localizationInProgressResult() *mcpgo.CallToolResult {
	return NewStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeLocalizationComplete,
		Message:   "a localization request is already in progress for this session",
		Data: map[string]any{
			"completion": map[string]any{
				"state": "localization_in_progress", "scope": "localization",
				"required_action": "wait", "allowed_tool_calls": 0,
				"contract_version": localizationTerminalContractV2,
				"enforceable":      false,
			},
			"facade": "explore", "operation": "localize",
		},
	})
}

func localizationTaskFingerprint(task string) string {
	return strings.Join(strings.Fields(task), " ")
}

// authorize checks a navigation call and reserves the single permitted
// localization read when applicable. The caller must finish the reservation
// after invocation so a failed read restores the allowance instead of silently
// consuming it.
func (s *localizationTerminalState) authorize(facade, operation string, arguments map[string]any) (*mcpgo.CallToolResult, bool) {
	if s == nil || !localizationNavigationFacade(facade) {
		return nil, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reservation != nil {
		return localizationInProgressResult(), false
	}
	if s.state == localizationStateInactive {
		return nil, false
	}
	// answer_ready terminates only localization navigation. Catch those facades
	// before their handlers can run and return a compact typed instruction;
	// unrelated work remains dispatchable through the early return above.
	if s.state == localizationStateAnswerReady {
		return localizationTerminalResult(s.completionLocked(), facade, operation), false
	}
	if s.state == localizationStateNeedsExactRead && facade == "read" && operation == "source" && exactLocalizationSymbol(arguments) == s.exactSymbol {
		s.state = localizationStateExactReadInFlight
		return nil, true
	}
	refinementSymbol := exactLocalizationSymbol(arguments)
	if s.state == localizationStateNeedsRefinement && facade == "read" && operation == "source" && s.refinementAllowsLocked(refinementSymbol) {
		route := s.refinementRoutes[refinementSymbol]
		s.inFlightImplementationSymbol = route.implementationSymbol
		s.inFlightEnforceable = route.enforceable
		s.state = localizationStateRefineInFlight
		return nil, true
	}

	completion := s.completionLocked()
	message := "localization is complete; return the existing evidence without another Gortex navigation call"
	switch s.state {
	case localizationStateNeedsExactRead:
		message = fmt.Sprintf("localization needs exactly one read(operation:\"source\") for %q; other navigation calls are blocked", s.exactSymbol)
	case localizationStateExactReadInFlight:
		message = "the permitted exact localization read is already in progress"
	case localizationStateNeedsRefinement:
		message = fmt.Sprintf("localization permits exactly one read(operation:\"source\") for %q; other navigation calls are blocked", s.refinementSymbol)
	case localizationStateRefineInFlight:
		message = "the permitted localization refinement read is already in progress"
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

func (s *localizationTerminalState) finishReservedRead(success bool) localizationCompletion {
	if s == nil {
		return newLocalizationCompletion(true, "")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch s.state {
	case localizationStateExactReadInFlight:
		if success {
			s.state = localizationStateAnswerReady
			s.exactSymbol = ""
			s.inFlightImplementationSymbol = ""
			return s.completionLocked()
		}
		s.inFlightImplementationSymbol = ""
		s.inFlightEnforceable = false
		s.enforceableOnAnswerReady = false
		s.state = localizationStateNeedsExactRead
	case localizationStateRefineInFlight:
		if success {
			implementationSymbol := s.inFlightImplementationSymbol
			enforceable := s.inFlightEnforceable
			s.inFlightImplementationSymbol = ""
			s.inFlightEnforceable = false
			s.enforceableOnAnswerReady = enforceable
			s.refinementSymbol = ""
			s.refinementSymbols = nil
			s.refinementRoutes = nil
			if implementationSymbol != "" {
				s.state = localizationStateNeedsExactRead
				s.exactSymbol = implementationSymbol
				return s.completionLocked()
			}
			s.state = localizationStateAnswerReady
			return s.completionLocked()
		}
		s.inFlightImplementationSymbol = ""
		s.inFlightEnforceable = false
		s.enforceableOnAnswerReady = false
		s.state = localizationStateNeedsRefinement
	}
	return s.completionLocked()
}

// localizationTerminalResult is the compact, typed suppression returned only
// after a successful localization response established answer_ready. It never
// replays evidence and is non-retriable by default.
func localizationTerminalResult(completion localizationCompletion, facade, operation string) *mcpgo.CallToolResult {
	data := map[string]any{"contract": localizationContractFor(completion)}
	if facade != "" {
		data["facade"] = facade
	}
	if operation != "" {
		data["operation"] = operation
	}
	return newStructuredErrorResult(StructuredError{
		ErrorCode: ErrCodeLocalizationTerminal,
		Message:   "localization is terminal for this user request; respond using the evidence already returned",
		Retriable: false,
		Data:      data,
	}, true)
}

func cloneLocalizationRefinementRoutes(routes map[string]localizationRefinementRoute) map[string]localizationRefinementRoute {
	if len(routes) == 0 {
		return nil
	}
	cloned := make(map[string]localizationRefinementRoute, len(routes))
	for symbol, route := range routes {
		cloned[symbol] = route
	}
	return cloned
}

// block is retained for direct state checks; production dispatch uses
// authorize so it can finish a reserved exact read after handler completion.
func (s *localizationTerminalState) block(facade, operation string, arguments map[string]any) *mcpgo.CallToolResult {
	blocked, _ := s.authorize(facade, operation, arguments)
	return blocked
}

func localizationNavigationFacade(facade string) bool {
	switch facade {
	case "explore", "search", "read", "relations", "trace", "analyze":
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
