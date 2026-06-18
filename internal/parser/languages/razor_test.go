package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestRazorExtractor(t *testing.T) {
	const razor = `@page "/counter"
@inherits ComponentBase
@inject IWeatherService Weather

<h1>Counter</h1>
<button @onclick="Increment">Click</button>

@code {
    private int count = 0;
    private void Increment()
    {
        count++;
    }
}
`
	res, err := NewRazorExtractor().Extract("Counter.razor", []byte(razor))
	if err != nil {
		t.Fatal(err)
	}

	var incr *graph.Node
	refs := map[string]bool{}
	for _, n := range res.Nodes {
		if n.Name == "Increment" {
			incr = n
		}
		if n.Name == "__RazorCode" {
			t.Errorf("synthetic wrapper class leaked into the graph: %+v", n)
		}
	}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && strings.HasPrefix(e.To, "unresolved::") {
			refs[strings.TrimPrefix(e.To, "unresolved::")] = true
		}
	}

	// The @code block's C# member is extracted, rebased into host coordinates.
	if incr == nil {
		t.Fatalf("@code method 'Increment' was not extracted from the Razor file")
	}
	if incr.Language != "razor" || incr.Meta["inline_script"] != true {
		t.Errorf("delegated symbol lang=%q meta=%v, want razor + inline_script", incr.Language, incr.Meta)
	}
	if incr.StartLine != 10 {
		t.Errorf("Increment StartLine = %d, want 10 (host-file coordinates)", incr.StartLine)
	}

	// @inherits and @inject directives become type references.
	for _, want := range []string{"ComponentBase", "IWeatherService"} {
		if !refs[want] {
			t.Errorf("missing directive type reference %q (refs: %v)", want, refs)
		}
	}
}

// TestRazorBraceMatcherSkipsStringsAndCarvesBareBlock is the B4 named test: a
// `}` inside a C# string or comment in a @code block must not truncate the
// block (which dropped every member after it), and a bare @{ } block is also
// carved and delegated. The per-file Blazor component node is emitted.
func TestRazorBraceMatcherSkipsStringsAndCarvesBareBlock(t *testing.T) {
	src := []byte("<h1>Hi</h1>\n" +
		"@code {\n" +
		"    string Brace() { return \"}\"; }\n" +
		"    void After() { }\n" +
		"    // a comment with a } brace\n" +
		"    void Last() { }\n" +
		"}\n" +
		"@{\n" +
		"    void BareHelper() { }\n" +
		"}\n")
	res, err := NewRazorExtractor().Extract("Counter.razor", src)
	if err != nil {
		t.Fatal(err)
	}

	methods := map[string]bool{}
	for _, n := range res.Nodes {
		if n.Kind == graph.KindMethod || n.Kind == graph.KindFunction {
			methods[n.Name] = true
		}
	}
	// All three @code members survive — the brace inside the string / comment
	// no longer truncates the block.
	for _, want := range []string{"Brace", "After", "Last"} {
		if !methods[want] {
			t.Fatalf("method %s was dropped by brace truncation; got %v", want, methods)
		}
	}
	// The synthetic wrapper method/class are not leaked as symbols.
	if methods["__Body"] {
		t.Fatalf("synthetic __Body wrapper leaked as a method")
	}

	// The bare @{ } block is carved (two spans total).
	spans := razorCodeSpans(src)
	var bareSeen bool
	for _, s := range spans {
		if s.bare {
			bareSeen = true
		}
	}
	if !bareSeen {
		t.Fatalf("bare @{ } block was not carved; spans=%v", spans)
	}

	// The per-file component node exists and is navigable.
	var component bool
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType && n.ID == "Counter.razor::Counter" {
			if c, _ := n.Meta["component"].(bool); c {
				component = true
			}
		}
	}
	if !component {
		t.Fatalf("expected a per-file component node Counter.razor::Counter")
	}
}

// TestRazorMatchBraceUnit checks the matcher directly on literals and comments.
func TestRazorMatchBraceUnit(t *testing.T) {
	// open at index 0; the } inside the string must be ignored.
	src := []byte(`{ var s = "a}b"; var c = '}'; /* } */ }`)
	end := matchRazorBrace(src, 0)
	if end != len(src)-1 {
		t.Fatalf("matchRazorBrace = %d, want %d (final brace)", end, len(src)-1)
	}
}
