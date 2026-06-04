package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	sitter "github.com/zzet/gortex/internal/parser/tsitter"
	gotsitter "github.com/zzet/gortex/internal/parser/tsitter/golang"
)

// findGoFuncDecl walks a Go parse tree for the first function_declaration
// whose name matches and returns its node.
func findGoFuncDecl(n *sitter.Node, src []byte, name string) *sitter.Node {
	if n == nil {
		return nil
	}
	if n.Type() == "function_declaration" {
		if nm := n.ChildByFieldName("name"); nm != nil && nm.Content(src) == name {
			return n
		}
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if got := findGoFuncDecl(n.Child(i), src, name); got != nil {
			return got
		}
	}
	return nil
}

func goBody(t *testing.T, tree *sitter.Tree, src []byte, name string) *sitter.Node {
	t.Helper()
	decl := findGoFuncDecl(tree.RootNode(), src, name)
	if decl == nil {
		t.Fatalf("function %q not found", name)
	}
	body := goFuncBody(decl)
	if body == nil {
		t.Fatalf("function %q has no body", name)
	}
	return body
}

func TestComplexityMetrics_GoLoopDepthAndCognitive(t *testing.T) {
	src := []byte(`package p

func flat(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func nested(m [][]int) int {
	s := 0
	for _, row := range m {
		for _, x := range row {
			if x > 0 {
				s += x
			}
		}
	}
	return s
}
`)
	tree, err := parser.ParseFile(src, gotsitter.GetLanguage())
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()

	flatBody := goBody(t, tree, src, "flat")
	nestedBody := goBody(t, tree, src, "nested")

	if d := MaxLoopDepth(flatBody, goLoopTypes, goComplexitySkip); d != 1 {
		t.Errorf("flat loop depth = %d, want 1", d)
	}
	if d := MaxLoopDepth(nestedBody, goLoopTypes, goComplexitySkip); d != 2 {
		t.Errorf("nested loop depth = %d, want 2", d)
	}

	flatCog := CognitiveComplexity(flatBody, goComplexityNodes, goNestingTypes, goComplexitySkip)
	nestedCog := CognitiveComplexity(nestedBody, goComplexityNodes, goNestingTypes, goComplexitySkip)
	if nestedCog <= flatCog {
		t.Errorf("nested cognitive (%d) should exceed flat cognitive (%d)", nestedCog, flatCog)
	}
	if flatCog < 1 {
		t.Errorf("flat cognitive = %d, want >= 1", flatCog)
	}
}

func TestStampFunctionMetrics_Go(t *testing.T) {
	src := []byte(`package p

func busy(m [][]int) int {
	s := 0
	for _, row := range m {
		for _, x := range row {
			if x > 0 && x < 100 {
				s += x
			}
		}
	}
	return s
}
`)
	tree, err := parser.ParseFile(src, gotsitter.GetLanguage())
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Close()
	n := &graph.Node{ID: "p.go::busy", Kind: graph.KindFunction, Name: "busy"}
	StampFunctionMetrics(n, goBody(t, tree, src, "busy"), "go")
	if got, _ := n.Meta["loop_depth"].(int); got != 2 {
		t.Errorf("loop_depth = %v, want 2", n.Meta["loop_depth"])
	}
	if got, _ := n.Meta["cognitive"].(int); got < 2 {
		t.Errorf("cognitive = %v, want >= 2", n.Meta["cognitive"])
	}
}
