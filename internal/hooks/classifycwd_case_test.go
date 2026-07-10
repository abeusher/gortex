package hooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/pathkey"
)

func forceHooksCaseInsensitive(t *testing.T, v bool) {
	t.Helper()
	prev := pathkey.CaseInsensitivePaths
	pathkey.CaseInsensitivePaths = v
	t.Cleanup(func() { pathkey.CaseInsensitivePaths = prev })
}

func TestClassifyCwd_CaseMismatchedExact(t *testing.T) {
	forceHooksCaseInsensitive(t, true)
	repos := []daemon.TrackedRepoStatus{{Path: "/Users/me/Repo"}}
	exact, contained := classifyCwd("/users/me/repo", repos)
	require.NotNil(t, exact, "case-variant cwd must match the tracked repo exactly")
	assert.Empty(t, contained)
}

func TestClassifyCwd_CaseMismatchedContained(t *testing.T) {
	forceHooksCaseInsensitive(t, true)
	repos := []daemon.TrackedRepoStatus{{Path: "/Users/me/Workspace/Repo"}}
	exact, contained := classifyCwd("/users/me/workspace", repos)
	assert.Nil(t, exact)
	require.Len(t, contained, 1, "a repo under a case-variant workspace cwd must be contained")
}

func TestClassifyCwd_CaseSensitiveNoMatch(t *testing.T) {
	forceHooksCaseInsensitive(t, false)
	repos := []daemon.TrackedRepoStatus{{Path: "/Users/me/Repo"}}
	exact, contained := classifyCwd("/users/me/repo", repos)
	assert.Nil(t, exact, "case-sensitive: byte-different cwd must not match")
	assert.Empty(t, contained)
}
