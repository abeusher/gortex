package progress

import (
	"context"
	"io"
)

// Spinner is the single-operation face of the Tracker, kept for the many
// call sites that want "animate this one label, then ✓ or ✗". It is a thin
// facade: on a capable terminal the Tracker renders a live compact header
// (spinner, title, status, elapsed) and turns Reporter stage transitions
// into a checklist of step rows; elsewhere it degrades to clean line output.
// Implements Reporter so it can be installed via WithReporter.
type Spinner struct {
	t *Tracker
}

// NewSpinner constructs a Spinner bound to w. When w isn't a TTY (or NO_COLOR
// / TERM=dumb / CI is set), the spinner is created in plain-text mode.
func NewSpinner(w io.Writer) *Spinner {
	return &Spinner{t: NewTracker(w)}
}

// Disable forces the spinner into plain-text mode. Effective before Start.
func (s *Spinner) Disable() { s.t.disable() }

// Enabled reports whether the spinner is animating.
func (s *Spinner) Enabled() bool { return s.t.Animated() }

// Start begins animating with the given label.
func (s *Spinner) Start(label string) { s.t.Start(label) }

// Set updates the label and sub-status mid-animation. Either may be empty to
// leave the existing value.
func (s *Spinner) Set(label, sub string) {
	s.t.SetTitle(label)
	s.t.SetStatus(sub)
}

// Report implements Reporter. Each distinct stage becomes its own live step
// row (with counters and, when the total is known, a progress bar); in plain
// mode stages print as start / finish line pairs with a slow heartbeat.
func (s *Spinner) Report(stage string, current, total int) {
	s.t.Report(stage, current, total)
}

// Done stops the spinner and replaces the frame with a green ✓ summary.
func (s *Spinner) Done() { s.t.Done("", "") }

// Fail stops the spinner and replaces the frame with a red ✗ summary.
func (s *Spinner) Fail(err error) { s.t.Fail(err) }

// Tracker exposes the underlying tracker for call sites that outgrow the
// single-label surface (explicit steps, log lines above the animation).
func (s *Spinner) Tracker() *Tracker { return s.t }

// Multi fans out reporter ticks to all of rs. Nil entries are skipped.
func Multi(rs ...Reporter) Reporter {
	out := make([]Reporter, 0, len(rs))
	for _, r := range rs {
		if r == nil {
			continue
		}
		out = append(out, r)
	}
	switch len(out) {
	case 0:
		return Nop{}
	case 1:
		return out[0]
	default:
		return multiReporter(out)
	}
}

type multiReporter []Reporter

func (m multiReporter) Report(stage string, current, total int) {
	for _, r := range m {
		r.Report(stage, current, total)
	}
}

// Run animates a spinner around fn. The context passed to fn carries the
// spinner as a Reporter, so any progress.FromContext(ctx).Report(…) inside fn
// drives the live step rows. The spinner is finished (✓ or ✗) before Run
// returns.
func Run(ctx context.Context, w io.Writer, label string, fn func(context.Context) error) error {
	sp := NewSpinner(w)
	return runWith(ctx, sp, label, fn)
}

// RunDisabled is Run with the spinner forced into plain-text mode.
func RunDisabled(ctx context.Context, w io.Writer, label string, fn func(context.Context) error) error {
	sp := NewSpinner(w)
	sp.Disable()
	return runWith(ctx, sp, label, fn)
}

func runWith(ctx context.Context, sp *Spinner, label string, fn func(context.Context) error) error {
	sp.Start(label)
	ctx = WithReporter(ctx, sp)
	err := fn(ctx)
	if err != nil {
		sp.Fail(err)
	} else {
		sp.Done()
	}
	return err
}
