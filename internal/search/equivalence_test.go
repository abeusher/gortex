package search

import (
	"slices"
	"testing"
)

func TestEquivalenceTable_CuratedLookup(t *testing.T) {
	tbl := NewEquivalenceTable(nil)

	// A word resolves to its class siblings, never itself.
	got := tbl.Expand("login")
	if len(got) == 0 {
		t.Fatal("Expand(login) returned no siblings")
	}
	if slices.Contains(got, "login") {
		t.Error("Expand must not include the query token itself")
	}
	for _, want := range []string{"auth", "authentication", "signin", "credential"} {
		if !slices.Contains(got, want) {
			t.Errorf("Expand(login) missing curated sibling %q; got %v", want, got)
		}
	}

	// Case-insensitive.
	if len(tbl.Expand("LOGIN")) != len(got) {
		t.Error("Expand should be case-insensitive")
	}

	// A word in no class returns nil.
	if tbl.Expand("zzzznotaword") != nil {
		t.Error("Expand of an unknown token should return nil")
	}

	// delete/remove are siblings.
	if !slices.Contains(tbl.Expand("delete"), "remove") {
		t.Error("delete should expand to remove")
	}
	if !slices.Contains(tbl.Expand("remove"), "delete") {
		t.Error("remove should expand to delete (symmetric)")
	}
}

func TestEquivalenceTable_RepoExtra(t *testing.T) {
	// A repo-custom class is added and its label joins the class.
	tbl := NewEquivalenceTable(map[string][]string{
		"widget": {"gadget", "gizmo"},
	})
	got := tbl.Expand("gadget")
	if !slices.Contains(got, "gizmo") || !slices.Contains(got, "widget") {
		t.Errorf("repo-extra class not wired: Expand(gadget) = %v", got)
	}

	// A repo extra whose label is a curated word extends that class.
	tbl2 := NewEquivalenceTable(map[string][]string{
		"auth": {"oauth", "sso"},
	})
	authSibs := tbl2.Expand("oauth")
	if !slices.Contains(authSibs, "login") {
		t.Errorf("repo extra keyed on a curated word should extend the curated class; got %v", authSibs)
	}
}

func TestEquivalenceTable_NilSafe(t *testing.T) {
	var tbl *EquivalenceTable
	if tbl.Expand("auth") != nil {
		t.Error("nil EquivalenceTable.Expand should return nil")
	}
	if tbl.ClassCount() != 0 {
		t.Error("nil EquivalenceTable.ClassCount should be 0")
	}
}
