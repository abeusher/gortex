package progress

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Tracker is the CLI's live progress surface: a flicker-free animated region
// on a capable terminal, and clean line-oriented text everywhere else (pipes,
// CI, NO_COLOR, TERM=dumb, --no-progress, a Windows console without VT).
//
// The animated region is composed of an optional brand header (the gortex
// mark lighting up with overall progress plus a title and a live status
// line) followed by one row per step — pending, active (spinner, counters,
// progress bar), done (check, final count, duration), failed, or skipped.
// The whole region repaints at a fixed cadence inside terminal synchronized-
// update brackets with every line hard-clamped to the terminal width, so the
// frame arithmetic can never desync — the classic source of spinner ghosting.
//
// A Tracker is safe for concurrent use. Hot loops may call Report thousands
// of times per second: updates only mutate state under the lock, and the
// render goroutine samples that state at the frame rate.
type Tracker struct {
	mu sync.Mutex
	w  io.Writer
	st styleSet

	animated bool
	logo     bool
	fps      time.Duration

	title  string
	status string

	steps       []*Step
	hasExplicit bool

	started    bool
	finished   bool
	failed     bool
	finErr     error
	finHead    string
	finSummary string

	startAt time.Time
	stopAt  time.Time
	tick    int

	lastLines  int
	flushQueue []string
	stopCh     chan struct{}
	loopDone   chan struct{}

	// Test seams.
	sizeFn func() (int, int)
	now    func() time.Time
}

// stepStatus is the lifecycle of a single tracked step.
type stepStatus int

const (
	stepPending stepStatus = iota
	stepActive
	stepDone
	stepFailed
	stepSkipped
)

// Step is one row of the tracker: a phase of the overall run. Handles are
// returned by AddStep / StartStep and stay valid for the tracker's lifetime.
type Step struct {
	t *Tracker

	label     string
	doneLabel string
	status    stepStatus
	cur       int64
	total     int64
	unit      string
	note      string
	err       error
	skipNote  string
	auto      bool

	started  time.Time
	stopped  time.Time
	lastBeat time.Time
}

// TrackerOption customizes a Tracker at construction.
type TrackerOption func(*Tracker)

// WithLogo selects the banner preset: the animated region opens with the
// gortex mark, the title, and a live status line. Flagship multi-phase flows
// (init, install) use it; short single-op commands keep the compact preset.
func WithLogo() TrackerOption {
	return func(t *Tracker) { t.logo = true }
}

// WithoutAnimation forces plain line output regardless of terminal
// capabilities — the hook for the global --no-progress flag.
func WithoutAnimation() TrackerOption {
	return func(t *Tracker) { t.animated = false }
}

// NewTracker builds a tracker bound to w. Animation is auto-detected from the
// writer and environment (see animationAllowed); pass WithoutAnimation to
// force plain output.
func NewTracker(w io.Writer, opts ...TrackerOption) *Tracker {
	t := &Tracker{
		w:   w,
		fps: 80 * time.Millisecond,
		now: time.Now,
	}
	t.sizeFn = func() (int, int) { return termSize(w) }
	t.animated = animationAllowed(w)
	for _, o := range opts {
		o(t)
	}
	t.st = newStyleSet(w)
	return t
}

// Animated reports whether the tracker renders the live animated region.
func (t *Tracker) Animated() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.animated
}

// disable forces plain mode; effective only before Start.
func (t *Tracker) disable() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.started {
		t.animated = false
	}
}

// Start opens the surface with the given title and, on animated terminals,
// begins the render loop. Idempotent.
func (t *Tracker) Start(title string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return
	}
	t.started = true
	t.title = title
	t.startAt = t.now()
	if !t.animated {
		if title != "" {
			fmt.Fprintf(t.w, "  %s\n", title)
		}
		return
	}
	t.stopCh = make(chan struct{})
	t.loopDone = make(chan struct{})
	_, _ = io.WriteString(t.w, ansiHideCursor)
	markCursorHidden(t.w)
	t.paintLocked(false)
	go t.loop()
}

// SetTitle replaces the header title mid-run.
func (t *Tracker) SetTitle(title string) {
	if title == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.title = title
}

// SetStatus replaces the live status line (banner preset) or the inline
// detail after the title (compact preset). Plain mode records it silently —
// it surfaces on the finish line.
func (t *Tracker) SetStatus(status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status = status
}

// AddStep appends a pending step row — the design's "planned but not yet
// running" state. Planned steps also make overall progress computable, which
// drives the logo fill.
func (t *Tracker) AddStep(label string) *Step {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hasExplicit = true
	return t.newStepLocked(label, false)
}

// StartStep activates the pending step with the given label, creating it
// first when it was never planned. Returns the step handle.
func (t *Tracker) StartStep(label string) *Step {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hasExplicit = true
	for _, s := range t.steps {
		if s.status == stepPending && s.label == label {
			t.activateStepLocked(s)
			return s
		}
	}
	s := t.newStepLocked(label, false)
	t.activateStepLocked(s)
	return s
}

func (t *Tracker) newStepLocked(label string, auto bool) *Step {
	s := &Step{t: t, label: label, auto: auto}
	t.steps = append(t.steps, s)
	return s
}

func (t *Tracker) activateStepLocked(s *Step) {
	if s.status != stepPending {
		return
	}
	s.status = stepActive
	s.started = t.now()
	s.lastBeat = s.started
	if !t.animated && !t.finished {
		fmt.Fprintf(t.w, "  %s ...\n", s.label)
	}
}

// lastActiveLocked returns the most recently activated still-active step.
func (t *Tracker) lastActiveLocked() *Step {
	for i := len(t.steps) - 1; i >= 0; i-- {
		if t.steps[i].status == stepActive {
			return t.steps[i]
		}
	}
	return nil
}

// Progress updates the step's counters. total may be 0 when unknown.
func (s *Step) Progress(cur, total int64) {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	s.cur, s.total = cur, total
	s.plainBeatLocked()
}

// SetUnit sets the dim unit suffix rendered after the counter ("files",
// "symbols", "edges").
func (s *Step) SetUnit(unit string) {
	s.t.mu.Lock()
	defer s.t.mu.Unlock()
	s.unit = unit
}

// Note sets a dim annotation rendered after the counters — a sub-stage name,
// the language mix, the current file.
func (s *Step) Note(note string) {
	s.t.mu.Lock()
	defer s.t.mu.Unlock()
	s.note = note
}

// Done completes the step, keeping its running label.
func (s *Step) Done() { s.DoneAs("") }

// DoneAs completes the step and swaps the label for its past-tense form
// ("parsing sources" → "symbols extracted"), matching the design language.
func (s *Step) DoneAs(doneLabel string) {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completeStepLocked(s, doneLabel, nil)
}

// Skip marks a planned step as intentionally not run.
func (s *Step) Skip(reason string) {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	if s.status == stepDone || s.status == stepFailed || s.status == stepSkipped {
		return
	}
	s.status = stepSkipped
	s.skipNote = reason
	s.stopped = t.now()
	if !t.animated && !t.finished {
		note := ""
		if reason != "" {
			note = ": " + reason
		}
		fmt.Fprintf(t.w, "  %s %s %s skipped%s\n", t.st.g.Pending, s.label, t.st.g.Dash, note)
	}
}

// Fail marks the step failed with err.
func (s *Step) Fail(err error) {
	t := s.t
	t.mu.Lock()
	defer t.mu.Unlock()
	t.completeStepLocked(s, "", err)
}

func (t *Tracker) completeStepLocked(s *Step, doneLabel string, err error) {
	if s.status == stepDone || s.status == stepFailed || s.status == stepSkipped {
		return
	}
	if s.status == stepPending {
		s.started = t.now()
	}
	s.stopped = t.now()
	if err != nil {
		s.status = stepFailed
		s.err = err
		if !t.animated && !t.finished {
			fmt.Fprintf(t.w, "  %s %s: %v\n", t.st.g.Fail, s.label, err)
		}
		return
	}
	s.status = stepDone
	if doneLabel != "" {
		s.doneLabel = doneLabel
	}
	if !t.animated && !t.finished {
		fmt.Fprintf(t.w, "  %s %s%s%s\n",
			t.st.g.OK, s.finalLabel(), t.plainCounts(s), t.plainDuration(s.stopped.Sub(s.started)))
	}
}

func (s *Step) finalLabel() string {
	if s.doneLabel != "" {
		return s.doneLabel
	}
	return s.label
}

// plainBeatLocked emits a throttled heartbeat line for a long-running step in
// plain mode, so CI logs show liveness (and where a run hangs) without being
// flooded by per-item ticks.
const plainHeartbeatEvery = 10 * time.Second

func (s *Step) plainBeatLocked() {
	t := s.t
	if t.animated || t.finished || s.status != stepActive {
		return
	}
	now := t.now()
	if now.Sub(s.lastBeat) < plainHeartbeatEvery {
		return
	}
	s.lastBeat = now
	fmt.Fprintf(t.w, "    %s%s (%s)\n", s.label, t.plainCounts(s), fmtDurationCompact(now.Sub(s.started)))
}

// Report implements Reporter. Without explicit steps, every distinct stage
// label materializes as its own step row — the previous stage completes with
// its final counters and duration, the new one starts spinning. With explicit
// steps (an orchestrator owns the checklist), stage ticks feed the active
// step's note and counters instead.
func (t *Tracker) Report(stage string, current, total int) {
	if stage == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.finished {
		return
	}
	if t.hasExplicit {
		if s := t.lastActiveLocked(); s != nil {
			s.note = stage
			s.cur, s.total = int64(current), int64(total)
			s.plainBeatLocked()
			return
		}
		t.status = stage
		return
	}
	if s := t.lastActiveLocked(); s != nil && s.auto {
		if s.label == stage {
			s.cur, s.total = int64(current), int64(total)
			s.plainBeatLocked()
			return
		}
		t.completeStepLocked(s, "", nil)
	}
	s := t.newStepLocked(stage, true)
	t.activateStepLocked(s)
	s.cur, s.total = int64(current), int64(total)
}

// Println writes a permanent line above the live region (or straight through
// in plain mode). Use it for warnings and notes that must survive the
// animation instead of writing to the tracker's writer directly.
func (t *Tracker) Println(a ...any) {
	t.Logf("%s", strings.TrimRight(fmt.Sprintln(a...), "\n"))
}

// Logf is Println with formatting.
func (t *Tracker) Logf(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.animated || !t.started || t.finished {
		fmt.Fprintln(t.w, msg)
		return
	}
	t.flushQueue = append(t.flushQueue, strings.Split(msg, "\n")...)
	t.paintLocked(false)
}

// Done finishes the run successfully. headline defaults to the title
// (compact) or "ready" (banner); summary, when non-empty, is rendered as the
// closing line ("indexed 2,431 files · 48,210 symbols"). Idempotent, and a
// later Fail is ignored.
func (t *Tracker) Done(headline, summary string) { t.finish(headline, summary, nil) }

// Fail finishes the run with an error: the active step (if any) is marked
// failed and the header switches to the failure state. Idempotent.
func (t *Tracker) Fail(err error) { t.finish("", "", err) }

func (t *Tracker) finish(headline, summary string, err error) {
	t.mu.Lock()
	if t.finished || !t.started {
		t.mu.Unlock()
		return
	}
	t.finished = true
	t.failed = err != nil
	t.finErr = err
	t.finHead = headline
	t.finSummary = summary
	t.stopAt = t.now()

	// Close out the still-running step so the final frame has no orphan
	// spinner rows: success completes it, failure pins the error on it.
	if s := t.lastActiveLocked(); s != nil {
		wasAnimated := t.animated
		if err != nil {
			// Attribute the error to the step row; the header carries it too.
			s.status = stepFailed
			s.err = err
			s.stopped = t.stopAt
		} else if s.auto {
			s.status = stepDone
			s.stopped = t.stopAt
			if !wasAnimated {
				fmt.Fprintf(t.w, "  %s %s%s%s\n",
					t.st.g.OK, s.finalLabel(), t.plainCounts(s), t.plainDuration(t.stopAt.Sub(s.started)))
			}
		} else {
			s.status = stepDone
			s.stopped = t.stopAt
		}
	}

	if !t.animated {
		t.plainFinishLocked()
		t.mu.Unlock()
		return
	}

	t.paintLocked(true)
	markCursorRestored(t.w)
	stop := t.stopCh
	done := t.loopDone
	t.mu.Unlock()
	if stop != nil {
		close(stop)
		<-done
	}
}

func (t *Tracker) plainFinishLocked() {
	elapsed := t.plainDuration(t.stopAt.Sub(t.startAt))
	if t.failed {
		if t.finErr != nil {
			fmt.Fprintf(t.w, "  %s %s: %v\n", t.st.g.Fail, t.title, t.finErr)
		} else {
			fmt.Fprintf(t.w, "  %s %s\n", t.st.g.Fail, t.title)
		}
		return
	}
	head := t.finHead
	if head == "" {
		head = t.title
	}
	summary := t.finSummary
	if summary == "" {
		summary = t.status
	}
	line := "  " + t.st.g.OK + " " + head
	if summary != "" {
		line += " " + t.st.g.Dash + " " + summary
	}
	fmt.Fprintln(t.w, line+elapsed)
}

// plainDuration renders " (dur)" for durations worth mentioning; sub-100ms
// operations stay clean (and unit-test output deterministic).
func (t *Tracker) plainDuration(d time.Duration) string {
	if d < 100*time.Millisecond {
		return ""
	}
	return " (" + fmtDurationCompact(d) + ")"
}

func (t *Tracker) plainCounts(s *Step) string {
	if s.cur <= 0 && (s.status != stepActive || s.total <= 0) {
		return ""
	}
	out := " " + t.st.g.Sep + " " + fmtCount(s.cur)
	if s.status == stepActive && s.total > 0 {
		out += " / " + fmtCount(s.total)
	}
	if s.unit != "" {
		out += " " + s.unit
	}
	return out
}

// ---- animated rendering ---------------------------------------------------

func (t *Tracker) loop() {
	defer close(t.loopDone)
	defer func() {
		if r := recover(); r != nil {
			_, _ = io.WriteString(t.w, ansiShowCursor+ansiReset)
			markCursorRestored(t.w)
			panic(r)
		}
	}()
	tk := time.NewTicker(t.fps)
	defer tk.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-tk.C:
			t.mu.Lock()
			if t.started && !t.finished {
				t.tick++
				t.paintLocked(false)
			}
			t.mu.Unlock()
		}
	}
}

// paintLocked repaints the live region. Protocol invariant: after every
// paint the cursor parks at column 0 on the line just below the region, so
// the next paint reaches the region top with a bare CR + cursor-up. Frames
// are wrapped in synchronized-update brackets and every line is width-clamped
// (a soft-wrapped line would corrupt the cursor arithmetic for all later
// frames — the failure mode this renderer exists to eliminate).
func (t *Tracker) paintLocked(final bool) {
	width, height := t.sizeFn()
	if width < 20 {
		width = 20
	}
	maxRegion := height - 2
	if maxRegion < 4 {
		maxRegion = 4
	}

	lines := t.buildLinesLocked(width)
	for len(lines) > maxRegion {
		if s := t.oldestFinishedStepLocked(); s != nil {
			// Retire the oldest finished row to permanent output above the
			// region so the live region always fits the terminal.
			t.flushQueue = append(t.flushQueue, t.stepRowLocked(s, width))
			t.removeStepLocked(s)
			lines = t.buildLinesLocked(width)
			continue
		}
		lines = lines[len(lines)-maxRegion:]
		break
	}

	var b strings.Builder
	b.WriteString(ansiSyncStart)
	b.WriteString("\r")
	b.WriteString(ansiUp(t.lastLines))
	if len(t.flushQueue) > 0 {
		b.WriteString(ansiClearBelow)
		for _, fl := range t.flushQueue {
			b.WriteString(clampLine(fl, width-1, t.st.g.Ellipsis))
			b.WriteString("\r\n")
		}
		t.flushQueue = nil
		t.lastLines = 0
	}
	for _, ln := range lines {
		b.WriteString(clampLine(ln, width-1, t.st.g.Ellipsis))
		b.WriteString(ansiClearEOL)
		b.WriteString("\r\n")
	}
	if len(lines) < t.lastLines {
		b.WriteString(ansiClearBelow)
	}
	t.lastLines = len(lines)
	if final {
		b.WriteString(ansiShowCursor)
		b.WriteString(ansiReset)
	}
	b.WriteString(ansiSyncEnd)
	_, _ = io.WriteString(t.w, b.String())
}

func (t *Tracker) oldestFinishedStepLocked() *Step {
	for _, s := range t.steps {
		if s.status == stepDone || s.status == stepSkipped || s.status == stepFailed {
			return s
		}
	}
	return nil
}

func (t *Tracker) removeStepLocked(target *Step) {
	for i, s := range t.steps {
		if s == target {
			t.steps = append(t.steps[:i], t.steps[i+1:]...)
			return
		}
	}
}

func (t *Tracker) buildLinesLocked(width int) []string {
	var lines []string
	if t.logo {
		frac := t.logoFracLocked()
		logo := logoLines(t.st.logoLit, t.st.logoDim, t.st.accent, t.st.g, frac, t.tick, -1)
		title := ""
		if t.title != "" {
			title = "   " + t.st.title.Render(t.title)
		}
		lines = append(lines,
			"  "+logo[0],
			"  "+logo[1]+title,
			"  "+logo[2]+"   "+t.statusRowLocked(),
			"  "+logo[3],
			"  "+logo[4],
		)
		if len(t.steps) > 0 {
			lines = append(lines, "")
		}
	} else {
		lines = append(lines, t.compactHeaderLocked())
	}
	for _, s := range t.steps {
		lines = append(lines, t.stepRowLocked(s, width))
	}
	if t.logo && t.finished && !t.failed && t.finSummary != "" {
		lines = append(lines, "", "  "+t.st.accent.Render(t.st.g.OK)+" "+t.st.dim.Render(t.finSummary))
	}
	if t.logo && t.finished && t.failed && t.finErr != nil && t.lastFailedStepLocked() == nil {
		lines = append(lines, "", "  "+t.st.err.Render(t.st.g.Fail+" "+t.finErr.Error()))
	}
	return lines
}

func (t *Tracker) lastFailedStepLocked() *Step {
	for i := len(t.steps) - 1; i >= 0; i-- {
		if t.steps[i].status == stepFailed {
			return t.steps[i]
		}
	}
	return nil
}

// logoFracLocked derives the logo fill fraction. Planned checklists light the
// mark progressively (done steps plus the active step's own fraction);
// unplanned auto-step runs use the indeterminate marquee until they finish.
func (t *Tracker) logoFracLocked() float64 {
	if t.finished && !t.failed {
		return 1
	}
	if !t.hasExplicit || len(t.steps) == 0 {
		if t.finished {
			return 0 // failed with no checklist: dim mark, the ✗ carries the news
		}
		return -1
	}
	var done, active float64
	for _, s := range t.steps {
		switch s.status {
		case stepDone, stepSkipped, stepFailed:
			done++
		case stepActive:
			if s.total > 0 {
				f := float64(s.cur) / float64(s.total)
				if f > 1 {
					f = 1
				}
				if f > active {
					active = f
				}
			}
		}
	}
	return (done + active) / float64(len(t.steps))
}

func (t *Tracker) elapsedLocked() string {
	end := t.now()
	if t.finished {
		end = t.stopAt
	}
	return fmtDurationCompact(end.Sub(t.startAt))
}

func (t *Tracker) statusRowLocked() string {
	elapsed := t.st.dimmer.Render(" " + t.st.g.Sep + " " + t.elapsedLocked())
	switch {
	case t.finished && t.failed:
		return t.st.err.Render(t.st.g.Fail+" failed") + elapsed
	case t.finished:
		head := t.finHead
		if head == "" || head == t.title {
			head = "ready"
		}
		return t.st.accent.Render(t.st.g.StatusReady) + " " + t.st.accentBold.Render(head) + elapsed
	default:
		text := t.status
		if text == "" {
			text = t.defaultStatusLocked()
		}
		spin := t.st.spin[t.tick%len(t.st.spin)]
		return t.st.accentDim.Render(spin) + " " + t.st.dim.Render(text) + elapsed
	}
}

// defaultStatusLocked derives a status when the caller set none: the phase
// position for a planned checklist ("phase 3 / 5"), the active stage label
// for reporter-driven runs, "working" as the last resort.
func (t *Tracker) defaultStatusLocked() string {
	if t.hasExplicit && len(t.steps) > 0 {
		done := 0
		anyActive := false
		for _, s := range t.steps {
			switch s.status {
			case stepDone, stepSkipped, stepFailed:
				done++
			case stepActive:
				anyActive = true
			}
		}
		cur := done
		if anyActive && cur < len(t.steps) {
			cur++
		}
		if cur == 0 {
			cur = 1
		}
		return fmt.Sprintf("phase %d / %d", cur, len(t.steps))
	}
	if s := t.lastActiveLocked(); s != nil {
		return s.label
	}
	return "working"
}

func (t *Tracker) compactHeaderLocked() string {
	elapsed := t.st.dimmer.Render(" " + t.st.g.Sep + " " + t.elapsedLocked())
	switch {
	case t.finished && t.failed:
		line := "  " + t.st.err.Render(t.st.g.Fail) + " " + t.st.title.Render(t.title)
		if t.finErr != nil {
			line += t.st.err.Render(": " + t.finErr.Error())
		}
		return line + elapsed
	case t.finished:
		head := t.finHead
		if head == "" {
			head = t.title
		}
		line := "  " + t.st.accent.Render(t.st.g.OK) + " " + t.st.title.Render(head)
		summary := t.finSummary
		if summary == "" {
			summary = t.status
		}
		if summary != "" {
			line += t.st.dim.Render(" " + t.st.g.Dash + " " + summary)
		}
		return line + elapsed
	default:
		mark := t.st.accentDim.Render(t.st.spin[t.tick%len(t.st.spin)])
		if len(t.steps) > 0 {
			mark = t.st.dimmer.Render(t.st.g.StatusBusy)
		}
		line := "  " + mark + " " + t.st.title.Render(t.title)
		if t.status != "" {
			line += t.st.dim.Render(" " + t.st.g.Dash + " " + t.status)
		}
		return line + elapsed
	}
}

// barCells is the progress bar width, matching the design's 22-cell bar.
const barCells = 22

func (t *Tracker) stepRowLocked(s *Step, width int) string {
	switch s.status {
	case stepPending:
		return "  " + t.st.dimmer.Render(t.st.g.Pending) + " " + t.st.dimmer.Render(s.label)
	case stepSkipped:
		note := ""
		if s.skipNote != "" {
			note = ": " + s.skipNote
		}
		return "  " + t.st.dimmer.Render(t.st.g.Pending) + " " +
			t.st.dimmer.Render(s.label+" "+t.st.g.Dash+" skipped"+note)
	case stepFailed:
		msg := ""
		if s.err != nil {
			msg = t.st.err.Render(" " + t.st.g.Dash + " " + s.err.Error())
		}
		return "  " + t.st.err.Render(t.st.g.Fail) + " " + t.st.fg.Render(s.label) + msg
	case stepDone:
		row := "  " + t.st.accent.Render(t.st.g.OK) + " " + t.st.fg.Render(s.finalLabel()) + t.countsLocked(s, true)
		if d := s.stopped.Sub(s.started); d >= 100*time.Millisecond {
			row += t.st.dimmer.Render(" (" + fmtDurationCompact(d) + ")")
		}
		return row
	default: // active
		spin := t.st.spin[t.tick%len(t.st.spin)]
		base := "  " + t.st.accentDim.Render(spin) + " " + t.st.fg.Render(s.label) + t.countsLocked(s, false)
		note := ""
		if s.note != "" {
			note = t.st.dimmer.Render("  " + s.note)
		}
		bar := ""
		if s.total > 0 {
			bar = t.barLocked(s)
		}
		// Fit by priority instead of mid-bar truncation: full row, then drop
		// the note, then drop the bar. The label and counters always stay.
		for _, row := range []string{base + note + bar, base + bar, base + note} {
			if visibleWidth(row) <= width-1 {
				return row
			}
		}
		return base
	}
}

func (t *Tracker) countsLocked(s *Step, done bool) string {
	if s.cur <= 0 && (done || s.total <= 0) {
		return ""
	}
	out := t.st.dim.Render(" "+t.st.g.Sep+" ") + t.st.fg.Render(fmtCount(s.cur))
	if !done && s.total > 0 {
		out += t.st.dim.Render(" / " + fmtCount(s.total))
	}
	if s.unit != "" {
		out += t.st.dim.Render(" " + s.unit)
	}
	return out
}

func (t *Tracker) barLocked(s *Step) string {
	frac := float64(s.cur) / float64(s.total)
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	fill := int(frac*barCells + 0.5)
	pct := int(frac*100 + 0.5)
	return "  " + t.st.accent.Render(strings.Repeat(t.st.g.BarFull, fill)) +
		t.st.dimmer.Render(strings.Repeat(t.st.g.BarEmpty, barCells-fill)) +
		t.st.dim.Render(fmt.Sprintf(" %3d%%", pct))
}

// ---- styles ---------------------------------------------------------------

// styleSet bundles the glyphs, spinner frames, and writer-bound styles one
// tracker instance renders with. Styles are bound to the tracker's own writer
// (not the process stdout) so `gortex … | tee log` keeps colors on the
// animated stderr region.
type styleSet struct {
	g    glyphSet
	spin []string

	fg         lipgloss.Style
	dim        lipgloss.Style
	dimmer     lipgloss.Style
	accent     lipgloss.Style
	accentBold lipgloss.Style
	accentDim  lipgloss.Style
	err        lipgloss.Style
	title      lipgloss.Style
	logoLit    lipgloss.Style
	logoDim    lipgloss.Style
}

func newStyleSet(w io.Writer) styleSet {
	r := lipgloss.NewRenderer(w)
	r.SetColorProfile(colorProfileFor(w))
	mk := func(c lipgloss.Color) lipgloss.Style { return r.NewStyle().Foreground(c) }
	return styleSet{
		g:          activeGlyphs(),
		spin:       spinFrames(),
		fg:         mk(colFg),
		dim:        mk(colFgDim),
		dimmer:     mk(colMuted),
		accent:     mk(colAccent),
		accentBold: r.NewStyle().Foreground(colAccent).Bold(true),
		accentDim:  mk(colAccentDim),
		err:        mk(colErr),
		title:      r.NewStyle().Foreground(colFg).Bold(true),
		logoLit:    mk(colPerim),
		logoDim:    mk(colInner),
	}
}
