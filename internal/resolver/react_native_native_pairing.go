package resolver

import (
	"sort"

	"github.com/zzet/gortex/internal/graph"
)

// rnNativePairVia marks a synthesized iOS↔Android native-implementation pairing
// edge.
const rnNativePairVia = "rn.native.pair"

// ResolveReactNativeNativePairing is the framework-dispatch synthesizer that
// links the two native implementations of a classic React Native module. A
// module is implemented twice — once on iOS (Objective-C / Swift,
// RCT_EXPORT_METHOD) and once on Android (Java / Kotlin, @ReactMethod) — and
// both extractors stamp the same rn_module + rn_method on their method nodes.
// For each (module, method) implemented on both platforms this pass
// synthesizes a pair of EdgeReferences edges (one each way) between the iOS and
// Android method, so navigating from one platform's implementation surfaces the
// other — the parallel-implementation relationship the static call graph can't
// see because the two sides never call each other.
//
// Only cross-platform pairs are linked (iOS↔Android); two implementations on
// the same platform are not paired. Full recompute and idempotent: edges are
// re-derived from the rn metadata, graph.AddEdge dedupes by key, and
// graph.EvictFile drops the pairing when either side's file is reindexed. Edges
// ride at ast_inferred and carry synthesizer provenance.
//
// Returns the number of cross-platform method pairs linked.
func ResolveReactNativeNativePairing(g graph.Store) int {
	if g == nil {
		return 0
	}

	type modKey struct{ module, method string }
	nativeByKey := map[modKey][]*graph.Node{}
	for _, n := range nodesByKindsOrAll(g, graph.KindMethod) {
		if n == nil || n.Meta == nil {
			continue
		}
		mod, _ := n.Meta["rn_module"].(string)
		meth, _ := n.Meta["rn_method"].(string)
		if mod == "" || meth == "" {
			continue
		}
		nativeByKey[modKey{mod, meth}] = append(nativeByKey[modKey{mod, meth}], n)
	}
	if len(nativeByKey) == 0 {
		return 0
	}

	keys := make([]modKey, 0, len(nativeByKey))
	for k := range nativeByKey {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].module != keys[j].module {
			return keys[i].module < keys[j].module
		}
		return keys[i].method < keys[j].method
	})

	var batch []*graph.Edge
	paired := 0
	for _, k := range keys {
		impls := nativeByKey[k]
		for i := 0; i < len(impls); i++ {
			for j := i + 1; j < len(impls); j++ {
				a, b := impls[i], impls[j]
				if a.ID == "" || b.ID == "" || a.ID == b.ID {
					continue
				}
				if !rnDifferentPlatform(a.Language, b.Language) {
					continue
				}
				batch = append(batch,
					rnNativePairEdge(a, b, k.module, k.method),
					rnNativePairEdge(b, a, k.module, k.method),
				)
				paired++
			}
		}
	}

	for _, e := range batch {
		g.AddEdge(e)
	}
	return paired
}

// rnNativePairEdge builds one direction of the cross-platform pairing.
func rnNativePairEdge(from, to *graph.Node, module, method string) *graph.Edge {
	return &graph.Edge{
		From:            from.ID,
		To:              to.ID,
		Kind:            graph.EdgeReferences,
		FilePath:        from.FilePath,
		Line:            from.StartLine,
		Confidence:      0.6,
		ConfidenceLabel: graph.ConfidenceLabelFor(graph.EdgeReferences, 0.6),
		Origin:          graph.OriginASTInferred,
		Meta: map[string]any{
			"via":             rnNativePairVia,
			"rn_module":       module,
			"rn_method":       method,
			"native_platform": rnPlatform(to.Language),
			MetaSynthesizedBy: SynthReactNativePair,
			MetaProvenance:    ProvenanceHeuristic,
		},
	}
}

// rnPlatform maps a native language to the React Native platform it
// implements: ios (Objective-C / Swift) or android (Java / Kotlin); "" for
// anything else.
func rnPlatform(lang string) string {
	switch lang {
	case "objc", "swift":
		return "ios"
	case "java", "kotlin":
		return "android"
	}
	return ""
}

// rnDifferentPlatform reports whether two native languages implement opposite
// React Native platforms, so only iOS↔Android pairs are linked.
func rnDifferentPlatform(a, b string) bool {
	pa, pb := rnPlatform(a), rnPlatform(b)
	return pa != "" && pb != "" && pa != pb
}
