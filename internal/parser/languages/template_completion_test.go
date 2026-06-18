package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestLiquidSectionSnippetNormalization proves render/include normalize to the
// theme-relative snippets/<name>.liquid file (and section to sections/<name>),
// whitespace-trim tags `{%- -%}` are accepted, and a {% schema %} block is
// recorded as a value-redacted constant (never the raw JSON, which carries
// store secrets). The normalized path is a real cross-file target a bare-name
// import never resolves.
func TestLiquidSectionSnippetNormalization(t *testing.T) {
	src := "{%- render 'product-card' -%}\n" +
		"{% include 'header' %}\n" +
		"{% section 'announcement-bar' %}\n" +
		"{% schema %}\n" +
		"{ \"name\": \"My Section\", \"api_key\": \"sk-secret\" }\n" +
		"{% endschema %}\n"
	res, err := NewLiquidExtractor().Extract("templates/index.liquid", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	imports := map[string]bool{}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeImports {
			imports[strings.TrimPrefix(e.To, "unresolved::import::")] = true
		}
	}
	for _, want := range []string{
		"snippets/product-card.liquid", // render, whitespace-trimmed
		"snippets/header.liquid",       // include
		"sections/announcement-bar.liquid",
	} {
		if !imports[want] {
			t.Errorf("missing normalized import %q (got %v)", want, keysOfBoolMap(imports))
		}
	}

	var schema *graph.Node
	for _, n := range res.Nodes {
		if n.Kind == graph.KindConstant && n.Name == "schema" {
			schema = n
		}
	}
	if schema == nil {
		t.Fatal("schema block produced no constant node")
	}
	if r, _ := schema.Meta["value_redacted"].(bool); !r {
		t.Error("schema constant is not value_redacted")
	}
	// The raw JSON (and any secret in it) must not be in the graph.
	for _, n := range res.Nodes {
		for _, v := range n.Meta {
			if s, ok := v.(string); ok && strings.Contains(s, "sk-secret") {
				t.Errorf("a schema secret leaked into node %s meta", n.ID)
			}
		}
	}
}

// TestMyBatisSqlFragmentIncludeRefs proves <sql> fragments become first-class
// nodes, <include refid> becomes a traversable reference (same-mapper and
// cross-mapper ns.X -> ns::X), and resultType/parameterType are captured — so
// fragment reuse is navigable, which a statement-only extractor drops.
func TestMyBatisSqlFragmentIncludeRefs(t *testing.T) {
	src := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE mapper PUBLIC "-//mybatis.org//DTD Mapper 3.0//EN" "http://mybatis.org/dtd/mybatis-3-mapper.dtd">
<mapper namespace="com.app.UserMapper">
  <sql id="Base_Column_List">id, name, email</sql>
  <select id="findUser" resultType="com.app.User" parameterType="long">
    SELECT <include refid="Base_Column_List"/> FROM users WHERE id = #{id}
    <include refid="com.app.CommonMapper.tenantFilter"/>
  </select>
</mapper>
`
	res, err := NewMyBatisExtractor().Extract("UserMapper.xml", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	// Fragment node exists, marked as a fragment.
	var frag *graph.Node
	var stmt *graph.Node
	for _, n := range res.Nodes {
		switch n.ID {
		case "com.app.UserMapper::Base_Column_List":
			frag = n
		case "com.app.UserMapper::findUser":
			stmt = n
		}
	}
	if frag == nil {
		t.Fatal("missing <sql> fragment node")
	}
	if f, _ := frag.Meta["mybatis_fragment"].(bool); !f {
		t.Error("fragment node not marked mybatis_fragment")
	}
	if stmt == nil {
		t.Fatal("missing statement node")
	}
	if stmt.Meta["mybatis_result_type"] != "com.app.User" {
		t.Errorf("resultType=%v, want com.app.User", stmt.Meta["mybatis_result_type"])
	}
	if stmt.Meta["mybatis_parameter_type"] != "long" {
		t.Errorf("parameterType=%v, want long", stmt.Meta["mybatis_parameter_type"])
	}

	// include refs: same-mapper + cross-mapper.
	refs := map[string]bool{}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeReferences && e.Meta != nil && e.Meta["via"] == "mybatis.include" {
			refs[e.From+" -> "+e.To] = true
		}
	}
	if !refs["com.app.UserMapper::findUser -> com.app.UserMapper::Base_Column_List"] {
		t.Error("missing same-mapper include reference to Base_Column_List")
	}
	if !refs["com.app.UserMapper::findUser -> com.app.CommonMapper::tenantFilter"] {
		t.Error("missing cross-mapper include reference (ns.X -> ns::X) to CommonMapper.tenantFilter")
	}
}

// TestDfmParentStackNesting proves true tree nesting — a nested control is a
// member of its IMMEDIATE parent, not the form root — and that a multi-line
// property value (`= ( … )`) carrying literal object/end tokens does not
// corrupt the depth tracking.
func TestDfmParentStackNesting(t *testing.T) {
	src := "object Form1: TForm1\n" +
		"  object Panel1: TPanel\n" +
		"    object Button1: TButton\n" +
		"      OnClick = Button1Click\n" +
		"    end\n" +
		"    Items.Strings = (\n" +
		"      'object FakeData'\n" +
		"      'end'\n" +
		"    )\n" +
		"  end\n" +
		"end\n"
	res, err := NewDFMExtractor().Extract("Form1.dfm", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	members := map[string]string{}
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeMemberOf {
			members[e.From] = e.To
		}
	}
	// Button1 is a member of Panel1 (its immediate parent), NOT Form1.
	if members["Form1.dfm::Button1"] != "Form1.dfm::Panel1" {
		t.Errorf("Button1 memberOf %q, want Form1.dfm::Panel1 (immediate parent)", members["Form1.dfm::Button1"])
	}
	if members["Form1.dfm::Panel1"] != "Form1.dfm::Form1" {
		t.Errorf("Panel1 memberOf %q, want Form1.dfm::Form1", members["Form1.dfm::Panel1"])
	}

	// The multi-line property data ('object FakeData', 'end') must not have
	// produced phantom nodes: exactly the 3 real controls.
	objects := 0
	for _, n := range res.Nodes {
		if n.Kind == graph.KindType || n.Kind == graph.KindField {
			objects++
		}
	}
	if objects != 3 {
		t.Errorf("extracted %d object nodes, want 3 (multi-line property data leaked)", objects)
	}
	if n := nodeByName(res.Nodes, "FakeData"); n != nil {
		t.Error("a multi-line property data line was parsed as an object")
	}
}

func keysOfBoolMap(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func nodeByName(nodes []*graph.Node, name string) *graph.Node {
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	return nil
}
