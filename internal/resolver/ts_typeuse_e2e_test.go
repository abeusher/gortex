package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser/languages"
)

// extractTSInto runs the real TypeScript extractor and adds its nodes /
// edges to g, stamping a common RepoPrefix so the per-repo resolver
// treats both files as one repo. Returns the emitted edges so callers
// can locate one and assert its target after resolution. No LSP, no
// type checker — pure tree-sitter extraction.
func extractTSInto(t *testing.T, g *graph.Graph, repo, path, src string) []*graph.Edge {
	t.Helper()
	res, err := languages.NewTypeScriptExtractor().Extract(path, []byte(src))
	require.NoError(t, err, "extract %s", path)
	for _, n := range res.Nodes {
		n.RepoPrefix = repo
		g.AddNode(n)
	}
	for _, e := range res.Edges {
		g.AddEdge(e)
	}
	return res.Edges
}

// TestTSTypeUse_VariableAnnotationResolvesCrossFileNoLSP proves the
// recall fix end to end with NO language server: a type used only in a
// variable annotation in render.ts resolves to its interface definition
// in types.ts via tree-sitter extraction + the name-based resolver.
// Before the fix, `const el: ExcalidrawElement` emitted no edge at all,
// so find_usages reported zero references unless an LSP filled the gap.
func TestTSTypeUse_VariableAnnotationResolvesCrossFileNoLSP(t *testing.T) {
	g := graph.New()
	extractTSInto(t, g, "app", "app/types.ts", `
export interface ExcalidrawElement {
	id: string;
}
`)
	edges := extractTSInto(t, g, "app", "app/render.ts", `
import { ExcalidrawElement } from "./types";

export function render(): void {
	const el: ExcalidrawElement = getElement();
	use(el);
}
`)

	var typeUse *graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::ExcalidrawElement" {
			typeUse = e
		}
	}
	require.NotNil(t, typeUse,
		"extractor must emit EdgeTypedAs -> unresolved::ExcalidrawElement for the variable annotation")

	New(g).ResolveAll()

	assert.Equal(t, "app/types.ts::ExcalidrawElement", typeUse.To,
		"variable-annotation type use must resolve cross-file to the interface def without LSP")
}

// TestTSTypeUse_FieldAnnotationResolvesCrossFileNoLSP does the same for a
// class field annotation (`active: ExcalidrawElement | null`).
func TestTSTypeUse_FieldAnnotationResolvesCrossFileNoLSP(t *testing.T) {
	g := graph.New()
	extractTSInto(t, g, "app", "app/types.ts", `
export interface ExcalidrawElement {
	id: string;
}
`)
	edges := extractTSInto(t, g, "app", "app/scene.ts", `
import { ExcalidrawElement } from "./types";

export class Scene {
	active: ExcalidrawElement | null = null;
}
`)

	var typeUse *graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::ExcalidrawElement" {
			typeUse = e
		}
	}
	require.NotNil(t, typeUse, "extractor must emit EdgeTypedAs for the field annotation")

	New(g).ResolveAll()

	assert.Equal(t, "app/types.ts::ExcalidrawElement", typeUse.To,
		"field type use must resolve cross-file without LSP")
}
