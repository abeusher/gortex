package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func thunkNode(g *graph.Graph, id, file, name string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindVariable, Name: name, FilePath: file, Meta: map[string]any{"redux_thunk": name}})
}

func sliceAction(g *graph.Graph, id, file, binding, member string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: binding + "." + member, FilePath: file, Meta: map[string]any{"store_factory": binding, "store_member": member}})
}

func thunkDispatch(g *graph.Graph, fromID, file, callee string) {
	g.AddEdge(&graph.Edge{From: fromID, To: "unresolved::*." + callee, Kind: graph.EdgeCalls, FilePath: file, Meta: map[string]any{"via": reduxThunkVia, "thunk_dispatch": callee}})
}

func synthThunkEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthReduxThunk {
			return e
		}
	}
	return nil
}

func TestResolveReduxThunkCalls_ThunkToStoreAction(t *testing.T) {
	g := graph.New()
	thunkNode(g, "store.js::fetchUser", "store.js", "fetchUser")
	sliceAction(g, "store.js::userSlice.setLoading@5", "store.js", "userSlice", "setLoading")
	sliceAction(g, "store.js::userSlice.set@6", "store.js", "userSlice", "set")
	thunkDispatch(g, "store.js::fetchUser", "store.js", "setLoading")
	thunkDispatch(g, "store.js::fetchUser", "store.js", "set")

	n := ResolveReduxThunkCalls(g)
	require.Equal(t, 2, n)

	e := synthThunkEdge(g, "store.js::fetchUser", "store.js::userSlice.setLoading@5")
	require.NotNil(t, e, "thunk should reach the dispatched slice reducer")
	assert.Equal(t, ConfidenceHeuristic, e.Confidence)
	assert.Equal(t, ProvenanceHeuristic, e.Meta[MetaProvenance])
	assert.NotNil(t, synthThunkEdge(g, "store.js::fetchUser", "store.js::userSlice.set@6"))
}

func TestResolveReduxThunkCalls_ThunkToThunkChain(t *testing.T) {
	g := graph.New()
	thunkNode(g, "t.js::outer", "t.js", "outer")
	thunkNode(g, "t.js::inner", "t.js", "inner")
	thunkDispatch(g, "t.js::outer", "t.js", "inner")

	require.Equal(t, 1, ResolveReduxThunkCalls(g))
	assert.NotNil(t, synthThunkEdge(g, "t.js::outer", "t.js::inner"), "thunk → thunk chain resolves")
}

func TestResolveReduxThunkCalls_CollisionPrefersSameFile(t *testing.T) {
	g := graph.New()
	thunkNode(g, "a.js::fetchA", "a.js", "fetchA")
	// Two setLoading actions, in different files.
	sliceAction(g, "a.js::sliceA.setLoading@2", "a.js", "sliceA", "setLoading")
	sliceAction(g, "b.js::sliceB.setLoading@2", "b.js", "sliceB", "setLoading")
	thunkDispatch(g, "a.js::fetchA", "a.js", "setLoading")

	ResolveReduxThunkCalls(g)
	assert.NotNil(t, synthThunkEdge(g, "a.js::fetchA", "a.js::sliceA.setLoading@2"), "prefers the same-file action")
	assert.Nil(t, synthThunkEdge(g, "a.js::fetchA", "b.js::sliceB.setLoading@2"), "must not bind across files when ambiguous")
}

func TestResolveReduxThunkCalls_UnresolvedStaysPlaceholder(t *testing.T) {
	g := graph.New()
	thunkNode(g, "t.js::outer", "t.js", "outer")
	thunkDispatch(g, "t.js::outer", "t.js", "ghost")

	assert.Equal(t, 0, ResolveReduxThunkCalls(g))
	// No action/thunk named ghost: the edge is not stamped as synthesized.
	assert.Nil(t, synthThunkEdge(g, "t.js::outer", "unresolved::*.ghost"))
}

func TestResolveReduxThunkCalls_NoThunksNoOp(t *testing.T) {
	g := graph.New()
	sliceAction(g, "s.js::sliceA.x@1", "s.js", "sliceA", "x")
	assert.Equal(t, 0, ResolveReduxThunkCalls(g))
}
