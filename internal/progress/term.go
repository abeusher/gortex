package progress

import (
	"fmt"
	"io"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"golang.org/x/term"
)

// ANSI control sequences used by the live renderer. Private-mode toggles that
// a terminal doesn't implement (the ?2026 synchronized-update pair) are
// ignored by spec, so they are safe to emit unconditionally.
const (
	ansiHideCursor = "\x1b[?25l"
	ansiShowCursor = "\x1b[?25h"
	ansiClearEOL   = "\x1b[K"
	ansiClearBelow = "\x1b[J"
	ansiSyncStart  = "\x1b[?2026h"
	ansiSyncEnd    = "\x1b[?2026l"
	ansiReset      = "\x1b[0m"
)

// ansiUp moves the cursor n lines up (column unchanged). n <= 0 is a no-op.
func ansiUp(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dA", n)
}

// colorProfileFor resolves the color depth for the animated renderer bound to
// w. termenv's per-platform detection covers COLORTERM / TERM on unix and the
// console-API probes on Windows; a writer that isn't a real terminal (frame
// capture under GORTEX_FORCE_ANIMATION) degrades to plain-byte styles unless
// the caller overrides the profile explicitly.
func colorProfileFor(w io.Writer) termenv.Profile {
	return termenv.NewOutput(w).ColorProfile()
}

// termSize returns the terminal dimensions behind w, falling back to a
// conservative 80×24 when w has no descriptor or the ioctl fails (captures,
// tests). Queried per frame so a live resize re-clamps the next repaint
// without any platform-specific resize signal.
func termSize(w io.Writer) (width, height int) {
	width, height = 80, 24
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return width, height
	}
	cw, ch, err := term.GetSize(int(f.Fd()))
	if err != nil || cw <= 0 || ch <= 0 {
		return width, height
	}
	return cw, ch
}

// visibleWidth measures the printable cell width of a styled line,
// ANSI-aware.
func visibleWidth(s string) int { return ansi.StringWidth(s) }

// clampLine hard-truncates a styled line to width cells, ANSI-aware, so a
// frame line can never soft-wrap. Soft wrap is fatal to a repaint renderer:
// one wrapped line desyncs the cursor-up arithmetic and every later frame
// smears. The ellipsis tail marks the cut.
func clampLine(s string, width int, ellipsis string) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, ellipsis)
}
