package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestGoObservability_SlogPackageCall(t *testing.T) {
	src := `package foo

import "log/slog"

func Run() {
	slog.Info("user.signup", "id", 42)
	slog.Error("payment.failed")
}
`
	fix := runGoExtract(t, src)

	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 2 {
		t.Fatalf("expected 2 KindEvent, got %d: %+v", len(events), events)
	}
	gotNames := map[string]bool{}
	for _, e := range events {
		gotNames[e.Name] = true
		if e.ID != "event::log::"+e.Name {
			t.Errorf("event id = %q (expected event::log::<name>)", e.ID)
		}
		if k, _ := e.Meta["event_kind"].(string); k != "log" {
			t.Errorf("event_kind = %q", k)
		}
	}
	if !gotNames["user.signup"] || !gotNames["payment.failed"] {
		t.Errorf("missing event names: %v", gotNames)
	}

	emits := fix.edgesByKind[graph.EdgeEmits]
	if len(emits) != 2 {
		t.Errorf("expected 2 EdgeEmits, got %d", len(emits))
	}
	for _, e := range emits {
		if e.From != "pkg/foo.go::Run" {
			t.Errorf("emit from = %q", e.From)
		}
		if m, _ := e.Meta["method"].(string); m != "Info" && m != "Error" {
			t.Errorf("method meta = %q", m)
		}
	}
}

func TestGoObservability_LoggerInstanceCall(t *testing.T) {
	// Generic *Logger.Info(...) call — catches zap, zerolog, logrus,
	// and most internal wrappers without per-provider plumbing.
	src := `package foo

type Logger struct{}

func (l *Logger) Info(msg string, args ...any) {}

func Run(log *Logger) {
	log.Info("auth.failed")
}
`
	fix := runGoExtract(t, src)
	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Name != "auth.failed" {
		t.Errorf("name = %q", events[0].Name)
	}
}

func TestGoObservability_NonLiteralArgSkipped(t *testing.T) {
	// Dynamic format strings don't produce a stable event name —
	// agents who care about those can grep. The scanner skips them.
	src := `package foo

import "log/slog"

func Run(msg string) {
	slog.Info(msg)
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.nodesByKind[graph.KindEvent]); got != 0 {
		t.Errorf("non-literal arg should not produce KindEvent, got %d", got)
	}
}

func TestGoObservability_DuplicateNameDeduplicates(t *testing.T) {
	// Two emit sites for the same event name should produce one
	// node and two edges, not two nodes.
	src := `package foo

import "log/slog"

func A() { slog.Info("user.signup") }
func B() { slog.Info("user.signup") }
`
	fix := runGoExtract(t, src)
	events := fix.nodesByKind[graph.KindEvent]
	if len(events) != 1 {
		t.Errorf("expected 1 deduped event node, got %d", len(events))
	}
	if got := len(fix.edgesByKind[graph.EdgeEmits]); got != 2 {
		t.Errorf("expected 2 emit edges (one per call site), got %d", got)
	}
}

func TestGoObservability_NonLogMethodIgnored(t *testing.T) {
	src := `package foo

type Counter struct{}

func (c *Counter) Add(name string, n int) {}

func Run(c *Counter) {
	c.Add("api.requests", 1)
}
`
	fix := runGoExtract(t, src)
	if got := len(fix.nodesByKind[graph.KindEvent]); got != 0 {
		t.Errorf("non-log method 'Add' should not produce KindEvent, got %d", got)
	}
}
