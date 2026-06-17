package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func rnPairEdge(g graph.Store, from, to string) *graph.Edge {
	for _, e := range g.GetOutEdges(from) {
		if e.To == to && e.Kind == graph.EdgeReferences && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == rnNativePairVia {
				return e
			}
		}
	}
	return nil
}

func TestResolveReactNativeNativePairing_PairsIOSAndAndroid(t *testing.T) {
	g := graph.New()
	rnNativeMethod(g, "ios/Battery.m::getLevel", "objc", "Battery", "getLevel")
	rnNativeMethod(g, "android/Battery.java::getLevel", "java", "Battery", "getLevel")

	assert.Equal(t, 1, ResolveReactNativeNativePairing(g))

	fwd := rnPairEdge(g, "ios/Battery.m::getLevel", "android/Battery.java::getLevel")
	require.NotNil(t, fwd, "ios→android pairing edge")
	assert.Equal(t, "Battery", fwd.Meta["rn_module"])
	assert.Equal(t, "getLevel", fwd.Meta["rn_method"])
	assert.Equal(t, "android", fwd.Meta["native_platform"])
	assert.Equal(t, SynthReactNativePair, fwd.Meta[MetaSynthesizedBy])
	assert.Equal(t, graph.OriginASTInferred, fwd.Origin)

	rev := rnPairEdge(g, "android/Battery.java::getLevel", "ios/Battery.m::getLevel")
	require.NotNil(t, rev, "android→ios pairing edge (bidirectional)")
	assert.Equal(t, "ios", rev.Meta["native_platform"])
}

func TestResolveReactNativeNativePairing_SwiftKotlinPaired(t *testing.T) {
	g := graph.New()
	rnNativeMethod(g, "ios/Battery.swift::getLevel", "swift", "Battery", "getLevel")
	rnNativeMethod(g, "android/Battery.kt::getLevel", "kotlin", "Battery", "getLevel")

	assert.Equal(t, 1, ResolveReactNativeNativePairing(g))
	assert.NotNil(t, rnPairEdge(g, "ios/Battery.swift::getLevel", "android/Battery.kt::getLevel"))
}

func TestResolveReactNativeNativePairing_NoPairWithinSamePlatform(t *testing.T) {
	g := graph.New()
	// Two iOS implementations of the same method must not pair with each other.
	rnNativeMethod(g, "ios/Battery.m::getLevel", "objc", "Battery", "getLevel")
	rnNativeMethod(g, "ios/Battery2.m::getLevel", "objc", "Battery", "getLevel")

	assert.Equal(t, 0, ResolveReactNativeNativePairing(g))
	assert.Nil(t, rnPairEdge(g, "ios/Battery.m::getLevel", "ios/Battery2.m::getLevel"))
}

func TestResolveReactNativeNativePairing_NoMetaNoPair(t *testing.T) {
	g := graph.New()
	rnNativeMethod(g, "ios/Battery.m::getLevel", "objc", "Battery", "getLevel")
	// Android side missing rn metadata — nothing to pair against.
	g.AddNode(&graph.Node{ID: "android/Other.java::getLevel", Kind: graph.KindMethod, Name: "getLevel", Language: "java"})
	assert.Equal(t, 0, ResolveReactNativeNativePairing(g))
}

func TestResolveReactNativeNativePairing_Idempotent(t *testing.T) {
	g := graph.New()
	rnNativeMethod(g, "ios/Battery.m::getLevel", "objc", "Battery", "getLevel")
	rnNativeMethod(g, "android/Battery.java::getLevel", "java", "Battery", "getLevel")

	first := ResolveReactNativeNativePairing(g)
	second := ResolveReactNativeNativePairing(g)
	assert.Equal(t, first, second)

	count := 0
	for e := range g.EdgesByKind(graph.EdgeReferences) {
		if e != nil && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == rnNativePairVia {
				count++
			}
		}
	}
	assert.Equal(t, 2, count, "exactly two pairing edges (one each direction) survive dedup")
}
