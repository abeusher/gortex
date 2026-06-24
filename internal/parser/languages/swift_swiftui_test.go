package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func swiftRoleOf(nodes []*graph.Node, name string) (string, bool) {
	for _, n := range nodes {
		if n.Kind == graph.KindType && n.Name == name && n.Meta != nil {
			if r, ok := n.Meta["swiftui_role"].(string); ok {
				return r, true
			}
		}
	}
	return "", false
}

func swiftEntryPoint(nodes []*graph.Node, name string) bool {
	for _, n := range nodes {
		if n.Kind == graph.KindType && n.Name == name && n.Meta != nil {
			b, _ := n.Meta["entry_point"].(bool)
			return b
		}
	}
	return false
}

func TestSwiftUI_ViewAndAppEntryClassification(t *testing.T) {
	src := []byte(`import SwiftUI

@main
struct MyApp: App {
    var body: some Scene { WindowGroup { ContentView() } }
}

struct ContentView: View {
    var body: some View { Text("hi") }
}

struct Plain {
    let x: Int
}
`)
	res, err := NewSwiftExtractor().Extract("App/MyApp.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	if r, _ := swiftRoleOf(res.Nodes, "ContentView"); r != "component" {
		t.Errorf("ContentView swiftui_role = %q (want component)", r)
	}
	if r, _ := swiftRoleOf(res.Nodes, "MyApp"); r != "app_entry" {
		t.Errorf("MyApp swiftui_role = %q (want app_entry)", r)
	}
	if !swiftEntryPoint(res.Nodes, "MyApp") {
		t.Errorf("@main App should be marked entry_point")
	}
	// A plain struct carries no SwiftUI role.
	if r, ok := swiftRoleOf(res.Nodes, "Plain"); ok {
		t.Errorf("plain struct should have no swiftui_role, got %q", r)
	}
}

func TestSwiftUI_AppWithoutMainNotEntry(t *testing.T) {
	// Conformance to App without @main is not classified as the app entry.
	src := []byte(`struct Helper: App {
    var body: some Scene { WindowGroup { } }
}
`)
	res, err := NewSwiftExtractor().Extract("App/Helper.swift", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := swiftRoleOf(res.Nodes, "Helper"); ok {
		t.Errorf("App conformance without @main should not be app_entry, got %q", r)
	}
}
