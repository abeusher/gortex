package mcp

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestLocalizationRefinementRequiredActionNamesFacadeReadSelector(t *testing.T) {
	const symbol = "repo/pkg/file.go::Resolver.Run"
	want := fmt.Sprintf(localizationRefinementRequiredActionFormat, symbol)
	completion := newLocalizationRefinementCompletion(symbol)
	if got := completion.RequiredAction; got != want {
		t.Fatalf("refinement action = %q, want %q", got, want)
	}
	if completion.refinementSymbol != symbol {
		t.Fatalf("refinement symbol = %q, want %q", completion.refinementSymbol, symbol)
	}
	if len(completion.AllowedSymbols) != 1 || completion.AllowedSymbols[0] != symbol {
		t.Fatalf("allowed symbols = %v, want [%q]", completion.AllowedSymbols, symbol)
	}
	if completion.ExactSymbol != "" {
		t.Fatalf("uncertain refinement falsely advertised exact symbol %q", completion.ExactSymbol)
	}

	encoded, err := json.Marshal(completion)
	if err != nil {
		t.Fatalf("marshal completion: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal completion: %v", err)
	}
	if got := payload["required_action"]; got != want {
		t.Fatalf("serialized refinement action = %q, want %q", got, want)
	}
	if _, exists := payload["exact_symbol"]; exists {
		t.Fatalf("uncertain refinement serialized exact_symbol: %#v", payload)
	}
}
