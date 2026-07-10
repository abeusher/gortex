package progress

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// TestGMarkShape pins the brand mark's geometry: 14 dots forming the
// dot-matrix G, accent at the tongue tip.
func TestGMarkShape(t *testing.T) {
	if len(gDots) != 14 {
		t.Fatalf("expected 14 dots in the mark, got %d", len(gDots))
	}
	want := map[[2]int]bool{
		{0, 1}: true, {0, 2}: true, {0, 3}: true,
		{1, 0}: true, {1, 4}: true,
		{2, 0}: true, {2, 2}: true, {2, 3}: true, {2, 4}: true,
		{3, 0}: true, {3, 4}: true,
		{4, 1}: true, {4, 2}: true, {4, 3}: true,
	}
	accents := 0
	for _, d := range gDots {
		if !want[[2]int{d.r, d.c}] {
			t.Errorf("unexpected dot at (%d,%d)", d.r, d.c)
		}
		if d.accent {
			accents++
			if d.r != 2 || d.c != 4 {
				t.Errorf("accent dot must be the tongue tip (2,4), got (%d,%d)", d.r, d.c)
			}
		}
	}
	if accents != 1 {
		t.Errorf("expected exactly one accent dot, got %d", accents)
	}
}

// distinctGlyphs makes lit / dim / accent dots distinguishable without color.
var distinctGlyphs = glyphSet{Dot: "X", DotDim: "."}

func renderLogoGlyphs(frac float64, tick, glint int) string {
	plain := lipgloss.NewStyle()
	lines := logoLines(plain, plain, plain, distinctGlyphs, frac, tick, glint)
	return strings.Join(lines[:], "\n")
}

func TestLogoProgressiveLighting(t *testing.T) {
	// frac=0: only the accent tip is bright.
	if got := strings.Count(renderLogoGlyphs(0, 0, -1), "X"); got != 1 {
		t.Errorf("frac=0 must light only the accent tip, got %d bright dots", got)
	}
	// frac=1: every dot is bright.
	if got := strings.Count(renderLogoGlyphs(1, 0, -1), "X"); got != 14 {
		t.Errorf("frac=1 must light all 14 dots, got %d", got)
	}
	// Lighting is monotonic in frac.
	prev := 0
	for _, frac := range []float64{0, 0.25, 0.5, 0.75, 1} {
		n := strings.Count(renderLogoGlyphs(frac, 0, -1), "X")
		if n < prev {
			t.Errorf("lighting regressed at frac=%.2f: %d < %d", frac, n, prev)
		}
		prev = n
	}
}

func TestLogoMarqueeWindow(t *testing.T) {
	// Indeterminate mode lights the accent tip plus a 4-dot window.
	if got := strings.Count(renderLogoGlyphs(-1, 0, -1), "X"); got != 5 {
		t.Errorf("marquee tick 0 must light tip + 4-dot window, got %d", got)
	}
	// The window moves with the tick.
	if renderLogoGlyphs(-1, 0, -1) == renderLogoGlyphs(-1, 3, -1) {
		t.Error("marquee frames must differ across ticks")
	}
}

func TestLogoLinesWidthStable(t *testing.T) {
	plain := lipgloss.NewStyle()
	for _, frac := range []float64{-1, 0, 0.5, 1} {
		lines := logoLines(plain, plain, plain, unicodeGlyphs, frac, 2, -1)
		for i, ln := range lines {
			if w := ansi.StringWidth(ln); w != MeshLogoWidth() {
				t.Errorf("frac=%.1f line %d width = %d, want %d (%q)", frac, i, w, MeshLogoWidth(), ln)
			}
		}
	}
}

func TestMeshLogoCompat(t *testing.T) {
	t.Setenv("GORTEX_ASCII", "")
	t.Setenv("GORTEX_UNICODE", "1")
	logo := MeshLogo(0)
	lines := strings.Split(logo, "\n")
	if len(lines) != MeshLogoLines() {
		t.Fatalf("MeshLogo renders %d lines, want %d", len(lines), MeshLogoLines())
	}
	for i, ln := range lines {
		if w := ansi.StringWidth(ln); w != MeshLogoWidth() {
			t.Errorf("line %d width = %d, want %d", i, w, MeshLogoWidth())
		}
	}
	// Static banner logo is fully lit: all 14 dots present.
	if got := strings.Count(ansi.Strip(logo), "●"); got != 14 {
		t.Errorf("static mark must show all 14 dots, got %d", got)
	}
}
