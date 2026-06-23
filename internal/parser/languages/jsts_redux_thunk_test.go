package languages

import (
	"fmt"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func reduxThunkDispatches(fix *extractedFixture, from string) map[string]*graph.Edge {
	out := map[string]*graph.Edge{}
	for _, e := range fix.edgesByKind[graph.EdgeCalls] {
		if e.From != from {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "redux-thunk" {
			continue
		}
		td, _ := e.Meta["thunk_dispatch"].(string)
		out[td] = e
	}
	return out
}

func TestReduxThunk_TagsThunkAndStampsDispatches(t *testing.T) {
	src := `import { createSlice, createAsyncThunk } from '@reduxjs/toolkit';

const userSlice = createSlice({
  name: 'user',
  reducers: {
    setLoading(state) {},
    set(state, action) {},
  },
});

const fetchUser = createAsyncThunk('user/fetch', async (id, { dispatch }) => {
  dispatch(setLoading());
  dispatch(userSlice.actions.set(id));
});
`
	fix := runJSExtractFixture(t, "store.js", src)

	thunk := fix.nodesByID["store.js::fetchUser"]
	if thunk == nil {
		t.Fatalf("no fetchUser node")
	}
	if got, _ := thunk.Meta["redux_thunk"].(string); got != "fetchUser" {
		t.Errorf("redux_thunk tag = %q (want fetchUser)", got)
	}

	disp := reduxThunkDispatches(fix, "store.js::fetchUser")
	if len(disp) != 2 {
		t.Fatalf("want 2 redux-thunk dispatch placeholders, got %d (%v)", len(disp), disp)
	}
	for _, callee := range []string{"setLoading", "set"} {
		e := disp[callee]
		if e == nil {
			t.Fatalf("missing dispatch placeholder for %q", callee)
		}
		if e.To != "unresolved::*."+callee {
			t.Errorf("placeholder To = %q (want unresolved::*.%s)", e.To, callee)
		}
	}
}

func TestReduxThunk_DispatchCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("const t = createAsyncThunk('t', async () => {\n")
	for i := range 30 {
		fmt.Fprintf(&b, "  dispatch(a%d());\n", i)
	}
	b.WriteString("});\n")

	fix := runJSExtractFixture(t, "t.js", b.String())
	if got := len(reduxThunkDispatches(fix, "t.js::t")); got != reduxThunkDispatchCap {
		t.Errorf("dispatch placeholders = %d (want capped at %d)", got, reduxThunkDispatchCap)
	}
}

func TestReduxThunk_NonThunkConstUntouched(t *testing.T) {
	// A plain const with a dispatch-shaped call must not be tagged.
	src := `const x = compute('id', () => { dispatch(go()); });
`
	fix := runJSExtractFixture(t, "p.js", src)
	if n := fix.nodesByID["p.js::x"]; n != nil {
		if _, tagged := n.Meta["redux_thunk"]; tagged {
			t.Errorf("non-thunk const must not be tagged redux_thunk")
		}
	}
	if got := len(reduxThunkDispatches(fix, "p.js::x")); got != 0 {
		t.Errorf("non-thunk const must produce no dispatch placeholders, got %d", got)
	}
}

func TestTSReduxThunk_Tagged(t *testing.T) {
	src := `import { createAsyncThunk } from '@reduxjs/toolkit';
const load = createAsyncThunk('load', async (_: number, { dispatch }) => {
  dispatch(begin());
});
`
	fix := runTSExtractFixture(t, "t.ts", src)
	thunk := fix.nodesByID["t.ts::load"]
	if thunk == nil {
		t.Fatalf("no load node")
	}
	if got, _ := thunk.Meta["redux_thunk"].(string); got != "load" {
		t.Errorf("redux_thunk tag = %q (want load)", got)
	}
	if got := len(reduxThunkDispatches(fix, "t.ts::load")); got != 1 {
		t.Errorf("want 1 dispatch placeholder, got %d", got)
	}
}
