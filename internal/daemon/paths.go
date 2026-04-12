package daemon

import (
	"os"
	"path/filepath"
	"runtime"
)

// SocketPath returns the Unix socket path the daemon listens on.
//
// Order of preference:
//  1. $GORTEX_DAEMON_SOCKET — explicit override (tests, custom deployments).
//  2. $XDG_RUNTIME_DIR/gortex.sock — Linux standard for user runtime files.
//     This path is cleaned automatically on logout and has sensible perms.
//  3. $HOME/.cache/gortex/daemon.sock — universal fallback used on macOS
//     (which has no $XDG_RUNTIME_DIR) and on Linux without systemd-logind.
//
// Unix socket paths have a length limit (~104 bytes on macOS, 108 on Linux).
// We don't enforce that here — the listener will fail loudly if the path
// is too long, and the fix is to set $GORTEX_DAEMON_SOCKET to a shorter
// path rather than silently truncating.
func SocketPath() string {
	if override := os.Getenv("GORTEX_DAEMON_SOCKET"); override != "" {
		return override
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" && runtime.GOOS == "linux" {
		return filepath.Join(rt, "gortex.sock")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to /tmp as a last resort; the daemon must start somewhere.
		return filepath.Join(os.TempDir(), "gortex.sock")
	}
	return filepath.Join(home, ".cache", "gortex", "daemon.sock")
}

// PIDFilePath returns the path of the daemon PID file. The daemon writes
// this on startup and removes it on graceful shutdown. Staleness detection
// (for crashed daemons that never removed their PID) is a `kill -0` check.
func PIDFilePath() string {
	if override := os.Getenv("GORTEX_DAEMON_PIDFILE"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gortex-daemon.pid")
	}
	return filepath.Join(home, ".cache", "gortex", "daemon.pid")
}

// LogFilePath returns the path the daemon writes logs to when running in
// --detach mode. In foreground mode stderr is used instead.
func LogFilePath() string {
	if override := os.Getenv("GORTEX_DAEMON_LOGFILE"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gortex-daemon.log")
	}
	return filepath.Join(home, ".cache", "gortex", "daemon.log")
}

// SnapshotPath returns the path the daemon saves graph snapshots to on
// periodic saves and clean shutdown. Loaded on startup for fast cold starts.
func SnapshotPath() string {
	if override := os.Getenv("GORTEX_DAEMON_SNAPSHOT"); override != "" {
		return override
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "gortex-daemon.gob.gz")
	}
	return filepath.Join(home, ".cache", "gortex", "daemon.gob.gz")
}

// EnsureParentDir creates the parent directory of path with permissions
// 0o700 (user only). Daemon state files live under the user's cache dir
// and should not be world-readable.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o700)
}
