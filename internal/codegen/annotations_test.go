package codegen

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func annoNode(id, name string) *graph.Node {
	return &graph.Node{ID: id, Kind: graph.KindType, Name: name, Meta: map[string]any{"kind": "annotation"}}
}

func TestMarkAnnotatedGenerated_Lombok(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "User.java::User", Kind: graph.KindType, Name: "User", FilePath: "User.java"},
		annoNode("annotation::java::Data", "Data"),
	}
	edges := []*graph.Edge{
		{From: "User.java::User", To: "annotation::java::Data", Kind: graph.EdgeAnnotated},
	}
	extra, stats := MarkAnnotatedGenerated(nodes, edges)
	require.Equal(t, 1, stats.NodesMarked)
	require.Len(t, extra, 1)
	require.Equal(t, graph.EdgeGeneratedBy, extra[0].Kind)
	require.Equal(t, "external::generator-tool:lombok", extra[0].To)

	host := nodes[0]
	require.Equal(t, true, host.Meta["has_generated_members"])
	require.Equal(t, "lombok", host.Meta["codegen_tool"])
	require.Contains(t, host.Meta["generated_members"].([]string), "getters")
}

func TestMarkAnnotatedGenerated_MapStruct(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "M.java::M", Kind: graph.KindInterface, Name: "M", FilePath: "M.java"},
		annoNode("annotation::java::Mapper", "org.mapstruct.Mapper"),
	}
	edges := []*graph.Edge{{From: "M.java::M", To: "annotation::java::Mapper", Kind: graph.EdgeAnnotated}}
	_, stats := MarkAnnotatedGenerated(nodes, edges)
	require.Equal(t, 1, stats.NodesMarked)
	require.Equal(t, "mapstruct", nodes[0].Meta["codegen_tool"])
}

func TestMarkAnnotatedGenerated_IgnoresNonCodegen(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "C.java::C", Kind: graph.KindType, Name: "C"},
		annoNode("annotation::java::Override", "Override"),
	}
	edges := []*graph.Edge{{From: "C.java::C", To: "annotation::java::Override", Kind: graph.EdgeAnnotated}}
	extra, stats := MarkAnnotatedGenerated(nodes, edges)
	require.Equal(t, 0, stats.NodesMarked)
	require.Empty(t, extra)
	require.Nil(t, nodes[0].Meta)
}

func TestMarkAnnotatedGenerated_MergesAndDedups(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "C::C", Kind: graph.KindType, Name: "C"},
		annoNode("annotation::java::Getter", "Getter"),
		annoNode("annotation::java::Setter", "Setter"),
	}
	edges := []*graph.Edge{
		{From: "C::C", To: "annotation::java::Getter", Kind: graph.EdgeAnnotated},
		{From: "C::C", To: "annotation::java::Setter", Kind: graph.EdgeAnnotated},
	}
	extra, stats := MarkAnnotatedGenerated(nodes, edges)
	require.Equal(t, 2, stats.NodesMarked)
	require.Len(t, extra, 1, "one EdgeGeneratedBy per (symbol, tool)")
	members := nodes[0].Meta["generated_members"].([]string)
	require.Contains(t, members, "getters")
	require.Contains(t, members, "setters")
}

func TestMarkAnnotatedGenerated_MVVMSourceGenerators(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "vm.cs::name", Kind: graph.KindField, Name: "name", FilePath: "vm.cs"},
		annoNode("annotation::csharp::ObservableProperty", "ObservableProperty"),
	}
	edges := []*graph.Edge{
		{From: "vm.cs::name", To: "annotation::csharp::ObservableProperty", Kind: graph.EdgeAnnotated},
	}
	_, stats := MarkAnnotatedGenerated(nodes, edges)
	require.Equal(t, 1, stats.NodesMarked)
	require.Equal(t, "mvvm_toolkit", nodes[0].Meta["codegen_tool"])
	require.Contains(t, nodes[0].Meta["generated_members"].([]string), "observable_property")
}

func TestNormalizeAnnotationName(t *testing.T) {
	require.Equal(t, "Data", normalizeAnnotationName("@Data"))
	require.Equal(t, "Data", normalizeAnnotationName("lombok.Data"))
	require.Equal(t, "Mapper", normalizeAnnotationName(" @org.mapstruct.Mapper "))
}

func TestMaterializeLombokAccessors_GettersSetters(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "User.java::User", Kind: graph.KindType, Name: "User", FilePath: "User.java"},
		annoNode("annotation::java::Data", "Data"),
		{ID: "User.java::User.name", Kind: graph.KindField, Name: "name", FilePath: "User.java", Meta: map[string]any{"receiver": "User", "field_type": "String"}},
		{ID: "User.java::User.active", Kind: graph.KindField, Name: "active", FilePath: "User.java", Meta: map[string]any{"receiver": "User", "field_type": "boolean"}},
	}
	edges := []*graph.Edge{{From: "User.java::User", To: "annotation::java::Data", Kind: graph.EdgeAnnotated}}
	MarkAnnotatedGenerated(nodes, edges)
	newNodes, newEdges := MaterializeLombokAccessors(nodes)

	byID := map[string]*graph.Node{}
	for _, n := range newNodes {
		byID[n.ID] = n
	}
	getName := byID["User.java::User.getName"]
	require.NotNil(t, getName, "getName accessor minted")
	require.Equal(t, graph.KindMethod, getName.Kind)
	require.Equal(t, "getName", getName.Name)
	require.Equal(t, "User", getName.Meta["receiver"])
	require.Equal(t, true, getName.Meta["synthesized"])
	require.Equal(t, true, getName.Meta["generated"])
	require.Equal(t, "lombok", getName.Meta["codegen_tool"])
	require.NotNil(t, byID["User.java::User.setName"], "setName minted")
	require.NotNil(t, byID["User.java::User.isActive"], "boolean getter is-prefixed")
	require.NotNil(t, byID["User.java::User.setActive"], "boolean setter minted")

	var hasDefines, hasGenBy bool
	for _, e := range newEdges {
		if e.Kind == graph.EdgeDefines && e.From == "User.java::User" && e.To == "User.java::User.getName" {
			hasDefines = true
		}
		if e.Kind == graph.EdgeGeneratedBy && e.From == "User.java::User.getName" && e.To == "external::generator-tool:lombok" {
			hasGenBy = true
		}
	}
	require.True(t, hasDefines, "EdgeDefines class→getName")
	require.True(t, hasGenBy, "EdgeGeneratedBy getName→lombok")
}

func TestMaterializeLombokAccessors_NoDuplicateHandWrittenWins(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "U.java::U", Kind: graph.KindType, Name: "U", FilePath: "U.java", Meta: map[string]any{"codegen_tool": "lombok", "generated_members": []string{"getters", "setters"}}},
		{ID: "U.java::U.id", Kind: graph.KindField, Name: "id", FilePath: "U.java", Meta: map[string]any{"receiver": "U", "field_type": "int"}},
		{ID: "U.java::U.getId", Kind: graph.KindMethod, Name: "getId", FilePath: "U.java", Meta: map[string]any{"receiver": "U"}},
	}
	newNodes, _ := MaterializeLombokAccessors(nodes)
	for _, n := range newNodes {
		if n.ID == "U.java::U.getId" {
			t.Errorf("hand-written getId must not be re-minted")
		}
	}
	var sawSetId bool
	for _, n := range newNodes {
		if n.ID == "U.java::U.setId" {
			sawSetId = true
		}
	}
	require.True(t, sawSetId, "setId minted (no hand-written version)")

	all := append(append([]*graph.Node{}, nodes...), newNodes...)
	again, _ := MaterializeLombokAccessors(all)
	require.Empty(t, again, "a reindex / re-run mints no duplicate accessors")
}

func TestMaterializeLombokAccessors_BuilderAndLogger(t *testing.T) {
	nodes := []*graph.Node{
		{ID: "S.java::S", Kind: graph.KindType, Name: "S", FilePath: "S.java", Meta: map[string]any{"codegen_tool": "lombok", "generated_members": []string{"builder", "logger"}}},
	}
	newNodes, _ := MaterializeLombokAccessors(nodes)
	byID := map[string]*graph.Node{}
	for _, n := range newNodes {
		byID[n.ID] = n
	}
	require.NotNil(t, byID["S.java::S.builder"], "builder() minted")
	require.NotNil(t, byID["S.java::SBuilder"], "Builder type minted")
	require.NotNil(t, byID["S.java::S.log"], "log field minted")
	require.Equal(t, "org.slf4j.Logger", byID["S.java::S.log"].Meta["field_type"])
}
