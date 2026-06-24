package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/zzet/gortex/internal/graph"
)

func razorComponent(g graph.Store, id, name, ns string) {
	g.AddNode(&graph.Node{
		ID: id, Kind: graph.KindType, Name: name, Language: "razor",
		Meta: map[string]any{"component": true, "scope_ns": ns},
	})
}

func razorUsing(g graph.Store, from, ns string) {
	g.AddEdge(&graph.Edge{From: from, To: "unresolved::razor_using::" + ns, Kind: graph.EdgeImports})
}

func razorTypeRef(g graph.Store, fromFile, name string) *graph.Edge {
	e := &graph.Edge{
		From: fromFile + "::Page", To: "unresolved::" + name,
		Kind: graph.EdgeReferences, FilePath: fromFile,
	}
	g.AddEdge(e)
	return e
}

// TestResolveRazorUsings_Cascade pins the _Imports.razor cascade: a `<Counter/>`
// in Components/Page.razor binds to the Counter component via the @using in
// Components/_Imports.razor.
func TestResolveRazorUsings_Cascade(t *testing.T) {
	g := graph.New()
	seedFile(g, "Components/_Imports.razor", "razor")
	seedFile(g, "Components/Page.razor", "razor")
	seedFile(g, "App/Widgets/Counter.razor", "razor")

	razorUsing(g, "Components/_Imports.razor", "App.Widgets")
	razorComponent(g, "Components/Page.razor::Page", "Page", "Components")
	razorComponent(g, "App/Widgets/Counter.razor::Counter", "Counter", "App.Widgets")
	ref := razorTypeRef(g, "Components/Page.razor", "Counter")

	r := New(g)
	r.resolveRazorUsings()

	assert.Equal(t, "App/Widgets/Counter.razor::Counter", ref.To)
	assert.Equal(t, graph.OriginASTResolved, ref.Origin)
	assert.Equal(t, "razor_using", ref.Meta["resolved_via"])
}

// TestResolveRazorUsings_DirectUsing pins a direct @using on the file (no
// cascade) binding the reference.
func TestResolveRazorUsings_DirectUsing(t *testing.T) {
	g := graph.New()
	seedFile(g, "Pages/Home.razor", "razor")
	seedFile(g, "Lib/Widget.razor", "razor")

	razorUsing(g, "Pages/Home.razor", "Lib")
	razorComponent(g, "Pages/Home.razor::Page", "Home", "Pages")
	razorComponent(g, "Lib/Widget.razor::Widget", "Widget", "Lib")
	ref := razorTypeRef(g, "Pages/Home.razor", "Widget")

	r := New(g)
	r.resolveRazorUsings()

	assert.Equal(t, "Lib/Widget.razor::Widget", ref.To, "direct @using binds without a cascade")
}

// TestResolveRazorUsings_AmbiguousStaysUnresolved pins that a type matching two
// imported namespaces is refused.
func TestResolveRazorUsings_AmbiguousStaysUnresolved(t *testing.T) {
	g := graph.New()
	seedFile(g, "Components/_Imports.razor", "razor")
	seedFile(g, "Components/Page.razor", "razor")
	seedFile(g, "A/Counter.razor", "razor")
	seedFile(g, "B/Counter.razor", "razor")

	razorUsing(g, "Components/_Imports.razor", "A")
	razorUsing(g, "Components/_Imports.razor", "B")
	razorComponent(g, "Components/Page.razor::Page", "Page", "Components")
	razorComponent(g, "A/Counter.razor::Counter", "Counter", "A")
	razorComponent(g, "B/Counter.razor::Counter", "Counter", "B")
	ref := razorTypeRef(g, "Components/Page.razor", "Counter")

	r := New(g)
	r.resolveRazorUsings()

	assert.Equal(t, "unresolved::Counter", ref.To, "ambiguous across two imported namespaces stays unresolved")
}

// TestResolveRazorUsings_NamespaceNotImported pins that a type whose namespace
// is not in the effective set stays unresolved.
func TestResolveRazorUsings_NamespaceNotImported(t *testing.T) {
	g := graph.New()
	seedFile(g, "Components/Page.razor", "razor")
	seedFile(g, "Other/Counter.razor", "razor")

	// Page imports App.Widgets, but Counter lives in Other.
	razorUsing(g, "Components/Page.razor", "App.Widgets")
	razorComponent(g, "Components/Page.razor::Page", "Page", "Components")
	razorComponent(g, "Other/Counter.razor::Counter", "Counter", "Other")
	ref := razorTypeRef(g, "Components/Page.razor", "Counter")

	r := New(g)
	r.resolveRazorUsings()

	assert.Equal(t, "unresolved::Counter", ref.To, "type in a non-imported namespace stays unresolved")
}
