package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func expoMethodMeta(nodes []*graph.Node) map[string][2]any {
	out := map[string][2]any{}
	for _, n := range nodes {
		if n.Kind != graph.KindMethod || n.Meta == nil {
			continue
		}
		mod, _ := n.Meta["expo_module"].(string)
		if mod == "" {
			continue
		}
		async, _ := n.Meta["expo_async"].(bool)
		out[n.Name] = [2]any{mod, async}
	}
	return out
}

func TestSwiftExtract_ExpoModule(t *testing.T) {
	src := `import ExpoModulesCore

public class MathModule: Module {
  public func definition() -> ModuleDefinition {
    Name("Math")
    Function("add") { (a: Int, b: Int) -> Int in
      return a + b
    }
    AsyncFunction("fetchData") { (url: String) in
    }
  }
}
`
	r, err := NewSwiftExtractor().Extract("MathModule.swift", []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	meta := expoMethodMeta(r.Nodes)
	if got, ok := meta["add"]; !ok || got[0] != "Math" || got[1] != false {
		t.Errorf("add = %v (ok=%v), want module Math, async false", meta["add"], ok)
	}
	if got, ok := meta["fetchData"]; !ok || got[0] != "Math" || got[1] != true {
		t.Errorf("fetchData = %v (ok=%v), want module Math, async true", meta["fetchData"], ok)
	}
}

func TestKotlinExtract_ExpoModule(t *testing.T) {
	src := `package expo.modules.math

import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition

class MathModule : Module() {
  override fun definition() = ModuleDefinition {
    Name("Math")
    Function("add") { a: Int, b: Int -> a + b }
    AsyncFunction("fetchData") { url: String -> }
  }
}
`
	r, err := NewKotlinExtractor().Extract("MathModule.kt", []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	meta := expoMethodMeta(r.Nodes)
	if got, ok := meta["add"]; !ok || got[0] != "Math" {
		t.Errorf("add = %v (ok=%v), want module Math", meta["add"], ok)
	}
	if _, ok := meta["fetchData"]; !ok {
		t.Errorf("fetchData not extracted")
	}
}

func TestExtractExpoModules_NonExpoFileIgnored(t *testing.T) {
	// A Swift file that happens to call Name("x")/Function("y") but is not
	// an Expo module (no ModuleDefinition) must yield nothing.
	if got := extractExpoModules([]byte(`func f() { Name("x"); Function("y") }`)); got != nil {
		t.Errorf("non-Expo file produced %v, want nil", got)
	}
}

func expoMembers(nodes []*graph.Node) map[string]*graph.Node {
	out := map[string]*graph.Node{}
	for _, n := range nodes {
		if n.Meta == nil {
			continue
		}
		if _, ok := n.Meta["expo_module"]; ok {
			out[n.Name] = n
		}
	}
	return out
}

func TestExpoModule_MembersAndClassFallback_Swift(t *testing.T) {
	src := `import ExpoModulesCore

public class SettingsModule: Module {
  public func definition() -> ModuleDefinition {
    Property("theme")
    Constants {
      ["version": "1.0"]
    }
    Events("onChange")
    AsyncFunction<Int>("compute") { (x: Int) in }
  }
}
`
	r, err := NewSwiftExtractor().Extract("SettingsModule.swift", []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	m := expoMembers(r.Nodes)
	if p, ok := m["theme"]; !ok || p.Kind != graph.KindField || p.Meta["expo_module"] != "Settings" || p.Meta["expo_kind"] != "property" {
		t.Errorf("Property theme = %+v (ok=%v), want field/Settings/property", m["theme"], ok)
	}
	if c, ok := m["Constants"]; !ok || c.Meta["expo_module"] != "Settings" || c.Meta["expo_kind"] != "constants" {
		t.Errorf("Constants = %+v (ok=%v), want Settings/constants", m["Constants"], ok)
	}
	if e, ok := m["onChange"]; !ok || e.Meta["expo_module"] != "Settings" || e.Meta["expo_kind"] != "events" {
		t.Errorf("Events onChange = %+v (ok=%v), want Settings/events", m["onChange"], ok)
	}
	if g, ok := m["compute"]; !ok || g.Meta["expo_module"] != "Settings" || g.Meta["expo_async"] != true {
		t.Errorf("generic AsyncFunction<Int> compute = %+v (ok=%v), want Settings/async", m["compute"], ok)
	}
}

func TestExpoModule_GenericAndClassFallback_Kotlin(t *testing.T) {
	src := `package expo.modules.demo

import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition

class GeometryModule : Module() {
  override fun definition() = ModuleDefinition {
    Property("origin")
    AsyncFunction<Float>("area") { r: Float -> }
  }
}
`
	r, err := NewKotlinExtractor().Extract("GeometryModule.kt", []byte(src))
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	m := expoMembers(r.Nodes)
	if g, ok := m["area"]; !ok || g.Meta["expo_module"] != "Geometry" || g.Meta["expo_async"] != true {
		t.Errorf("generic AsyncFunction<Float> area = %+v (ok=%v), want Geometry/async", m["area"], ok)
	}
	if p, ok := m["origin"]; !ok || p.Kind != graph.KindField || p.Meta["expo_module"] != "Geometry" {
		t.Errorf("Property origin = %+v (ok=%v), want field/Geometry", m["origin"], ok)
	}
}
