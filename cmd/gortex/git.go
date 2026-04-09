package main

import (
	"bytes"
	"os/exec"
	"strings"
)

// gitCommitHash returns the HEAD commit hash for the repository at dir,
// or an empty string if git is unavailable or the directory is not a repo.
func gitCommitHash(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}
