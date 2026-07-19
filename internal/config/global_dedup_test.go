package config

import (
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/pathkey"
)

// forceCaseInsensitive flips pathkey.CaseInsensitivePaths for the test and
// restores it afterwards. Tests using it must not run in parallel because
// the flag is process-global.
func forceCaseInsensitive(t *testing.T, v bool) {
	t.Helper()
	prev := pathkey.CaseInsensitivePaths
	pathkey.CaseInsensitivePaths = v
	t.Cleanup(func() { pathkey.CaseInsensitivePaths = prev })
}

// caseVariantPaths returns two absolute, non-existent paths under a fresh
// temp dir that differ only by the case of their final component, so a
// fold treats them as the same directory while os.Stat fails (letting
// SamePathIdentity trust the fold regardless of the host filesystem).
func caseVariantPaths(t *testing.T) (upper, lower string) {
	t.Helper()
	base := t.TempDir()
	return filepath.Join(base, "MyRepo"), filepath.Join(base, "myrepo")
}

func TestAddRepo_DedupesCaseVariant(t *testing.T) {
	forceCaseInsensitive(t, true)
	upper, lower := caseVariantPaths(t)

	gc := &GlobalConfig{}
	if err := gc.AddRepo(RepoEntry{Path: upper}); err != nil {
		t.Fatalf("AddRepo(upper): %v", err)
	}
	if err := gc.AddRepo(RepoEntry{Path: lower}); err != nil {
		t.Fatalf("AddRepo(lower): %v", err)
	}
	if len(gc.Repos) != 1 {
		t.Fatalf("case variant should not add a second entry: got %d entries", len(gc.Repos))
	}
	// The first spelling is the identity anchor and must be preserved.
	if gc.Repos[0].Path != upper {
		t.Fatalf("stored spelling rewritten: got %q want %q", gc.Repos[0].Path, upper)
	}
}

func TestAddRepo_CaseSensitiveKeepsBoth(t *testing.T) {
	forceCaseInsensitive(t, false)
	upper, lower := caseVariantPaths(t)

	gc := &GlobalConfig{}
	if err := gc.AddRepo(RepoEntry{Path: upper}); err != nil {
		t.Fatalf("AddRepo(upper): %v", err)
	}
	if err := gc.AddRepo(RepoEntry{Path: lower}); err != nil {
		t.Fatalf("AddRepo(lower): %v", err)
	}
	if len(gc.Repos) != 2 {
		t.Fatalf("case-sensitive: distinct casings should both be tracked: got %d", len(gc.Repos))
	}
}

func TestRemoveRepo_MatchesCaseVariant(t *testing.T) {
	forceCaseInsensitive(t, true)
	upper, lower := caseVariantPaths(t)

	gc := &GlobalConfig{Repos: []RepoEntry{{Path: upper}}}
	if err := gc.RemoveRepo(lower); err != nil {
		t.Fatalf("RemoveRepo(lower) should match the upper entry: %v", err)
	}
	if len(gc.Repos) != 0 {
		t.Fatalf("entry not removed: %d remain", len(gc.Repos))
	}
}

func TestDedupeRepos_KeepsFirstReturnsRemoved(t *testing.T) {
	forceCaseInsensitive(t, true)
	upper, lower := caseVariantPaths(t)
	other := filepath.Join(t.TempDir(), "distinct")

	gc := &GlobalConfig{Repos: []RepoEntry{
		{Path: upper, Name: "first"},
		{Path: other, Name: "other"},
		{Path: lower, Name: "dup"},
	}}
	removed := gc.DedupeRepos()

	if len(removed) != 1 {
		t.Fatalf("expected 1 removed entry, got %d", len(removed))
	}
	if removed[0].Path != lower {
		t.Fatalf("wrong entry removed: got %q want %q", removed[0].Path, lower)
	}
	if len(gc.Repos) != 2 {
		t.Fatalf("expected 2 surviving entries, got %d", len(gc.Repos))
	}
	// The first (oldest) spelling of the duplicated directory survives.
	if gc.Repos[0].Path != upper || gc.Repos[0].Name != "first" {
		t.Fatalf("first entry not preserved: %+v", gc.Repos[0])
	}
}

func TestDedupeRepos_NoDuplicatesNoOp(t *testing.T) {
	forceCaseInsensitive(t, true)
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	gc := &GlobalConfig{Repos: []RepoEntry{{Path: a}, {Path: b}}}
	if removed := gc.DedupeRepos(); removed != nil {
		t.Fatalf("expected no removals, got %v", removed)
	}
	if len(gc.Repos) != 2 {
		t.Fatalf("clean list must be untouched: got %d", len(gc.Repos))
	}
}
