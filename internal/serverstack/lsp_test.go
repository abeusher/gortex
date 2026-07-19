package serverstack

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/semantic/lsp"
)

// TestPreferredSpecName pins the resolve-time helper's server choice:
// the highest-priority spec the router reports available wins, with
// the node-based TypeScript server serving only when tsgo is absent.
func TestPreferredSpecName(t *testing.T) {
	availableSet := func(names ...string) func(*lsp.ServerSpec) bool {
		have := make(map[string]bool, len(names))
		for _, n := range names {
			have[n] = true
		}
		return func(s *lsp.ServerSpec) bool { return s != nil && have[s.Name] }
	}

	if got := preferredSpecName(availableSet("tsgo", "typescript-language-server"), ".ts"); got != "tsgo" {
		t.Errorf("both available: got %q, want tsgo", got)
	}
	if got := preferredSpecName(availableSet("typescript-language-server"), ".ts"); got != "typescript-language-server" {
		t.Errorf("tsgo absent: got %q, want typescript-language-server", got)
	}
	if got := preferredSpecName(availableSet(), ".ts"); got != "tsgo" {
		t.Errorf("none available: got %q, want priority winner tsgo", got)
	}
	if got := preferredSpecName(availableSet("pyright", "pyrefly"), ".py"); got != "pyright" {
		t.Errorf("python default: got %q, want pyright", got)
	}
	if got := preferredSpecName(availableSet(), ".unknown_ext"); got != "" {
		t.Errorf("unknown extension: got %q, want empty", got)
	}
}

func TestRepoLikelyHasPythonIntent(t *testing.T) {
	dir := t.TempDir()
	if RepoLikelyHasPythonIntent(dir) {
		t.Fatalf("empty temp dir should not look like a Python repo")
	}

	if err := os.WriteFile(filepath.Join(dir, "pyproject.toml"), []byte("[project]\nname = \"demo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !RepoLikelyHasPythonIntent(dir) {
		t.Fatalf("pyproject.toml should mark a Python repo")
	}
}

func TestRepoLikelyHasPythonIntent_RootScript(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script.py"), []byte("print('hello')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !RepoLikelyHasPythonIntent(dir) {
		t.Fatalf("root-level .py file should mark a Python repo")
	}
}
