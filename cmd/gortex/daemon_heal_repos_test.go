package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
)

func TestHealDuplicateRepos_DropsCaseVariantAndPersists(t *testing.T) {
	forceCaseInsensitivePaths(t, true)

	base := t.TempDir()
	upper := filepath.Join(base, "MyRepo")
	lower := filepath.Join(base, "myrepo")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{Repos: []config.RepoEntry{
		{Path: upper, Name: "first"},
		{Path: lower, Name: "dup"},
	}}
	gc.SetConfigPath(cfgPath)
	require.NoError(t, gc.Save())

	removed := healDuplicateRepos(gc, zap.NewNop())
	assert.Equal(t, 1, removed)
	require.Len(t, gc.Repos, 1)
	assert.Equal(t, upper, gc.Repos[0].Path, "first (oldest) spelling must survive")

	// Persisted: reloading the config yields the single healed entry.
	reloaded, err := config.LoadGlobal(cfgPath)
	require.NoError(t, err)
	require.Len(t, reloaded.Repos, 1)
	assert.Equal(t, upper, reloaded.Repos[0].Path)
}

func TestHealDuplicateRepos_NoDuplicatesNoOp(t *testing.T) {
	forceCaseInsensitivePaths(t, true)
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	gc := &config.GlobalConfig{Repos: []config.RepoEntry{
		{Path: filepath.Join(t.TempDir(), "a")},
		{Path: filepath.Join(t.TempDir(), "b")},
	}}
	gc.SetConfigPath(cfgPath)
	require.NoError(t, gc.Save())

	assert.Equal(t, 0, healDuplicateRepos(gc, zap.NewNop()))
	assert.Len(t, gc.Repos, 2)
}

func TestHealDuplicateRepos_NilConfig(t *testing.T) {
	assert.Equal(t, 0, healDuplicateRepos(nil, zap.NewNop()))
}
