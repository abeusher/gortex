package parity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BenchRepo is one per-language benchmark repository the parity run indexes and
// measures coverage over.
type BenchRepo struct {
	Language string
	URL      string
	// Ref optionally pins a branch/tag for reproducibility (empty = default).
	Ref string
}

// benchRepos is the frozen per-language corpus used for parity coverage. The
// list is the canonical, license-clean public repository per language; it is
// cloned and cached on demand, never vendored.
var benchRepos = []BenchRepo{
	{Language: "python", URL: "https://github.com/psf/requests"},
	{Language: "go", URL: "https://github.com/gin-gonic/gin"},
	{Language: "rust", URL: "https://github.com/BurntSushi/ripgrep"},
	{Language: "java", URL: "https://github.com/google/gson"},
	{Language: "csharp", URL: "https://github.com/jbogard/MediatR"},
	{Language: "php", URL: "https://github.com/guzzle/guzzle"},
	{Language: "ruby", URL: "https://github.com/sidekiq/sidekiq"},
	{Language: "c", URL: "https://github.com/redis/redis"},
	{Language: "cpp", URL: "https://github.com/google/leveldb"},
	{Language: "swift", URL: "https://github.com/Alamofire/Alamofire"},
	{Language: "kotlin", URL: "https://github.com/square/okhttp"},
	{Language: "scala", URL: "https://github.com/gatling/gatling"},
	{Language: "dart", URL: "https://github.com/flutter/packages"},
	{Language: "lua", URL: "https://github.com/nvim-telescope/telescope.nvim"},
	{Language: "typescript", URL: "https://github.com/colinhacks/zod"},
	{Language: "svelte", URL: "https://github.com/sveltejs/realworld"},
	{Language: "vue", URL: "https://github.com/nuxt/movies"},
}

// BenchRepos returns the frozen benchmark corpus.
func BenchRepos() []BenchRepo {
	out := make([]BenchRepo, len(benchRepos))
	copy(out, benchRepos)
	return out
}

// EnsureRepo returns the local checkout path for repo, shallow-cloning it into
// cacheDir on a cache miss. A repo already checked out under its derived
// directory name is reused without re-cloning.
func EnsureRepo(cacheDir string, repo BenchRepo) (string, error) {
	dir := filepath.Join(cacheDir, repoDirName(repo))
	if isGitCheckout(dir) {
		return dir, nil
	}
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	args := []string{"clone", "--depth", "1", "--quiet"}
	if repo.Ref != "" {
		args = append(args, "--branch", repo.Ref)
	}
	args = append(args, repo.URL, dir)
	if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git clone %s: %w: %s", repo.URL, err, strings.TrimSpace(string(out)))
	}
	return dir, nil
}

// repoDirName derives a filesystem-safe cache directory name from a repo URL,
// e.g. https://github.com/psf/requests -> psf__requests.
func repoDirName(repo BenchRepo) string {
	u := strings.TrimSuffix(repo.URL, ".git")
	u = strings.TrimPrefix(u, "https://github.com/")
	u = strings.TrimPrefix(u, "http://github.com/")
	u = strings.Trim(u, "/")
	return strings.ReplaceAll(u, "/", "__")
}

// isGitCheckout reports whether dir is an existing git checkout.
func isGitCheckout(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil && info.IsDir()
}
