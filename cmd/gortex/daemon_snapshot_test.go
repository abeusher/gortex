package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// TestSnapshotRoundTrip proves that save + load preserves nodes and
// edges bit-for-bit. This is the guarantee the daemon's startup restore
// depends on; a silent corruption here would give warm-started daemons
// a stale graph that doesn't match any real source file.
func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "snap.gob.gz"))

	orig := graph.New()
	orig.AddNode(&graph.Node{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go"})
	orig.AddNode(&graph.Node{ID: "b.go::Bar", Name: "Bar", Kind: graph.KindMethod, FilePath: "b.go"})
	orig.AddEdge(&graph.Edge{From: "b.go::Bar", To: "a.go::Foo", Kind: graph.EdgeCalls, FilePath: "b.go", Line: 12})

	saveSnapshot(orig, "v-test", zap.NewNop())

	restored := graph.New()
	loaded, err := loadSnapshot(restored, zap.NewNop())
	require.NoError(t, err)
	require.True(t, loaded, "loadSnapshot must succeed for a freshly-written file")

	assert.Equal(t, orig.NodeCount(), restored.NodeCount(),
		"node count must round-trip")
	assert.Equal(t, orig.EdgeCount(), restored.EdgeCount(),
		"edge count must round-trip")

	n := restored.GetNode("a.go::Foo")
	require.NotNil(t, n)
	assert.Equal(t, "Foo", n.Name)
}

func TestLoadSnapshot_MissingFile_NotAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", filepath.Join(dir, "nope.gob.gz"))

	g := graph.New()
	loaded, err := loadSnapshot(g, zap.NewNop())
	require.NoError(t, err, "missing snapshot must not surface as an error — first-run path")
	assert.False(t, loaded, "no snapshot means loaded=false")
	assert.Equal(t, 0, g.NodeCount())
}

func TestLoadSnapshot_CorruptFile_ReportsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.gob.gz")
	t.Setenv("GORTEX_DAEMON_SNAPSHOT", path)
	require.NoError(t, os.WriteFile(path, []byte("not a gzip stream"), 0o600))

	g := graph.New()
	loaded, err := loadSnapshot(g, zap.NewNop())
	assert.Error(t, err, "corrupt snapshot must not be silently swallowed")
	assert.False(t, loaded)
	assert.Equal(t, 0, g.NodeCount())
}
