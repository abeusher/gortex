package languages

import (
	"regexp"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// svelteStoreRefRe matches a Svelte `$store` auto-subscription read in markup
// (`$count`, `{$count}`), capturing the store name. The `$` prefix on an
// imported store is Svelte's auto-subscribe sugar.
var svelteStoreRefRe = regexp.MustCompile(`\$(\w+)`)

// mineSvelteStoreSubscriptions resolves Svelte `$store` auto-subscriptions: a
// `$foo` read in markup where `foo` is an imported store binds the component to
// that store import (its subscribe contract) instead of dangling. Svelte 5 runes
// ($state/$derived/...) are excluded -- they are framework macros, not stores.
// Reuses the import edges the delegated script extractor already emitted.
func mineSvelteStoreSubscriptions(src []byte, filePath, componentID string, result *parser.ExtractionResult) {
	importedStores := map[string]string{}
	for _, e := range result.Edges {
		if e == nil || e.Kind != graph.EdgeImports || !graph.IsUnresolvedTarget(e.To) {
			continue
		}
		name := graph.UnresolvedName(e.To)
		i := strings.LastIndex(name, "::")
		if i < 0 {
			continue // a module-level import, not a named binding
		}
		if leaf := name[i+2:]; leaf != "" {
			importedStores[leaf] = e.To
		}
	}
	if len(importedStores) == 0 {
		return
	}
	runes := frameworkSuppressionSets["svelte"]
	tmpl := blankTemplateRegions(src, "svelte")
	seen := map[string]bool{}
	for _, m := range svelteStoreRefRe.FindAllSubmatchIndex(tmpl, -1) {
		name := string(tmpl[m[2]:m[3]])
		if name == "" || seen[name] || runes["$"+name] {
			continue
		}
		target, ok := importedStores[name]
		if !ok {
			continue
		}
		seen[name] = true
		result.Edges = append(result.Edges, &graph.Edge{
			From: componentID, To: target, Kind: graph.EdgeReferences,
			FilePath: filePath, Line: 1 + strings.Count(string(tmpl[:m[0]]), "\n"),
			Origin: graph.OriginASTInferred,
			Meta:   map[string]any{"via": "svelte_store", "ref_context": "store_subscription", "store": name},
		})
	}
}
