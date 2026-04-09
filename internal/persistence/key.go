package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
)

// CacheKey produces a filesystem-safe directory name from repo path + commit hash.
func CacheKey(repoPath, commitHash string) string {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		abs = repoPath
	}
	h := sha256.Sum256([]byte(abs))
	pathPart := hex.EncodeToString(h[:6])

	commitPart := commitHash
	if len(commitPart) > 12 {
		commitPart = commitPart[:12]
	}

	return pathPart + "_" + commitPart
}
