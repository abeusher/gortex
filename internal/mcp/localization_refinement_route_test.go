package mcp

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRefinementRouteConcreteReadCompletesInOneCall(t *testing.T) {
	state := &localizationTerminalState{}
	concrete := "repo/replace.go::replaceAll"
	state.armRefinementRoutesForTask(
		"find the replace implementation",
		concrete,
		[]string{concrete},
		map[string]localizationRefinementRoute{concrete: {}},
		nil,
	)

	requireRefinementSourceReservation(t, state, concrete)
	completion := state.finishReservedRead(true)
	requireRefinementCompletion(t, completion, localizationStateAnswerReady, "", 0)

	result, reserved := state.authorize("read", "source", refinementSourceArgs(concrete))
	if reserved {
		t.Fatal("read after concrete completion reserved a handler")
	}
	requireLocalizationTerminalError(t, result, "read", "source")
}

func TestRefinementRouteUsesActuallySelectedAlternateGenericCandidate(t *testing.T) {
	state := &localizationTerminalState{}
	preferred := "repo/replace.go::replaceAll"
	generic := "repo/replacer.go::Replacer.Replace"
	implementation := "repo/replacer.go::Replacer.replaceAll"
	routes := map[string]localizationRefinementRoute{
		preferred: {},
		generic:   {implementationSymbol: implementation},
	}
	state.armRefinementRoutesForTask(
		"find the replace implementation",
		preferred,
		[]string{preferred, generic},
		routes,
		nil,
	)

	requireRefinementSourceReservation(t, state, generic)
	completion := state.finishReservedRead(true)
	requireRefinementCompletion(t, completion, localizationStateNeedsExactRead, implementation, 1)

	requireRefinementSourceReservation(t, state, implementation)
	completion = state.finishReservedRead(true)
	requireRefinementCompletion(t, completion, localizationStateAnswerReady, "", 0)

	result, reserved := state.authorize("read", "source", refinementSourceArgs(implementation))
	if reserved {
		t.Fatal("third read reserved a handler")
	}
	requireLocalizationTerminalError(t, result, "read", "source")
}

func TestRefinementRouteGenericReadFailureRestoresFirstAllowance(t *testing.T) {
	state := &localizationTerminalState{}
	generic := "repo/replacer.go::Replacer.Replace"
	implementation := "repo/replacer.go::Replacer.replaceAll"
	state.armRefinementRoutesForTask(
		"find the replace implementation",
		generic,
		[]string{generic},
		map[string]localizationRefinementRoute{
			generic: {implementationSymbol: implementation},
		},
		nil,
	)

	requireRefinementSourceReservation(t, state, generic)
	completion := state.finishReservedRead(false)
	requireRefinementCompletion(t, completion, localizationStateNeedsRefinement, "", 1)

	requireRefinementSourceReservation(t, state, generic)
	state.finishReservedRead(false)
}

func TestRefinementRouteExactHopFailureRestoresExactAllowance(t *testing.T) {
	state := &localizationTerminalState{}
	generic := "repo/replacer.go::Replacer.Replace"
	implementation := "repo/replacer.go::Replacer.replaceAll"
	state.armRefinementRoutesForTask(
		"find the replace implementation",
		generic,
		[]string{generic},
		map[string]localizationRefinementRoute{
			generic: {implementationSymbol: implementation},
		},
		nil,
	)

	requireRefinementSourceReservation(t, state, generic)
	completion := state.finishReservedRead(true)
	requireRefinementCompletion(t, completion, localizationStateNeedsExactRead, implementation, 1)

	requireRefinementSourceReservation(t, state, implementation)
	completion = state.finishReservedRead(false)
	requireRefinementCompletion(t, completion, localizationStateNeedsExactRead, implementation, 1)

	requireRefinementSourceReservation(t, state, implementation)
	state.finishReservedRead(true)
}

func TestRefinementAllowedSymbolsMirrorSessionAuthorization(t *testing.T) {
	state := &localizationTerminalState{}
	preferred := "repo/replace.go::replaceAll"
	generic := "repo/replacer.go::Replacer.Replace"
	implementation := "repo/replacer.go::Replacer.replaceAll"
	state.armRefinementRoutesForTask(
		"find the replace implementation",
		preferred,
		[]string{preferred, generic},
		map[string]localizationRefinementRoute{
			preferred: {},
			generic:   {implementationSymbol: implementation},
		},
		nil,
	)

	wire, err := json.Marshal(state.completionLocked())
	if err != nil {
		t.Fatalf("marshal localization completion: %v", err)
	}
	var completion localizationCompletion
	if err := json.Unmarshal(wire, &completion); err != nil {
		t.Fatalf("unmarshal localization completion: %v", err)
	}
	if !reflect.DeepEqual(completion.AllowedSymbols, state.refinementSymbols) {
		t.Fatalf("wire allowed symbols = %v, state authorization = %v", completion.AllowedSymbols, state.refinementSymbols)
	}
	if !strings.Contains(completion.RequiredAction, "completion.allowed_symbols") {
		t.Fatalf("required action does not name the authorization field: %q", completion.RequiredAction)
	}
	if strings.Contains(string(wire), implementation) {
		t.Fatalf("serialized completion leaks session-only implementation hop %q: %s", implementation, wire)
	}
}

func refinementSourceArgs(symbol string) map[string]any {
	return map[string]any{"target": map[string]any{"symbol": symbol}}
}

func requireRefinementSourceReservation(t *testing.T, state *localizationTerminalState, symbol string) {
	t.Helper()
	blocked, reserved := state.authorize("read", "source", refinementSourceArgs(symbol))
	if blocked != nil || !reserved {
		t.Fatalf("source read reservation for %q = (%+v, %v), want reservation", symbol, blocked, reserved)
	}
}

func requireRefinementCompletion(t *testing.T, completion localizationCompletion, state, exactSymbol string, allowed int) {
	t.Helper()
	if completion.State != state || completion.ExactSymbol != exactSymbol || completion.AllowedToolCalls != allowed {
		t.Fatalf(
			"completion = {state:%q exact:%q allowed:%d}, want {state:%q exact:%q allowed:%d}",
			completion.State,
			completion.ExactSymbol,
			completion.AllowedToolCalls,
			state,
			exactSymbol,
			allowed,
		)
	}
}
