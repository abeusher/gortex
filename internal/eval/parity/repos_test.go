package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchRepos(t *testing.T) {
	repos := BenchRepos()
	if len(repos) == 0 {
		t.Fatal("benchmark corpus is empty")
	}
	seen := map[string]bool{}
	for _, r := range repos {
		if r.Language == "" || r.URL == "" {
			t.Errorf("malformed bench repo: %+v", r)
		}
		if !strings.HasPrefix(r.URL, "https://") {
			t.Errorf("bench repo URL is not https: %q", r.URL)
		}
		if seen[r.Language] {
			t.Errorf("duplicate language in corpus: %q", r.Language)
		}
		seen[r.Language] = true
		if repoDirName(r) == "" || strings.ContainsAny(repoDirName(r), "/\\") {
			t.Errorf("unsafe cache dir name %q for %+v", repoDirName(r), r)
		}
	}
	// BenchRepos returns a copy — mutating it must not affect the source list.
	repos[0].URL = "mutated"
	if BenchRepos()[0].URL == "mutated" {
		t.Error("BenchRepos leaked a reference to the frozen corpus")
	}
}

func TestEnsureRepoCacheHit(t *testing.T) {
	cacheDir := t.TempDir()
	repo := BenchRepo{Language: "go", URL: "https://github.com/example/widget"}

	// Simulate a prior checkout — EnsureRepo must reuse it without cloning.
	dir := filepath.Join(cacheDir, repoDirName(repo))
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := EnsureRepo(cacheDir, repo)
	if err != nil {
		t.Fatalf("EnsureRepo cache hit errored (did it try to clone?): %v", err)
	}
	if got != dir {
		t.Errorf("EnsureRepo = %q, want cached %q", got, dir)
	}
}
