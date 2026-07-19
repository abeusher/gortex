//go:build windows

package progress

import (
	"io"

	"golang.org/x/sys/windows"
)

// enableVT switches the console behind w into VT-processing mode so the
// animated renderer's escape sequences are interpreted instead of echoed.
// Windows Terminal and recent conhost builds accept it; a console that
// refuses (pre-1511 conhost, exotic redirectors) returns false and the
// caller stays on plain output.
func enableVT(w io.Writer) bool {
	f, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
