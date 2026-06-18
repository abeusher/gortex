package mcp

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
)

func TestSmartContextDefaultOff(t *testing.T) {
	s := &Server{} // no configManager → empty config
	// No include_* args → every in-pack section off.
	got := s.smartContextSections(map[string]any{}, "")
	if got.Any() {
		t.Errorf("smart_context in-pack sections should default off, got %+v", got)
	}

	// An explicit per-call opt-in turns the section on.
	on := s.smartContextSections(map[string]any{"include_call_paths": true}, "")
	if !on.CallPaths {
		t.Errorf("include_call_paths=true should enable CallPaths, got %+v", on)
	}
	if on.Flows || on.Confidence {
		t.Errorf("only CallPaths should be on, got %+v", on)
	}
}

func TestSmartContextDefaultOff_AttachNoop(t *testing.T) {
	s := &Server{}
	result := map[string]any{"relevant_symbols": []string{}}
	// Default-off sections leave the pack untouched.
	s.attachInPackSections(result, s.smartContextSections(map[string]any{}, ""), nil)
	if _, ok := result["in_pack"]; ok {
		t.Errorf("default-off should not add an in_pack block, got %+v", result["in_pack"])
	}
	// Opting call-paths in with no graph / no reachable paths adds no block.
	s.attachInPackSections(result, config.SmartContextSections{CallPaths: true}, nil)
	if _, ok := result["in_pack"]; ok {
		t.Errorf("opt-in with no reachable paths should add no block, got %+v", result["in_pack"])
	}
}

// smartCtxGraph builds a→b→focus, c→focus.
func smartCtxGraph() *graph.Graph {
	g := graph.New()
	for _, id := range []string{"focus", "a", "b", "c"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: "x.go"})
	}
	g.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "b", To: "focus", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "c", To: "focus", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	return g
}

func TestSmartContextCallPaths(t *testing.T) {
	s := &Server{graph: smartCtxGraph()}
	// focus is the anchor (symbols[0]); a and c are roots.
	symbols := []*graph.Node{{ID: "focus"}, {ID: "a"}, {ID: "c"}}

	result := map[string]any{}
	s.attachInPackSections(result, config.SmartContextSections{CallPaths: true}, symbols)

	blk, ok := result["in_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected in_pack block, got %T", result["in_pack"])
	}
	cps, ok := blk["call_paths"].([]map[string]any)
	if !ok || len(cps) != 2 {
		t.Fatalf("expected 2 call_paths, got %T len=%d", blk["call_paths"], len(cps))
	}
	// Shortest first: c→focus (len 1) before a→b→focus (len 2).
	if cps[0]["root"] != "c" || cps[0]["length"] != 1 {
		t.Errorf("first path = %+v, want c len 1", cps[0])
	}
	if cps[1]["root"] != "a" || cps[1]["length"] != 2 {
		t.Errorf("second path = %+v, want a len 2", cps[1])
	}
	if cps[0]["anchor"] != "focus" {
		t.Errorf("anchor = %v, want focus", cps[0]["anchor"])
	}

	// Default-off leaves the pack untouched even with reachable symbols.
	off := map[string]any{}
	s.attachInPackSections(off, config.SmartContextSections{}, symbols)
	if _, ok := off["in_pack"]; ok {
		t.Errorf("call-paths off should add no block, got %+v", off["in_pack"])
	}
}

func TestGCXSmartContext_CallPaths(t *testing.T) {
	result := map[string]any{
		"relevant_symbols": []map[string]any{},
		"in_pack": map[string]any{
			"call_paths": []map[string]any{
				{"root": "c", "anchor": "focus", "length": 1, "confidence": 0.9, "nodes": []string{"c", "focus"}},
			},
		},
	}
	out, err := encodeSmartContext(result)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "smart_context.call_paths") {
		t.Errorf("GCX output missing call_paths section:\n%s", s)
	}
	if !strings.Contains(s, "c>focus") {
		t.Errorf("GCX output missing joined nodes 'c>focus':\n%s", s)
	}
}

// flowGraph builds a forward chain focus→a→b.
func flowGraph() *graph.Graph {
	g := graph.New()
	for _, id := range []string{"focus", "a", "b"} {
		g.AddNode(&graph.Node{ID: id, Kind: graph.KindFunction, Name: id, FilePath: "x.go"})
	}
	g.AddEdge(&graph.Edge{From: "focus", To: "a", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	g.AddEdge(&graph.Edge{From: "a", To: "b", Kind: graph.EdgeCalls, Origin: graph.OriginASTResolved})
	return g
}

func TestSmartContextFlows(t *testing.T) {
	s := &Server{graph: flowGraph()}
	result := map[string]any{}
	s.attachInPackSections(result, config.SmartContextSections{Flows: true}, []*graph.Node{{ID: "focus"}})

	blk, ok := result["in_pack"].(map[string]any)
	if !ok {
		t.Fatalf("expected in_pack block, got %T", result["in_pack"])
	}
	flows, ok := blk["flows"].(map[string]any)
	if !ok {
		t.Fatalf("expected flows section, got %T", blk["flows"])
	}
	spine, ok := flows["spine"].([]string)
	if !ok || len(spine) != 3 || spine[0] != "focus" || spine[1] != "a" || spine[2] != "b" {
		t.Fatalf("spine = %v, want [focus a b]", flows["spine"])
	}

	// GCX encoding emits a flow_spine section.
	out, err := encodeSmartContext(map[string]any{"relevant_symbols": []map[string]any{}, "in_pack": blk})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "smart_context.flow_spine") || !strings.Contains(string(out), "focus>a>b") {
		t.Errorf("GCX missing flow_spine:\n%s", out)
	}

	// Flows off → no block.
	off := map[string]any{}
	s.attachInPackSections(off, config.SmartContextSections{}, []*graph.Node{{ID: "focus"}})
	if _, ok := off["in_pack"]; ok {
		t.Errorf("flows off should add no block, got %+v", off["in_pack"])
	}
}

func TestSmartContextBoundary(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{ID: "focus", Kind: graph.KindFunction, Name: "focus", FilePath: "x.go"})
	g.AddEdge(&graph.Edge{From: "focus", To: "unresolved::dynamicCall", Kind: graph.EdgeCalls})
	s := &Server{graph: g}

	result := map[string]any{}
	s.attachInPackSections(result, config.SmartContextSections{Flows: true}, []*graph.Node{{ID: "focus"}})

	blk := result["in_pack"].(map[string]any)
	flows := blk["flows"].(map[string]any)
	bs, ok := flows["boundaries"].([]map[string]any)
	if !ok || len(bs) != 1 {
		t.Fatalf("boundaries = %v, want one", flows["boundaries"])
	}
	if bs[0]["from"] != "focus" || bs[0]["target"] != "dynamicCall" || bs[0]["reason"] != "dynamic_dispatch" {
		t.Errorf("boundary = %+v", bs[0])
	}

	out, err := encodeSmartContext(map[string]any{"relevant_symbols": []map[string]any{}, "in_pack": blk})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "smart_context.flow_boundaries") || !strings.Contains(string(out), "dynamicCall") {
		t.Errorf("GCX missing flow_boundaries:\n%s", out)
	}
}
