package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// unresolvedCallNames collects the leaf names of every unresolved call/ref edge.
func unresolvedCallNames(edges []*graph.Edge) map[string]bool {
	out := map[string]bool{}
	for _, e := range edges {
		switch e.Kind {
		case graph.EdgeCalls, graph.EdgeReferences, graph.EdgeInstantiates, graph.EdgeReads, graph.EdgeImports:
			if graph.IsUnresolvedTarget(e.To) {
				out[graph.UnresolvedName(e.To)] = true
			}
		}
	}
	return out
}

// TestTemplateHandlerSynthAndSuppression proves the Vue passes: compiler
// macros / Nuxt auto-imports leave no unresolved edges (precision), a real
// user call is kept, and a `@click` template binding produces a callback
// reference edge to the in-script handler (the template→code link).
func TestTemplateHandlerSynthAndSuppression(t *testing.T) {
	sfc := "<script setup lang=\"ts\">\n" +
		"import { ref } from 'vue'\n" +
		"const props = defineProps<{ msg: string }>()\n" +
		"const emit = defineEmits(['change'])\n" +
		"const { data } = useFetch('/api/items')\n" +
		"function increment() { realHelper() }\n" +
		"function realHelper() {}\n" +
		"</script>\n" +
		"<template>\n" +
		"  <button @click=\"increment\">{{ data }}</button>\n" +
		"  <Child v-on:select=\"realHelper\" />\n" +
		"</template>\n"

	res, err := NewVueExtractor().Extract("Counter.vue", []byte(sfc))
	if err != nil {
		t.Fatal(err)
	}

	// Macros are suppressed.
	unresolved := unresolvedCallNames(res.Edges)
	for _, macro := range []string{"defineProps", "defineEmits", "useFetch"} {
		if unresolved[macro] {
			t.Errorf("framework macro %q was not suppressed (left an unresolved edge)", macro)
		}
	}

	// A real user call survives (resolved or unresolved — just not dropped).
	var keptRealHelperCall bool
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls && strings.HasSuffix(e.To, "realHelper") {
			keptRealHelperCall = true
		}
	}
	if !keptRealHelperCall {
		t.Error("a real user call (realHelper) was wrongly suppressed")
	}

	// The @click handler is wired to the in-script function via a callback ref.
	var handlerBound bool
	for _, e := range res.Edges {
		if e.Meta != nil && e.Meta["via"] == "template_handler" &&
			strings.HasSuffix(e.To, "::increment") {
			handlerBound = true
			if e.Meta["ref_context"] != graph.RefContextCallback {
				t.Errorf("template handler ref_context=%v, want callback", e.Meta["ref_context"])
			}
		}
	}
	if !handlerBound {
		t.Error("@click=\"increment\" did not produce a template_handler edge to the increment function")
	}
}

// TestFrameworkSuppressionRunes proves Svelte rune / lifecycle suppression and
// the Svelte on:click handler binding — the precision win across frameworks.
func TestFrameworkSuppressionRunes(t *testing.T) {
	svelte := "<script lang=\"ts\">\n" +
		"  let count = $state(0)\n" +
		"  let doubled = $derived(count * 2)\n" +
		"  function handle() {}\n" +
		"  onMount(() => {})\n" +
		"</script>\n" +
		"<button on:click={handle}>{doubled}</button>\n"

	res, err := NewSvelteExtractor().Extract("Counter.svelte", []byte(svelte))
	if err != nil {
		t.Fatal(err)
	}
	unresolved := unresolvedCallNames(res.Edges)
	for _, rune_ := range []string{"$state", "$derived", "onMount"} {
		if unresolved[rune_] {
			t.Errorf("Svelte builtin %q was not suppressed", rune_)
		}
	}

	var handlerBound bool
	for _, e := range res.Edges {
		if e.Meta != nil && e.Meta["via"] == "template_handler" && strings.HasSuffix(e.To, "::handle") {
			handlerBound = true
		}
	}
	if !handlerBound {
		t.Error("on:click={handle} did not produce a template_handler edge to handle")
	}
}
