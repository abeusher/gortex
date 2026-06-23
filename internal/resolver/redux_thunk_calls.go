package resolver

import (
	"github.com/zzet/gortex/internal/graph"
)

// reduxThunkVia is the Meta["via"] tag the JS/TS extractors stamp on a
// createAsyncThunk dispatch placeholder — a `dispatch(action())` inside a
// thunk's payload-creator body that the static call graph cannot see
// because the thunk is registered, not directly called.
const reduxThunkVia = "redux-thunk"

// ResolveReduxThunkCalls binds Redux Toolkit thunk dispatch chains:
// `const fetchUser = createAsyncThunk(type, (arg, {dispatch}) => {
// dispatch(setLoading()); dispatch(slice.actions.set(...)) })` links
// fetchUser → setLoading and fetchUser → set.
//
// The extractor tags the thunk node with Meta["redux_thunk"]=<name> and
// stamps each inner dispatch as a placeholder EdgeCalls from the thunk to
// `unresolved::*.<callee>` with Meta["via"]="redux-thunk" +
// Meta["thunk_dispatch"]=<callee>. This pass resolves each callee against
// (1) a same-file thunk, (2) a unique thunk match, then (3) a store-factory
// action node (cross-linking the store-factory pass — so a thunk
// dispatching a createSlice reducer reaches the reducer). Register after
// SynthStoreFactory so the store-action nodes are present.
//
// Returns the number of dispatch placeholders landed on a real target.
func ResolveReduxThunkCalls(g graph.Store) int {
	if g == nil {
		return 0
	}
	// index: thunk nodes by name, store-factory action nodes by member.
	thunkByName := map[string][]*graph.Node{}
	actionByMember := map[string][]*graph.Node{}
	for _, n := range nodesByKindsOrAll(g, graph.KindFunction, graph.KindMethod, graph.KindVariable, graph.KindConstant) {
		if n == nil || n.Meta == nil {
			continue
		}
		if t, _ := n.Meta["redux_thunk"].(string); t != "" {
			thunkByName[t] = append(thunkByName[t], n)
		}
		if sf, _ := n.Meta["store_factory"].(string); sf != "" {
			member, _ := n.Meta["store_member"].(string)
			if member == "" {
				member = n.Name
			}
			actionByMember[member] = append(actionByMember[member], n)
		}
	}
	if len(thunkByName) == 0 {
		return 0
	}

	resolved := 0
	var reindex []graph.EdgeReindex
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != reduxThunkVia {
			continue
		}
		callee, _ := e.Meta["thunk_dispatch"].(string)
		if callee == "" {
			continue
		}
		target := pickReduxThunkTarget(g, e, callee, thunkByName, actionByMember)

		want := "unresolved::*." + callee
		if target != nil {
			want = target.ID
		}
		if e.To == want {
			if target != nil {
				resolved++
			}
			continue
		}
		oldTo := e.To
		e.To = want
		if target != nil {
			e.Origin = graph.OriginASTInferred
			e.Confidence = ConfidenceHeuristic
			e.ConfidenceLabel = graph.ConfidenceLabelFor(graph.EdgeCalls, ConfidenceHeuristic)
			StampSynthesized(e, SynthReduxThunk)
			resolved++
		} else {
			e.Origin = graph.OriginASTInferred
			e.Confidence = 0
			e.ConfidenceLabel = ""
			UnstampSynthesized(e)
		}
		reindex = append(reindex, graph.EdgeReindex{Edge: e, OldTo: oldTo})
	}
	if len(reindex) > 0 {
		g.ReindexEdges(reindex)
	}
	return resolved
}

// pickReduxThunkTarget resolves a dispatched callee. It prefers a thunk
// of that name (a thunk→thunk chain) — same-file first, then a unique
// match — then falls back to a store-factory action of that name, with
// the same same-file-then-unique disambiguation. Returns nil when the
// choice is ambiguous (never guesses).
func pickReduxThunkTarget(g graph.Store, call *graph.Edge, callee string, thunks, actions map[string][]*graph.Node) *graph.Node {
	if t := pickStoreAction(g, call, sameBoundaryCandidates(g, call.From, thunks[callee])); t != nil {
		return t
	}
	return pickStoreAction(g, call, sameBoundaryCandidates(g, call.From, actions[callee]))
}
