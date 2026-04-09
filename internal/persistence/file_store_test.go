package persistence

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func testSnapshot() *Snapshot {
	return &Snapshot{
		Version:    "0.1.0-test",
		RepoPath:   "/tmp/test-repo",
		CommitHash: "abc123def456",
		IndexedAt:  time.Now().Truncate(time.Second),
		Nodes: []*graph.Node{
			{
				ID: "main.go::Foo", Kind: graph.KindFunction, Name: "Foo",
				FilePath: "main.go", StartLine: 1, EndLine: 5, Language: "go",
				Meta: map[string]any{"signature": "func Foo(x int) error"},
			},
			{
				ID: "main.go::Bar", Kind: graph.KindMethod, Name: "Bar",
				FilePath: "main.go", StartLine: 7, EndLine: 12, Language: "go",
				Meta: map[string]any{"receiver": "Server", "signature": "func (s *Server) Bar()"},
			},
		},
		Edges: []*graph.Edge{
			{
				From: "main.go::Foo", To: "main.go::Bar", Kind: graph.EdgeCalls,
				FilePath: "main.go", Line: 3, Confidence: 0.95,
				Meta: map[string]any{"receiver_type": "Server"},
			},
		},
		FileMtimes: map[string]int64{
			"main.go": 1700000000000000000,
			"util.go": 1700000001000000000,
		},
	}
}

func TestFileStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "0.1.0-test")
	require.NoError(t, err)

	snap := testSnapshot()

	// Save.
	require.NoError(t, fs.Save(snap))

	// Check.
	assert.True(t, fs.Check(snap.RepoPath, snap.CommitHash))

	// Validate.
	assert.True(t, fs.Validate(snap.RepoPath, snap.CommitHash))

	// Load.
	loaded, err := fs.Load(snap.RepoPath, snap.CommitHash)
	require.NoError(t, err)

	// Verify.
	assert.Equal(t, snap.Version, loaded.Version)
	assert.Equal(t, snap.RepoPath, loaded.RepoPath)
	assert.Equal(t, snap.CommitHash, loaded.CommitHash)
	assert.Equal(t, snap.IndexedAt, loaded.IndexedAt)

	require.Len(t, loaded.Nodes, 2)
	assert.Equal(t, "main.go::Foo", loaded.Nodes[0].ID)
	assert.Equal(t, "Foo", loaded.Nodes[0].Name)
	assert.Equal(t, "func Foo(x int) error", loaded.Nodes[0].Meta["signature"])

	assert.Equal(t, "Server", loaded.Nodes[1].Meta["receiver"])

	require.Len(t, loaded.Edges, 1)
	assert.Equal(t, "main.go::Foo", loaded.Edges[0].From)
	assert.Equal(t, "main.go::Bar", loaded.Edges[0].To)
	assert.Equal(t, 0.95, loaded.Edges[0].Confidence)
	assert.Equal(t, "Server", loaded.Edges[0].Meta["receiver_type"])

	assert.Equal(t, snap.FileMtimes, loaded.FileMtimes)
}

func TestFileStore_Validate_VersionMismatch(t *testing.T) {
	dir := t.TempDir()
	fsV1, err := NewFileStore(dir, "0.1.0")
	require.NoError(t, err)

	snap := testSnapshot()
	snap.Version = "0.1.0"
	require.NoError(t, fsV1.Save(snap))

	// Same version validates.
	assert.True(t, fsV1.Validate(snap.RepoPath, snap.CommitHash))

	// Different version fails.
	fsV2, err := NewFileStore(dir, "0.2.0")
	require.NoError(t, err)
	assert.False(t, fsV2.Validate(snap.RepoPath, snap.CommitHash))
}

func TestFileStore_Evict(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "0.1.0")
	require.NoError(t, err)

	snap := testSnapshot()
	require.NoError(t, fs.Save(snap))
	assert.True(t, fs.Check(snap.RepoPath, snap.CommitHash))

	require.NoError(t, fs.Evict(snap.RepoPath, snap.CommitHash))
	assert.False(t, fs.Check(snap.RepoPath, snap.CommitHash))
}

func TestFileStore_Load_NotFound(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "0.1.0")
	require.NoError(t, err)

	_, err = fs.Load("/nonexistent", "abc123")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestFileStore_MetaWithSliceTypes(t *testing.T) {
	dir := t.TempDir()
	fs, err := NewFileStore(dir, "0.1.0")
	require.NoError(t, err)

	snap := &Snapshot{
		Version:    "0.1.0",
		RepoPath:   "/tmp/test",
		CommitHash: "def789",
		IndexedAt:  time.Now().Truncate(time.Second),
		Nodes: []*graph.Node{
			{
				ID: "iface.go::Reader", Kind: graph.KindInterface, Name: "Reader",
				FilePath: "iface.go", Language: "go",
				Meta: map[string]any{"methods": []string{"Read", "Close"}},
			},
		},
		FileMtimes: map[string]int64{"iface.go": 1700000000},
	}

	require.NoError(t, fs.Save(snap))

	loaded, err := fs.Load(snap.RepoPath, snap.CommitHash)
	require.NoError(t, err)

	methods, ok := loaded.Nodes[0].Meta["methods"].([]string)
	require.True(t, ok, "methods should deserialize as []string")
	assert.Equal(t, []string{"Read", "Close"}, methods)
}

func TestNopStore(t *testing.T) {
	var s NopStore
	assert.False(t, s.Check("x", "y"))
	_, err := s.Load("x", "y")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.NoError(t, s.Save(testSnapshot()))
	assert.False(t, s.Validate("x", "y"))
	assert.NoError(t, s.Evict("x", "y"))
	assert.NoError(t, s.Close())
}
