package tstypes

import (
	"testing"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// A typed parameter grounds its receiver: `$x->bar()` on a `Foo $x`
// resolves to Foo::bar.
func TestPHP_TypedParamResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Foo {
    public function bar(): void {}
}

class App {
    public function f(Foo $x): void {
        $x->bar();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	res, err := p.Enrich(g, dir)
	if err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("typed-param call %s -> %s not resolved; edges: %v", caller.ID, target.ID, g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "php-types")
	if res.EdgesConfirmed+res.EdgesAdded == 0 {
		t.Errorf("result reported no edge work: %+v", res)
	}
}

// `$this->field->method()` resolves through the declared property type.
func TestPHP_ThisFieldResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Foo {
    public function bar(): void {}
}

class App {
    private Foo $x;

    public function f(): void {
        $this->x->bar();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	if callEdgeTo(g, caller.ID, target.ID) == nil {
		t.Fatalf("$this->x->bar() not resolved through field type; edges: %v", g.GetOutEdges(caller.ID))
	}
}

// `(new Foo())->bar()` and the parenthesis-free `new Foo()->bar()` both
// type their receiver from the constructor expression.
func TestPHP_NewExprChainResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Foo {
    public function bar(): void {}
}

class App {
    public function f(): void {
        (new Foo())->bar();
        new Foo()->bar();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("new-expression chain not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "php-types")
}

// A local bound from `new Foo()` propagates its type to a later call.
func TestPHP_LocalConstructorInferenceResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Foo {
    public function bar(): void {}
}

class App {
    public function f(): void {
        $o = new Foo();
        $o->bar();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "bar", graph.KindMethod)
	if callEdgeTo(g, caller.ID, target.ID) == nil {
		t.Fatalf("constructor-inferred local call not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
}

// A static `Foo::make()` resolves to the named class's method.
func TestPHP_StaticCallResolves(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Foo {
    public static function make(): void {}
}

class App {
    public function f(): void {
        Foo::make();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "make", graph.KindMethod)
	e := callEdgeTo(g, caller.ID, target.ID)
	if e == nil {
		t.Fatalf("static Foo::make() not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
	assertASTProvenance(t, e, "php-types")
}

// A constructor-promoted property is treated as a typed field, so a
// call through it resolves.
func TestPHP_PromotedParamFieldResolvesCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Dep {
    public function work(): void {}
}

class App {
    public function __construct(private Dep $dep) {}

    public function f(): void {
        $this->dep->work();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "work", graph.KindMethod)
	if callEdgeTo(g, caller.ID, target.ID) == nil {
		t.Fatalf("promoted-property call not resolved; edges: %v", g.GetOutEdges(caller.ID))
	}
}

// Assigning a typed parameter to a property gives the property that
// type, even when the property declaration itself is untyped.
func TestPHP_ThisFieldFromParamInference(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class Dep {
    public function work(): void {}
}

class App {
    private $cached;

    public function __construct(Dep $seed) {
        $this->cached = $seed;
    }

    public function f(): void {
        $this->cached->work();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	target := nodeByNameKind(t, g, "work", graph.KindMethod)
	if callEdgeTo(g, caller.ID, target.ID) == nil {
		t.Fatalf("property-from-parameter inference did not resolve the call; edges: %v", g.GetOutEdges(caller.ID))
	}
}

// `class Impl extends Base implements Greeter` synthesizes the
// inheritance edges, and a call to an inherited method resolves through
// the extends climb.
func TestPHP_ExtendsImplementsAndInheritedCall(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
interface Greeter {
    public function greet(): void;
}

class Base {
    public function run(): void {}
}

class Impl extends Base implements Greeter {
    public function greet(): void {}

    public function go(Impl $i): void {
        $i->run();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	impl := nodeByNameKind(t, g, "Impl", graph.KindType)
	base := nodeByNameKind(t, g, "Base", graph.KindType)
	iface := nodeByNameKind(t, g, "Greeter", graph.KindInterface)

	ee := edgeBetween(g, impl.ID, graph.EdgeExtends, base.ID)
	if ee == nil {
		t.Fatalf("extends edge missing; edges: %v", g.GetOutEdges(impl.ID))
	}
	assertASTProvenance(t, ee, "php-types")

	ie := edgeBetween(g, impl.ID, graph.EdgeImplements, iface.ID)
	if ie == nil {
		t.Fatalf("implements edge missing; edges: %v", g.GetOutEdges(impl.ID))
	}
	assertASTProvenance(t, ie, "php-types")

	goMethod := nodeByNameKind(t, g, "go", graph.KindMethod)
	run := nodeByNameKind(t, g, "run", graph.KindMethod)
	if callEdgeTo(g, goMethod.ID, run.ID) == nil {
		t.Fatalf("inherited method call did not resolve through extends; edges: %v", g.GetOutEdges(goMethod.ID))
	}
}

// An ambiguous overload (two same-named methods, no way to choose) is
// skipped rather than guessed.
func TestPHP_AmbiguousOverloadSkipped(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"app.php": `<?php
class K {
    public function bar() {}
    public function bar() {}
}

class App {
    public function f(K $k): void {
        $k->bar();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "f", graph.KindMethod)
	assertUntouched(t, g, caller.ID, "bar", "php-types")
}

// `use App\Service` binds the short name to the imported FQN, steering a
// cross-file resolution onto the right package when several types share
// a name.
func TestPHP_ImportHintDisambiguatesCrossFile(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"App/Service.php": `<?php
namespace App;

class Service {
    public function run(): void {}
}
`,
		"Other/Service.php": `<?php
namespace Other;

class Service {
    public function run(): void {}
}
`,
		"Client/Handler.php": `<?php
namespace Client;

use App\Service;

class Handler {
    public function handle(Service $s): void {
        $s->run();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.Enrich(g, dir); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "handle", graph.KindMethod)
	want := "App/Service.php::Service.run"
	if callEdgeTo(g, caller.ID, want) == nil {
		t.Fatalf("import-hinted call did not land on %s; edges: %v", want, g.GetOutEdges(caller.ID))
	}
	wrong := "Other/Service.php::Service.run"
	if callEdgeTo(g, caller.ID, wrong) != nil {
		t.Fatalf("call landed on the wrong namespace's type %s", wrong)
	}
}

// EnrichFile resolves only the named file's calls, leaving others alone.
func TestPHP_EnrichFileScopesToOneFile(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"foo.php": `<?php
class Foo {
    public function bar(): void {}
    public function baz(): void {}
}
`,
		"app.php": `<?php
class App {
    public function main(Foo $x): void {
        $x->bar();
    }
}
`,
		"other.php": `<?php
class Other {
    public function go(Foo $x): void {
        $x->baz();
    }
}
`,
	})
	p := NewProvider(PHPSpec(), zap.NewNop())
	if _, err := p.EnrichFile(g, dir, "app.php"); err != nil {
		t.Fatal(err)
	}
	caller := nodeByNameKind(t, g, "main", graph.KindMethod)
	bar := nodeByNameKind(t, g, "bar", graph.KindMethod)
	if callEdgeTo(g, caller.ID, bar.ID) == nil {
		t.Fatalf("EnrichFile did not resolve the target file's call")
	}
	other := nodeByNameKind(t, g, "go", graph.KindMethod)
	assertUntouched(t, g, other.ID, "baz", "php-types")
}
