package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestJavaScriptExtractor_ClassArrowField(t *testing.T) {
	const js = `class Counter {
  count = 0;
  handleClick = () => {
    this.count++;
  };
  render() { return null; }
}
`
	res, err := NewJavaScriptExtractor().Extract("Counter.js", []byte(js))
	if err != nil {
		t.Fatal(err)
	}
	var handle *graph.Node
	for _, n := range res.Nodes {
		if n.Name == "handleClick" {
			handle = n
		}
	}
	if handle == nil {
		t.Fatal("arrow-valued class field 'handleClick' was not extracted")
	}
	if handle.Kind != graph.KindMethod {
		t.Errorf("handleClick should be a callable method, got %s", handle.Kind)
	}
	if handle.ID != "Counter.js::Counter.handleClick" {
		t.Errorf("handleClick id = %q, want Counter.js::Counter.handleClick", handle.ID)
	}
	var memberEdge bool
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeMemberOf && e.From == handle.ID && e.To == "Counter.js::Counter" {
			memberEdge = true
		}
	}
	if !memberEdge {
		t.Errorf("handleClick should be member_of Counter")
	}
}
