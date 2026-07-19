package progress

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// forceUnicode pins the glyph set so assertions are stable regardless of the
// host environment.
func forceUnicode(t *testing.T) {
	t.Helper()
	t.Setenv("GORTEX_ASCII", "")
	t.Setenv("GORTEX_UNICODE", "1")
}

// newAnimatedTestTracker returns a tracker that renders the animated protocol
// into buf: animation forced (no TTY in tests), the frame ticker frozen so
// paints happen only at Start / Logf / finish, and a fixed terminal size.
func newAnimatedTestTracker(t *testing.T, buf *bytes.Buffer, w, h int, opts ...TrackerOption) *Tracker {
	t.Helper()
	t.Setenv("GORTEX_FORCE_ANIMATION", "1")
	tr := NewTracker(buf, opts...)
	tr.fps = time.Hour
	tr.sizeFn = func() (int, int) { return w, h }
	return tr
}

func TestTrackerPlainExplicitStepsLifecycle(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := NewTracker(&buf, WithoutAnimation())
	tr.Start("gortex init")

	scan := tr.AddStep("scan repository")
	parse := tr.AddStep("parse sources")
	analyze := tr.AddStep("analyze codebase")
	broken := tr.AddStep("configure adapters")
	if strings.Contains(buf.String(), "scan repository") {
		t.Fatalf("pending steps must stay silent in plain mode, got:\n%s", buf.String())
	}

	tr.StartStep("scan repository")
	scan.Progress(1338, 0)
	scan.SetUnit("files")
	scan.DoneAs("files discovered")

	tr.StartStep("parse sources")
	parse.Progress(48210, 0)
	parse.SetUnit("symbols")
	parse.Done()

	analyze.Skip("disabled by flag")

	tr.StartStep("configure adapters")
	broken.Fail(errors.New("no adapters selected"))

	tr.Done("ready", "indexed 1,338 files · 48,210 symbols")

	out := buf.String()
	wants := []string{
		"  gortex init",
		"  scan repository ...",
		"  ✓ files discovered · 1,338 files",
		"  parse sources ...",
		"  ✓ parse sources · 48,210 symbols",
		"  · analyze codebase — skipped: disabled by flag",
		"  configure adapters ...",
		"  ✗ configure adapters: no adapters selected",
		"  ✓ ready — indexed 1,338 files · 48,210 symbols",
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("plain output missing %q\n--- got ---\n%s", w, out)
		}
	}
}

func TestTrackerPlainHeartbeatThrottled(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := NewTracker(&buf, WithoutAnimation())
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.now = func() time.Time { return clock }
	tr.Start("indexing")

	s := tr.StartStep("resolving references")
	s.Progress(1, 10)
	s.Progress(2, 10) // within the throttle window — silent
	if got := strings.Count(buf.String(), "resolving references"); got != 1 {
		t.Fatalf("ticks inside the throttle window must not print, got %d occurrences:\n%s", got, buf.String())
	}

	clock = clock.Add(11 * time.Second)
	s.Progress(5, 10)
	if !strings.Contains(buf.String(), "    resolving references · 5 / 10 (11s)") {
		t.Errorf("expected a heartbeat line after the throttle window, got:\n%s", buf.String())
	}

	s.Progress(6, 10) // window reset — silent again
	if got := strings.Count(buf.String(), "resolving references"); got != 2 {
		t.Errorf("heartbeat must rearm the throttle, got %d occurrences:\n%s", got, buf.String())
	}
}

func TestTrackerAnimatedFrameProtocol(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, 80, 24)
	tr.Start("gortex enrich")

	s := tr.StartStep("stamping blame")
	s.Progress(120, 400)
	s.SetUnit("nodes")
	s.Done()
	tr.Done("", "400 nodes stamped")

	out := buf.String()
	if !strings.HasPrefix(out, ansiHideCursor) {
		t.Errorf("animated stream must open by hiding the cursor, got prefix %q", out[:min(20, len(out))])
	}
	if !strings.Contains(out, ansiSyncStart) || !strings.Contains(out, ansiSyncEnd) {
		t.Error("frames must be wrapped in synchronized-update brackets")
	}
	if strings.Count(out, ansiSyncStart) != strings.Count(out, ansiSyncEnd) {
		t.Error("unbalanced synchronized-update brackets")
	}
	if !strings.Contains(out, ansiShowCursor) {
		t.Error("final frame must re-show the cursor")
	}
	if !strings.Contains(out, ansiReset) {
		t.Error("final frame must reset SGR state")
	}
	// The final frame carries the completed checklist and the summary.
	for _, w := range []string{"✓ stamping blame", "400 nodes stamped", "gortex enrich"} {
		if !strings.Contains(ansi.Strip(out), w) {
			t.Errorf("final frame missing %q\n--- stripped ---\n%s", w, ansi.Strip(out))
		}
	}
	// Every frame line terminates with clear-to-EOL + CRLF so repaints can
	// never leave residue and the cursor arithmetic stays exact.
	frames := strings.Split(out, ansiSyncStart)
	for _, fr := range frames[1:] {
		body := strings.TrimSuffix(fr, ansiSyncEnd)
		for _, ln := range strings.Split(body, "\r\n") {
			if strings.Contains(ln, "\n") {
				t.Errorf("bare LF inside a frame line: %q", ln)
			}
		}
	}
}

func TestTrackerAnimatedLinesNeverExceedWidth(t *testing.T) {
	forceUnicode(t)
	const width = 34
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, width, 24)
	tr.Start("gortex enrich with an extremely long command title that cannot fit")

	s := tr.StartStep("a step label that is much longer than the terminal width for sure")
	s.Progress(123456, 654321)
	s.SetUnit("symbols")
	s.Note("annotation that also overflows the width")
	tr.Done("", "")

	for _, raw := range strings.Split(buf.String(), "\r\n") {
		if w := ansi.StringWidth(raw); w > width-1 {
			t.Errorf("frame line exceeds clamp width %d (got %d): %q", width-1, w, ansi.Strip(raw))
		}
	}
}

func TestTrackerOverflowRetiresFinishedRows(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, 80, 8) // maxRegion = 6
	tr.Start("bulk run")
	for i := 0; i < 10; i++ {
		s := tr.StartStep(fmt.Sprintf("phase-%02d", i))
		s.Done()
	}
	tr.Done("", "")

	stripped := ansi.Strip(buf.String())
	for i := 0; i < 10; i++ {
		label := fmt.Sprintf("phase-%02d", i)
		if !strings.Contains(stripped, label) {
			t.Errorf("finished step %s lost during overflow handling\n--- got ---\n%s", label, stripped)
		}
	}
	// The final live region must fit the clamped height. Region lines are the
	// ones terminated by clear-to-EOL; retired rows above the region are
	// written without it.
	frames := strings.Split(buf.String(), ansiSyncStart)
	last := frames[len(frames)-1]
	if lines := strings.Count(last, ansiClearEOL); lines > 6 {
		t.Errorf("final live region paints %d lines, exceeding the clamp of 6", lines)
	}
}

func TestTrackerLogfWritesAboveRegion(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, 80, 24)
	tr.Start("watching")
	tr.Logf("warning: adapter %s misbehaved", "codex")
	tr.Done("", "")

	out := ansi.Strip(buf.String())
	warnIdx := strings.Index(out, "warning: adapter codex misbehaved")
	if warnIdx < 0 {
		t.Fatalf("log line missing from animated stream:\n%s", out)
	}
	// The region is repainted after the log line, so the title must appear
	// again later in the stream.
	if !strings.Contains(out[warnIdx:], "watching") {
		t.Errorf("live region not repainted after log line:\n%s", out)
	}
}

func TestTrackerReporterBuildsChecklistAnimated(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, 100, 24)
	tr.Start("indexing repository")
	tr.Report("discovering files", 0, 0)
	tr.Report("discovering files", 900, 0)
	tr.Report("parsing", 10, 900)
	tr.Report("parsing", 900, 900)
	tr.Done("", "")

	out := ansi.Strip(buf.String())
	if !strings.Contains(out, "✓ discovering files · 900") {
		t.Errorf("previous stage must complete with its final counter:\n%s", out)
	}
	if !strings.Contains(out, "✓ parsing · 900") {
		t.Errorf("last stage must be completed by Done:\n%s", out)
	}
}

func TestTrackerBannerPresetRendersLogoAndStatus(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := newAnimatedTestTracker(t, &buf, 100, 30, WithLogo())
	tr.Start("gortex init")
	tr.AddStep("index repository")
	tr.AddStep("configure adapters")
	tr.StartStep("index repository").Done()
	tr.SetStatus("configuring")
	tr.StartStep("configure adapters").Done()
	tr.Done("ready", "indexed everything")

	out := ansi.Strip(buf.String())
	if got := strings.Count(out, "●"); got < 10 {
		t.Errorf("banner preset must render the dot-matrix mark, found %d dots:\n%s", got, out)
	}
	for _, w := range []string{"gortex init", "ready", "indexed everything", "✓ index repository", "✓ configure adapters"} {
		if !strings.Contains(out, w) {
			t.Errorf("banner output missing %q:\n%s", w, out)
		}
	}
}

func TestTrackerConcurrentReportsAreSafe(t *testing.T) {
	forceUnicode(t)
	t.Setenv("GORTEX_FORCE_ANIMATION", "1")
	var buf bytes.Buffer
	tr := NewTracker(&buf)
	tr.fps = 2 * time.Millisecond
	tr.sizeFn = func() (int, int) { return 80, 24 }
	tr.Start("stress")

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tr.Report(fmt.Sprintf("stage-%d", g%3), i, 200)
			}
		}(g)
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond) // let the ticker paint at least once
	tr.Done("", "")
	if !strings.Contains(ansi.Strip(buf.String()), "stress") {
		t.Error("expected output from the concurrent run")
	}
}

func TestTrackerFinishIdempotent(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	tr := NewTracker(&buf, WithoutAnimation())
	tr.Start("run")
	tr.Done("", "")
	tr.Done("", "")
	tr.Fail(errors.New("late"))

	out := buf.String()
	if got := strings.Count(out, "✓"); got != 1 {
		t.Errorf("expected exactly one ✓, got %d:\n%s", got, out)
	}
	if strings.Contains(out, "✗") {
		t.Errorf("Fail after Done must be ignored:\n%s", out)
	}
}

func TestSpinnerCompatStatusRidesFinishLine(t *testing.T) {
	forceUnicode(t)
	var buf bytes.Buffer
	sp := NewSpinner(&buf)
	sp.Disable()
	sp.Start("Enriched via daemon")
	sp.Set("", "42 nodes stamped")
	sp.Done()

	if !strings.Contains(buf.String(), "✓ Enriched via daemon — 42 nodes stamped") {
		t.Errorf("finish line must carry the last status, got:\n%s", buf.String())
	}
}
