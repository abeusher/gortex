package modules

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestParseGoMod_Variants(t *testing.T) {
	src := []byte(`module github.com/example/x

go 1.22

require github.com/spf13/cobra v1.10.0

require (
	github.com/sabhiram/go-gitignore v0.0.0-20210923224102-525f6e181f06
	github.com/stretchr/testify v1.11.1
	go.uber.org/zap v1.27.1 // indirect
)

replace github.com/foo/bar => ./local/bar
`)
	specs := ParseGoMod(src)
	if len(specs) != 4 {
		t.Fatalf("expected 4 specs, got %d: %+v", len(specs), specs)
	}

	got := map[string]Spec{}
	for _, s := range specs {
		got[s.Path] = s
	}

	if got["github.com/spf13/cobra"].Version != "v1.10.0" {
		t.Errorf("cobra version = %q", got["github.com/spf13/cobra"].Version)
	}
	if !got["go.uber.org/zap"].Indirect {
		t.Errorf("zap should be indirect")
	}
	if got["go.uber.org/zap"].Indirect != true {
		t.Errorf("zap indirect flag wrong")
	}
	if got["github.com/sabhiram/go-gitignore"].Indirect {
		t.Errorf("go-gitignore should not be indirect")
	}
}

func TestParseGoMod_ReplaceDirective(t *testing.T) {
	src := []byte(`module x

require github.com/foo/bar v1.0.0
replace github.com/foo/bar => ./local/bar
`)
	specs := ParseGoMod(src)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	if specs[0].Replace != "./local/bar" {
		t.Errorf("replace = %q", specs[0].Replace)
	}
}

func TestParseGoMod_Empty(t *testing.T) {
	if got := ParseGoMod(nil); got != nil {
		t.Errorf("nil input should yield nil specs")
	}
	if got := ParseGoMod([]byte("module x\n")); len(got) != 0 {
		t.Errorf("module-only manifest should have no deps")
	}
}

func TestModuleNodeID(t *testing.T) {
	cases := []struct {
		ecosystem, path, version, want string
	}{
		{"go", "github.com/foo/bar", "v1.0.0", "module::go:github.com/foo/bar@v1.0.0"},
		{"go", "github.com/foo/bar", "", "module::go:github.com/foo/bar"},
		{"npm", "lodash", "4.17.0", "module::npm:lodash@4.17.0"},
	}
	for _, c := range cases {
		if got := ModuleNodeID(c.ecosystem, c.path, c.version); got != c.want {
			t.Errorf("ModuleNodeID(%q,%q,%q) = %q, want %q",
				c.ecosystem, c.path, c.version, got, c.want)
		}
	}
}

func TestBuildGraphArtifacts(t *testing.T) {
	specs := []Spec{
		{Ecosystem: "go", Path: "github.com/foo/bar", Version: "v1.0.0", Line: 5},
		{Ecosystem: "go", Path: "github.com/foo/bar", Version: "v1.0.0", Line: 6}, // dup
		{Ecosystem: "go", Path: "go.uber.org/zap", Version: "v1.27.1", Indirect: true, Line: 7},
	}
	nodes, edges := BuildGraphArtifacts("go.mod", specs)

	if len(nodes) != 2 {
		t.Errorf("expected 2 unique nodes, got %d", len(nodes))
	}
	if len(edges) != 3 {
		t.Errorf("expected 3 edges (one per spec, dups produce dup edges), got %d", len(edges))
	}
	for _, e := range edges {
		if e.From != "go.mod" {
			t.Errorf("edge from = %q", e.From)
		}
		if e.Kind != graph.EdgeDependsOnModule {
			t.Errorf("edge kind = %q", e.Kind)
		}
	}

	for _, n := range nodes {
		if n.Kind != graph.KindModule {
			t.Errorf("node kind = %q", n.Kind)
		}
		if n.Meta["ecosystem"] != "go" {
			t.Errorf("ecosystem meta = %v", n.Meta["ecosystem"])
		}
	}
	// Verify the indirect flag on the zap node.
	for _, n := range nodes {
		if n.Meta["path"] == "go.uber.org/zap" {
			if v, _ := n.Meta["indirect"].(bool); !v {
				t.Errorf("zap indirect flag missing")
			}
		}
	}
}

func TestParsePackageJSON_AllBlocks(t *testing.T) {
	src := []byte(`{
  "name": "my-app",
  "version": "1.0.0",
  "dependencies": {
    "react": "^18.2.0",
    "lodash": "4.17.21"
  },
  "devDependencies": {
    "vitest": "^1.0.0"
  },
  "peerDependencies": {
    "next": ">=13.0.0"
  },
  "optionalDependencies": {
    "fsevents": "^2.3.0"
  }
}`)
	specs := ParsePackageJSON(src)
	if len(specs) != 5 {
		t.Fatalf("expected 5 specs, got %d: %+v", len(specs), specs)
	}

	got := map[string]Spec{}
	for _, s := range specs {
		got[s.Path] = s
		if s.Ecosystem != "npm" {
			t.Errorf("ecosystem = %q for %q", s.Ecosystem, s.Path)
		}
	}
	if got["react"].Version != "^18.2.0" {
		t.Errorf("react version = %q", got["react"].Version)
	}
	if got["react"].Indirect {
		t.Errorf("react should NOT be indirect (production dep)")
	}
	if !got["vitest"].Indirect || got["vitest"].Replace != "dev" {
		t.Errorf("vitest should be dev-indirect: %+v", got["vitest"])
	}
	if got["next"].Replace != "peer" {
		t.Errorf("next.Replace = %q", got["next"].Replace)
	}
	if got["fsevents"].Replace != "optional" {
		t.Errorf("fsevents.Replace = %q", got["fsevents"].Replace)
	}
}

func TestParsePackageJSON_Empty(t *testing.T) {
	if got := ParsePackageJSON(nil); got != nil {
		t.Errorf("nil input → nil specs")
	}
	if got := ParsePackageJSON([]byte("{}")); len(got) != 0 {
		t.Errorf("empty manifest → empty specs")
	}
}

func TestParsePackageJSON_Malformed(t *testing.T) {
	if got := ParsePackageJSON([]byte("not json")); got != nil {
		t.Errorf("malformed input → nil")
	}
}

func TestParsePackageJSON_StableOrder(t *testing.T) {
	// JSON map iteration is randomised — our packageJSONBlock
	// helper sorts within each block to keep tests deterministic.
	src := []byte(`{"dependencies": {"zoo": "1.0", "alpha": "2.0", "beta": "3.0"}}`)
	specs := ParsePackageJSON(src)
	if len(specs) != 3 {
		t.Fatalf("expected 3, got %d", len(specs))
	}
	if specs[0].Path != "alpha" || specs[1].Path != "beta" || specs[2].Path != "zoo" {
		t.Errorf("not alphabetically sorted: %+v", specs)
	}
}

func TestLinkImports_LongestPrefix(t *testing.T) {
	g := graph.New()
	// Two import nodes — one for an exact match, one for a sub-package.
	g.AddNode(&graph.Node{
		ID:       "pkg/a.go::import::github.com/spf13/cobra",
		Kind:     graph.KindImport,
		FilePath: "pkg/a.go",
		Meta:     map[string]any{"path": "github.com/spf13/cobra"},
	})
	g.AddNode(&graph.Node{
		ID:       "pkg/b.go::import::github.com/spf13/cobra/doc",
		Kind:     graph.KindImport,
		FilePath: "pkg/b.go",
		Meta:     map[string]any{"path": "github.com/spf13/cobra/doc"},
	})
	g.AddNode(&graph.Node{
		ID:       "pkg/c.go::import::own/internal/foo",
		Kind:     graph.KindImport,
		FilePath: "pkg/c.go",
		Meta:     map[string]any{"path": "own/internal/foo"},
	})

	specs := []Spec{
		{Ecosystem: "go", Path: "github.com/spf13/cobra", Version: "v1.0.0"},
		{Ecosystem: "go", Path: "go.uber.org/zap", Version: "v1.27.1"},
	}

	emitted := LinkImports(g, specs, "own")
	if emitted != 2 {
		t.Errorf("expected 2 edges (cobra exact + cobra/doc prefix; own/internal skipped), got %d", emitted)
	}

	wantTo := "module::go:github.com/spf13/cobra@v1.0.0"
	hits := 0
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeDependsOnModule && e.To == wantTo {
			hits++
		}
	}
	if hits != 2 {
		t.Errorf("expected 2 edges to %q, got %d", wantTo, hits)
	}
}

func TestLinkImports_PrefersLongerSpecForVersionedImports(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID:       "f::import::github.com/foo/bar/v2/sub",
		Kind:     graph.KindImport,
		FilePath: "f.go",
		Meta:     map[string]any{"path": "github.com/foo/bar/v2/sub"},
	})

	// Both v1 and v2 exist — the longest match wins.
	specs := []Spec{
		{Ecosystem: "go", Path: "github.com/foo/bar", Version: "v1.0.0"},
		{Ecosystem: "go", Path: "github.com/foo/bar/v2", Version: "v2.1.0"},
	}

	if got := LinkImports(g, specs, ""); got != 1 {
		t.Fatalf("expected 1 edge, got %d", got)
	}
	for _, e := range g.AllEdges() {
		if e.Kind == graph.EdgeDependsOnModule {
			if e.To != "module::go:github.com/foo/bar/v2@v2.1.0" {
				t.Errorf("wrong module target: %q (longest spec should win)", e.To)
			}
		}
	}
}

func TestLinkImports_SkipsWhenNoMatch(t *testing.T) {
	g := graph.New()
	g.AddNode(&graph.Node{
		ID:       "f::import::stdlib",
		Kind:     graph.KindImport,
		FilePath: "f.go",
		Meta:     map[string]any{"path": "fmt"},
	})

	specs := []Spec{
		{Ecosystem: "go", Path: "github.com/foo/bar"},
	}

	if got := LinkImports(g, specs, ""); got != 0 {
		t.Errorf("stdlib import shouldn't match external module, got %d edges", got)
	}
}

func TestShortName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"github.com/foo/bar", "bar"},
		{"github.com/foo/bar/v2", "bar"},
		{"github.com/foo/bar/v10", "bar"},
		{"foo", "foo"},
		{"", ""},
	}
	for _, c := range cases {
		if got := shortName(c.in); got != c.want {
			t.Errorf("shortName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
