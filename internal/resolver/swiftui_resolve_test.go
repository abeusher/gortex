package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func swiftRefEdge(g *graph.Graph, from, file, to string, kind graph.EdgeKind) {
	g.AddEdge(&graph.Edge{From: from, To: to, Kind: kind, FilePath: file})
}

func synthSwiftUIEdge(g graph.Store, kind graph.EdgeKind, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(kind) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthSwiftUIResolve {
			return e
		}
	}
	return nil
}

func TestResolveSwiftUIRefs_ViewModelAndModel(t *testing.T) {
	g := graph.New()
	const view = "App/Views/ProfileView.swift::ProfileView"
	g.AddNode(&graph.Node{ID: view, Kind: graph.KindType, Name: "ProfileView", FilePath: "App/Views/ProfileView.swift", Language: "swift"})

	// ViewModel under /ViewModels/, model under /Models/.
	convNode(g, "App/ViewModels/ProfileViewModel.swift::ProfileViewModel", "App/ViewModels/ProfileViewModel.swift", "ProfileViewModel")
	convNode(g, "App/Models/User.swift::User", "App/Models/User.swift", "User")

	swiftRefEdge(g, view, "App/Views/ProfileView.swift", "unresolved::ProfileViewModel", graph.EdgeInstantiates)
	swiftRefEdge(g, view, "App/Views/ProfileView.swift", "unresolved::User", graph.EdgeReferences)

	require.Equal(t, 2, ResolveSwiftUIRefs(g))
	assert.NotNil(t, synthSwiftUIEdge(g, graph.EdgeInstantiates, view, "App/ViewModels/ProfileViewModel.swift::ProfileViewModel"),
		"*ViewModel binds to /ViewModels/")
	assert.NotNil(t, synthSwiftUIEdge(g, graph.EdgeReferences, view, "App/Models/User.swift::User"),
		"PascalCase model binds to /Models/")
}

func TestResolveSwiftUIRefs_BareModelNotInModelsDirSkipped(t *testing.T) {
	g := graph.New()
	const view = "App/Views/V.swift::V"
	g.AddNode(&graph.Node{ID: view, Kind: graph.KindType, Name: "V", FilePath: "App/Views/V.swift", Language: "swift"})
	// `Button` resolves to a type, but it's not under /Models/ — must be skipped
	// (a SwiftUI built-in used in the view must not bind to an unrelated type).
	convNode(g, "App/Widgets/Button.swift::Button", "App/Widgets/Button.swift", "Button")
	swiftRefEdge(g, view, "App/Views/V.swift", "unresolved::Button", graph.EdgeInstantiates)

	require.Equal(t, 0, ResolveSwiftUIRefs(g))
	assert.Nil(t, synthSwiftUIEdge(g, graph.EdgeInstantiates, view, "App/Widgets/Button.swift::Button"))
}

func TestResolveSwiftUIRefs_AmbiguousViewModelLeftAlone(t *testing.T) {
	g := graph.New()
	const view = "App/Views/V.swift::V"
	g.AddNode(&graph.Node{ID: view, Kind: graph.KindType, Name: "V", FilePath: "App/Views/V.swift", Language: "swift"})
	convNode(g, "a/HomeViewModel.swift::HomeViewModel", "a/HomeViewModel.swift", "HomeViewModel")
	convNode(g, "b/HomeViewModel.swift::HomeViewModel", "b/HomeViewModel.swift", "HomeViewModel")
	swiftRefEdge(g, view, "App/Views/V.swift", "unresolved::HomeViewModel", graph.EdgeInstantiates)

	require.Equal(t, 0, ResolveSwiftUIRefs(g))
}

func TestResolveSwiftUIRefs_NonSwiftLeftAlone(t *testing.T) {
	g := graph.New()
	const goFn = "pkg/svc.go::Use"
	g.AddNode(&graph.Node{ID: goFn, Kind: graph.KindFunction, Name: "Use", FilePath: "pkg/svc.go", Language: "go"})
	convNode(g, "App/ViewModels/ProfileViewModel.swift::ProfileViewModel", "App/ViewModels/ProfileViewModel.swift", "ProfileViewModel")
	swiftRefEdge(g, goFn, "pkg/svc.go", "unresolved::ProfileViewModel", graph.EdgeInstantiates)

	require.Equal(t, 0, ResolveSwiftUIRefs(g))
}
