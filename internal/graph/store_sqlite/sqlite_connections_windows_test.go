//go:build windows

package store_sqlite

import (
	"strings"
	"testing"
)

// A Windows drive-letter path must render as file:///C:/... — commit
// 1ba5c8b's url.URL construction emitted file:C:/..., which SQLite rejects
// with "invalid uri authority: C:", so a daemon with a fresh default store
// could not start on Windows at all.
func TestSqliteDSNWindowsDriveLetterPath(t *testing.T) {
	dsn := sqliteDSN(`C:\tmp\gortex-test\store.sqlite`, "mode=ro")
	if !strings.HasPrefix(dsn, "file:///") {
		t.Fatalf("windows drive-letter DSN must start with file:/// (got %q)", dsn)
	}
	if strings.Contains(dsn, "file:C:") || strings.Contains(dsn, "file://C:") {
		t.Fatalf("drive letter leaked into URI authority position: %q", dsn)
	}
}
