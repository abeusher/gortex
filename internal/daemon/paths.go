package daemon

import (
	"os"
	"path/filepath"
	"runtime"
)

// stateDir returns the directory the daemon keeps its runtime state in
// (socket, PID file, logs, snapshot) and whether it could be resolved.
//
//   - Windows: %LocalAppData%\gortex (via os.UserCacheDir).
//   - macOS / Linux: $HOME/.cache/gortex.
//
// The boolean is false when the home / cache directory can't be
// resolved at all, in which case callers fall back to the temp dir.
func stateDir() (string, bool) {
	if runtime.GOOS == "windows" {
		dir, err := os.UserCacheDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(dir, "gortex"), true
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(home, ".cache", "gortex"), true
}

// SocketPath returns the socket path the daemon listens on. The socket
// is an AF_UNIX socket on every supported OS — Windows has supported
// AF_UNIX since Windows 10 1803, so the same transport works there.
//
// Order of preference:
//  1. $GORTEX_DAEMON_SOCKET — explicit override (tests, custom deployments).
//  2. $XDG_RUNTIME_DIR/gortex.sock — Linux standard for user runtime files.
//     This path is cleaned automatically on logout and has sensible perms.
//  3. The per-user state dir — $HOME/.cache/gortex on macOS/Linux,
//     %LocalAppData%\gortex on Windows.
//
// AF_UNIX socket paths have a length limit (~104 bytes on macOS, 108 on
// Linux and Windows). We don't enforce that here — the listener fails
// loudly if the path is too long, and the fix is to set
// $GORTEX_DAEMON_SOCKET to a shorter path rather than silently truncating.
func SocketPath() string {
	if override := os.Getenv("GORTEX_DAEMON_SOCKET"); override != "" {
		return override
	}
	if rt := os.Getenv("XDG_RUNTIME_DIR"); rt != "" && runtime.GOOS == "linux" {
		return filepath.Join(rt, "gortex.sock")
	}
	if dir, ok := stateDir(); ok {
		return filepath.Join(dir, "daemon.sock")
	}
	// Fall back to the temp dir as a last resort; the daemon must start
	// somewhere.
	return filepath.Join(os.TempDir(), "gortex.sock")
}

// PIDFilePath returns the path of the daemon PID file. The daemon writes
// this on startup and removes it on graceful shutdown. Staleness detection
// (for crashed daemons that never removed their PID) is a process-liveness
// probe — see platform.ProcessAlive.
func PIDFilePath() string {
	if override := os.Getenv("GORTEX_DAEMON_PIDFILE"); override != "" {
		return override
	}
	if dir, ok := stateDir(); ok {
		return filepath.Join(dir, "daemon.pid")
	}
	return filepath.Join(os.TempDir(), "gortex-daemon.pid")
}

// LogFilePath returns the path the daemon writes logs to when running in
// --detach mode. In foreground mode stderr is used instead.
func LogFilePath() string {
	if override := os.Getenv("GORTEX_DAEMON_LOGFILE"); override != "" {
		return override
	}
	if dir, ok := stateDir(); ok {
		return filepath.Join(dir, "daemon.log")
	}
	return filepath.Join(os.TempDir(), "gortex-daemon.log")
}

// SnapshotPath returns the path the daemon saves graph snapshots to on
// periodic saves and clean shutdown. Loaded on startup for fast cold starts.
func SnapshotPath() string {
	if override := os.Getenv("GORTEX_DAEMON_SNAPSHOT"); override != "" {
		return override
	}
	if dir, ok := stateDir(); ok {
		return filepath.Join(dir, "daemon.gob.gz")
	}
	return filepath.Join(os.TempDir(), "gortex-daemon.gob.gz")
}

// EnsureParentDir creates the parent directory of path with permissions
// 0o700 (user only). Daemon state files live under the user's cache dir
// and should not be world-readable. The mode is advisory on Windows,
// where filesystem ACLs already scope %LocalAppData% to the user.
func EnsureParentDir(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o700)
}
