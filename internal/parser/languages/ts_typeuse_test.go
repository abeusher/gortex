package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// typedAsTo counts EdgeTypedAs edges whose target is unresolved::<name>.
func typedAsTo(edges []*graph.Edge, name string) []*graph.Edge {
	var out []*graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::"+name {
			out = append(out, e)
		}
	}
	return out
}

// TestTSTypeUse_VariableAnnotationEmitsTypedAs pins the LSP-free recall
// fix: a type used only in variable / const annotation position must
// emit an EdgeTypedAs to unresolved::<Type> so find_usages lands it
// cross-file without a language server. Before the fix, `const el: T`
// only seeded the local type-env map and emitted no edge (recall ~0).
func TestTSTypeUse_VariableAnnotationEmitsTypedAs(t *testing.T) {
	src := `import { ExcalidrawElement } from "./types";

export function render(): void {
	const el: ExcalidrawElement = getElement();
	let pending: ExcalidrawElement[] = [];
	doStuff(el, pending);
}
`
	_, edges := runTSExtract(t, "src/render.ts", src)

	hits := typedAsTo(edges, "ExcalidrawElement")
	if len(hits) < 2 {
		t.Fatalf("expected >=2 EdgeTypedAs -> unresolved::ExcalidrawElement (const + let), got %d", len(hits))
	}
	// Each usage must be attributed to its enclosing function, not dropped.
	for _, e := range hits {
		if !strings.Contains(e.From, "render") {
			t.Errorf("EdgeTypedAs From = %q, want it attributed to render()", e.From)
		}
	}
}

// TestTSTypeUse_ClassFieldEmitsTypedAs pins the field-annotation case:
// `field: T` references T. Primitives (number) must NOT emit an edge.
func TestTSTypeUse_ClassFieldEmitsTypedAs(t *testing.T) {
	src := `class Scene {
	private active: ExcalidrawElement | null = null;
	count: number = 0;
}
`
	_, edges := runTSExtract(t, "src/scene.ts", src)

	hits := typedAsTo(edges, "ExcalidrawElement")
	if len(hits) == 0 {
		t.Fatalf("expected field EdgeTypedAs -> unresolved::ExcalidrawElement, got none")
	}
	for _, e := range hits {
		if !strings.Contains(e.From, "Scene") {
			t.Errorf("field EdgeTypedAs From = %q, want it attributed to the Scene field", e.From)
		}
	}
	if got := typedAsTo(edges, "number"); len(got) != 0 {
		t.Errorf("primitive `number` must not emit EdgeTypedAs, got %d", len(got))
	}
}

// TestTSTypeUse_TopLevelConstAttributesToFile pins the file-fallback: a
// top-level `const x: T` outside any function attributes to the file
// node rather than being dropped.
func TestTSTypeUse_TopLevelConstAttributesToFile(t *testing.T) {
	src := `const config: AppConfig = loadConfig();
`
	_, edges := runTSExtract(t, "src/config.ts", src)

	hits := typedAsTo(edges, "AppConfig")
	if len(hits) != 1 {
		t.Fatalf("expected 1 EdgeTypedAs -> unresolved::AppConfig, got %d", len(hits))
	}
	if hits[0].From != "src/config.ts" {
		t.Errorf("top-level const EdgeTypedAs From = %q, want the file node src/config.ts", hits[0].From)
	}
}
