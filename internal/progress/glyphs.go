package progress

import (
	"os"
	"runtime"

	"github.com/charmbracelet/lipgloss"
)

// glyphSet is the small set of display glyphs that differ between a UTF-8
// terminal and a legacy OEM / ASCII one: the success / failure markers, the
// status dot, the mid-dot separator, and the box-drawing charset. Gortex
// renders more box-drawing than a check/cross-only CLI (the rounded card
// border), so the ASCII fallback has to cover the whole border too. The
// live tracker adds the progress-bar cells, the pending marker, and the
// busy / ready status dots to the same fallback contract.
type glyphSet struct {
	OK          string
	Fail        string
	Dot         string
	DotDim      string
	Pending     string
	Sep         string
	Dash        string
	Ellipsis    string
	BarFull     string
	BarEmpty    string
	StatusBusy  string
	StatusReady string
	Border      lipgloss.Border
}

var (
	unicodeGlyphs = glyphSet{
		OK:          "✓",
		Fail:        "✗",
		Dot:         "●",
		DotDim:      "●",
		Pending:     "·",
		Sep:         "·",
		Dash:        "—",
		Ellipsis:    "…",
		BarFull:     "█",
		BarEmpty:    "░",
		StatusBusy:  "◐",
		StatusReady: "●",
		Border:      lipgloss.RoundedBorder(),
	}
	asciiGlyphs = glyphSet{
		OK:          "+",
		Fail:        "x",
		Dot:         "*",
		DotDim:      "o",
		Pending:     ".",
		Sep:         "-",
		Dash:        "-",
		Ellipsis:    "...",
		BarFull:     "#",
		BarEmpty:    "-",
		StatusBusy:  "o",
		StatusReady: "*",
		Border:      lipgloss.ASCIIBorder(),
	}

	brailleSpin = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	asciiSpin   = []string{"|", "/", "-", "\\"}
)

// activeGlyphs returns the glyph set appropriate to the current terminal:
// UTF-8 box-drawing / check glyphs when it can render them, ASCII otherwise.
// Resolved per call so a runtime override (or a test) takes effect immediately;
// the cost is a couple of env reads plus, on Windows only, one cheap codepage
// syscall.
func activeGlyphs() glyphSet {
	if supportsUnicode() {
		return unicodeGlyphs
	}
	return asciiGlyphs
}

// spinFrames returns the animated-spinner frame cycle. Braille frames need
// font coverage that the check / box-drawing glyphs don't: legacy conhost
// fonts (Consolas on a CP65001 console) render ✓ and █ but draw braille as
// boxes, so on Windows the braille cycle is reserved for terminals that
// declare themselves modern (Windows Terminal, ConEmu, an inherited TERM).
// Everything else animates with the four-frame ASCII cycle.
func spinFrames() []string {
	if !supportsUnicode() {
		return asciiSpin
	}
	if runtime.GOOS == "windows" && !windowsModernTerminal() {
		return asciiSpin
	}
	return brailleSpin
}

// windowsModernTerminal reports whether the process runs under a Windows
// terminal with full glyph coverage: Windows Terminal (WT_SESSION), ConEmu
// (ConEmuANSI=ON), or an environment that carries a unix-style TERM (mintty,
// msys, ssh sessions).
func windowsModernTerminal() bool {
	if os.Getenv("WT_SESSION") != "" {
		return true
	}
	if os.Getenv("ConEmuANSI") == "ON" {
		return true
	}
	return os.Getenv("TERM") != ""
}

// supportsUnicode reports whether the active terminal can render the UTF-8
// box-drawing / check glyphs. Explicit env overrides win (GORTEX_ASCII opt-out,
// GORTEX_UNICODE opt-in); a linux virtual console (TERM=linux, CP437-ish) and a
// non-UTF-8 Windows console codepage both fall back to ASCII. Every other
// terminal is assumed UTF-8-capable, the modern default.
func supportsUnicode() bool {
	if envFlag("GORTEX_ASCII") {
		return false
	}
	if envFlag("GORTEX_UNICODE") {
		return true
	}
	if os.Getenv("TERM") == "linux" {
		return false
	}
	if runtime.GOOS == "windows" {
		return windowsConsoleUTF8()
	}
	return true
}

// envFlag reports whether the named env var is set to a truthy value.
func envFlag(name string) bool {
	switch os.Getenv(name) {
	case "1", "true", "TRUE", "yes", "on":
		return true
	}
	return false
}
