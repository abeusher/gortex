package indexer

import (
	"errors"
	"strings"
	"syscall"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestENOSPCPartialWatchWarnsOnce proves the inotify-exhaustion UX: an ENOSPC
// notes the degraded state, logs the operator warning naming the sysctl exactly
// once across repeats, fires the degraded callback once, and exposes the reason
// for the whole-index "frozen" banner.
func TestENOSPCPartialWatchWarnsOnce(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	w := &Watcher{logger: zap.New(core)}
	var cbCount int
	w.OnDegraded(func(string) { cbCount++ })

	if !w.noteWatchDegraded(syscall.ENOSPC) {
		t.Fatal("the first ENOSPC must be reported as the first (logged) occurrence")
	}
	if w.noteWatchDegraded(syscall.ENOSPC) {
		t.Error("a repeated ENOSPC must not re-log")
	}
	w.noteWatchDegraded(syscall.ENOSPC)

	if got := logs.Len(); got != 1 {
		t.Errorf("operator warning logged %d times, want exactly 1", got)
	}
	if cbCount != 1 {
		t.Errorf("degraded callback fired %d times, want 1", cbCount)
	}
	if r := w.DegradedReason(); !strings.Contains(r, "fs.inotify.max_user_watches") {
		t.Errorf("DegradedReason = %q, want it to name fs.inotify.max_user_watches", r)
	}
}

// TestWatchDegradedFDExhaustionWarnsOnce covers the EMFILE/ENFILE branch and
// confirms an unrelated error is not treated as degradation.
func TestWatchDegradedFDExhaustionWarnsOnce(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	w := &Watcher{logger: zap.New(core)}

	if !w.noteWatchDegraded(syscall.EMFILE) {
		t.Fatal("the first EMFILE must be the first occurrence")
	}
	if w.noteWatchDegraded(syscall.ENFILE) {
		t.Error("a follow-up FD-exhaustion error must not re-log")
	}
	if got := logs.Len(); got != 1 {
		t.Errorf("operator warning logged %d times, want exactly 1", got)
	}
	if r := w.DegradedReason(); !strings.Contains(r, "ulimit") {
		t.Errorf("FD-exhaustion reason should advise raising ulimit; got %q", r)
	}

	// A non-degradation error must be ignored — no false frozen banner.
	if w.noteWatchDegraded(errors.New("some other failure")) {
		t.Error("an unrelated error must not note watcher degradation")
	}
}
