package persistence

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func init() {
	// Register concrete types that appear in Node.Meta / Edge.Meta map[string]any.
	gob.Register(map[string]any{})
	gob.Register([]any{})
	gob.Register([]string{})
}

const (
	snapshotFile = "snapshot.gob.gz"
	versionFile  = ".version"
)

// FileStore persists snapshots as gob+gzip files in a directory hierarchy.
// Layout: {dir}/{cacheKey}/snapshot.gob.gz + .version
type FileStore struct {
	dir     string
	version string
}

// NewFileStore creates a file-based persistence store.
// If dir is empty, defaults to ~/.cache/gortex/.
func NewFileStore(dir, version string) (*FileStore, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("persistence: resolve home dir: %w", err)
		}
		dir = filepath.Join(home, ".cache", "gortex")
	}
	return &FileStore{dir: dir, version: version}, nil
}

func (fs *FileStore) entryDir(repoPath, commitHash string) string {
	return filepath.Join(fs.dir, CacheKey(repoPath, commitHash))
}

func (fs *FileStore) Check(repoPath, commitHash string) bool {
	dir := fs.entryDir(repoPath, commitHash)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, versionFile))
	return err == nil
}

func (fs *FileStore) Validate(repoPath, commitHash string) bool {
	dir := fs.entryDir(repoPath, commitHash)
	data, err := os.ReadFile(filepath.Join(dir, versionFile))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == fs.version
}

func (fs *FileStore) Load(repoPath, commitHash string) (*Snapshot, error) {
	dir := fs.entryDir(repoPath, commitHash)
	f, err := os.Open(filepath.Join(dir, snapshotFile))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("persistence: open snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("persistence: gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	var snap Snapshot
	if err := gob.NewDecoder(gz).Decode(&snap); err != nil {
		return nil, fmt.Errorf("persistence: gob decode: %w", err)
	}

	return &snap, nil
}

func (fs *FileStore) Save(snap *Snapshot) error {
	dir := fs.entryDir(snap.RepoPath, snap.CommitHash)

	// Remove old entry if it exists.
	_ = os.RemoveAll(dir)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("persistence: mkdir: %w", err)
	}

	// Write snapshot.
	f, err := os.Create(filepath.Join(dir, snapshotFile))
	if err != nil {
		return fmt.Errorf("persistence: create snapshot: %w", err)
	}

	gz := gzip.NewWriter(f)
	enc := gob.NewEncoder(gz)

	if err := enc.Encode(snap); err != nil {
		_ = gz.Close()
		_ = f.Close()
		return fmt.Errorf("persistence: gob encode: %w", err)
	}

	if err := gz.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("persistence: gzip close: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("persistence: file close: %w", err)
	}

	// Write version file.
	if err := os.WriteFile(filepath.Join(dir, versionFile), []byte(fs.version), 0o644); err != nil {
		return fmt.Errorf("persistence: write version: %w", err)
	}

	return nil
}

func (fs *FileStore) Evict(repoPath, commitHash string) error {
	return os.RemoveAll(fs.entryDir(repoPath, commitHash))
}

func (fs *FileStore) Close() error { return nil }
