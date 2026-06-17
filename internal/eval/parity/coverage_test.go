package parity

import (
	"math"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestCoverageOf(t *testing.T) {
	g := graph.New()

	fn := func(id, file, lang string) {
		g.AddNode(&graph.Node{ID: id, Name: id, Kind: graph.KindFunction, FilePath: file, Language: lang})
	}
	file := func(path, lang string) {
		g.AddNode(&graph.Node{ID: path, Name: path, Kind: graph.KindFile, FilePath: path, Language: lang})
	}
	call := func(from, to string) {
		g.AddEdge(&graph.Edge{From: from, To: to, Kind: graph.EdgeCalls})
	}

	// Three Go source files, each with one function.
	file("a.go", "go")
	file("b.go", "go")
	file("c.go", "go")
	fn("a.go::Foo", "a.go", "go")
	fn("b.go::Bar", "b.go", "go")
	fn("c.go::Baz", "c.go", "go")

	// b depends on a, c depends on b → a and b are covered, c is not.
	call("b.go::Bar", "a.go::Foo")
	call("c.go::Baz", "b.go::Bar")
	// Same-file call must not count toward coverage.
	call("a.go::Foo", "a.go::Foo")
	// An unresolved target must not cover anything.
	call("a.go::Foo", "unresolved::Qux")

	// A Python file nothing depends on → 0% covered.
	file("p.py", "python")
	fn("p.py::pyf", "p.py", "python")

	cov := CoverageOf(g)
	byLang := map[string]LanguageCoverage{}
	for _, c := range cov {
		byLang[c.Language] = c
	}

	got, ok := byLang["go"]
	if !ok {
		t.Fatalf("no go coverage row in %+v", cov)
	}
	if got.SymbolFiles != 3 || got.CoveredFiles != 2 {
		t.Errorf("go: SymbolFiles=%d CoveredFiles=%d, want 3 and 2", got.SymbolFiles, got.CoveredFiles)
	}
	if math.Abs(got.Coverage-2.0/3.0) > 1e-9 {
		t.Errorf("go: Coverage=%v, want %v", got.Coverage, 2.0/3.0)
	}

	py, ok := byLang["python"]
	if !ok {
		t.Fatalf("no python coverage row in %+v", cov)
	}
	if py.SymbolFiles != 1 || py.CoveredFiles != 0 || py.Coverage != 0 {
		t.Errorf("python: %+v, want SymbolFiles=1 CoveredFiles=0 Coverage=0", py)
	}
}
