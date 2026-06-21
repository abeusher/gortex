package serverstack

import (
	"os"
	"path/filepath"
	"testing"
)

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
