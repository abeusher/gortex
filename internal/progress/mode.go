package progress

import (
	"io"
	"os"
	"sync/atomic"

	"golang.org/x/term"
)

// IsTTY reports whether w is backed by a terminal file descriptor.
func IsTTY(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func envDisablesColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return true
	}
	if t := os.Getenv("TERM"); t == "dumb" {
		return true
	}
	return false
}

// animationAllowed decides whether w can host the animated renderer. The
// contract matches the --no-progress help text: NO_COLOR, TERM=dumb, a
// non-TTY writer, and CI all force plain output. On Windows it additionally
// requires the console to accept VT processing (legacy conhost without VT
// gets plain text rather than raw escape soup). GORTEX_FORCE_ANIMATION=1
// overrides everything — it exists for demos and PTY-less frame capture.
func animationAllowed(w io.Writer) bool {
	if envFlag("GORTEX_FORCE_ANIMATION") {
		return true
	}
	if !IsTTY(w) {
		return false
	}
	if envDisablesColor() {
		return false
	}
	if os.Getenv("CI") != "" {
		return false
	}
	return enableVT(w)
}

// cursorHiddenOnStderr tracks whether an animated tracker writing to the
// process stderr currently has the cursor hidden. RestoreTerminal uses it to
// undo the hide from exit paths that bypass a tracker's own finish (fatal
// errors, panics unwinding main).
var cursorHiddenOnStderr atomic.Bool

// RestoreTerminal re-shows the cursor and resets SGR state on stderr if an
// animated tracker left the cursor hidden. Safe to call unconditionally and
// repeatedly; a no-op when nothing needs restoring. Wire it into every
// process exit path that can interrupt a live animation.
func RestoreTerminal() {
	if cursorHiddenOnStderr.CompareAndSwap(true, false) {
		_, _ = os.Stderr.WriteString(ansiShowCursor + ansiReset)
	}
}

func markCursorHidden(w io.Writer)   { setCursorFlag(w, true) }
func markCursorRestored(w io.Writer) { setCursorFlag(w, false) }

func setCursorFlag(w io.Writer, hidden bool) {
	if f, ok := w.(*os.File); ok && f == os.Stderr {
		cursorHiddenOnStderr.Store(hidden)
	}
}
