package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestCaptureNgRxEffects(t *testing.T) {
	src := `import { createEffect, ofType } from '@ngrx/effects';
import { Injectable } from '@angular/core';

@Injectable()
export class UserEffects {
  loadUsers$ = createEffect(() => this.actions$.pipe(
    ofType(LoadUsers),
    mergeMap(() => this.api.load())
  ));
}
`
	res, err := NewTypeScriptExtractor().Extract("effects.ts", []byte(src))
	require.NoError(t, err)

	var effect *graph.Node
	for _, n := range res.Nodes {
		if n.Name == "loadUsers$" {
			effect = n
		}
	}
	require.NotNil(t, effect, "effect property should be extracted")
	assert.Equal(t, "loadUsers$", effect.Meta["ngrx_effect"], "effect node should be tagged")

	var ph *graph.Edge
	for _, e := range res.Edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v == "ngrx-effect" {
			ph = e
		}
	}
	require.NotNil(t, ph, "createEffect+ofType should emit a placeholder dispatch edge")
	assert.Equal(t, effect.ID, ph.From, "placeholder attributed to the effect")
	assert.Equal(t, "LoadUsers", ph.Meta["ngrx_action"])
	assert.Equal(t, "unresolved::*.LoadUsers", ph.To)
	assert.Equal(t, graph.EdgeCalls, ph.Kind)
}
