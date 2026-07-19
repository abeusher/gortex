package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/config"
)

func TestMatchRepo_CaseVariantAbsPath(t *testing.T) {
	forceCaseInsensitivePaths(t, true)
	repos := []config.RepoEntry{
		{Name: "alpha", Path: "/Users/me/Projects/Alpha"},
		{Name: "beta", Path: "/Users/me/Projects/Beta"},
	}
	i, err := matchRepo(repos, "/users/me/projects/alpha")
	require.NoError(t, err)
	assert.Equal(t, 0, i, "case-variant abs path must resolve to the right repo")
}

func TestMatchRepo_CaseSensitiveRejectsVariant(t *testing.T) {
	forceCaseInsensitivePaths(t, false)
	repos := []config.RepoEntry{{Name: "alpha", Path: "/Users/me/Projects/Alpha"}}
	_, err := matchRepo(repos, "/users/me/projects/alpha")
	require.Error(t, err, "case-sensitive: byte-different abs path must not match")
}

func TestMatchRepo_NameMatch(t *testing.T) {
	repos := []config.RepoEntry{{Name: "alpha", Path: "/Users/me/Projects/Alpha"}}
	i, err := matchRepo(repos, "alpha")
	require.NoError(t, err)
	assert.Equal(t, 0, i)
}

func TestMatchRepo_SuffixMatch(t *testing.T) {
	// Exercises the separator-aware suffix fallback (was a hardcoded "/").
	repos := []config.RepoEntry{{Name: "x", Path: "/Users/x/code/work/tuck-api"}}
	i, err := matchRepo(repos, "tuck-api")
	require.NoError(t, err)
	assert.Equal(t, 0, i)
}
