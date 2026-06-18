package indexer

import (
	"testing"

	"github.com/zzet/gortex/internal/parser/crashpool"
)

func TestEffectiveExtractBudget(t *testing.T) {
	cases := []struct {
		name  string
		base  int
		bytes int
		want  int
	}{
		{"disabled base zero", 0, 100000, 0},
		{"disabled base negative", -1, 100000, -1},
		{"empty file gets base", 100, 0, 100},
		{"small file adds proportional ms", 100, 5 * extractBudgetBytesPerMs, 105},
		{"one unit", 100, extractBudgetBytesPerMs, 101},
		{"sub-unit truncates", 100, extractBudgetBytesPerMs - 1, 100},
		{"huge file capped at multiple", 100, 1 << 30, 100 * extractBudgetMaxMultiple},
		{"exactly at cap boundary", 100, (extractBudgetMaxMultiple - 1) * 100 * extractBudgetBytesPerMs, 100 * extractBudgetMaxMultiple},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := effectiveExtractBudget(c.base, c.bytes); got != c.want {
				t.Errorf("effectiveExtractBudget(%d, %d) = %d, want %d", c.base, c.bytes, got, c.want)
			}
		})
	}
}

func TestExtractFileRetry(t *testing.T) {
	t.Run("crash then success retries once on a clean worker", func(t *testing.T) {
		calls := 0
		res := submitWithRetry(func() crashpool.Result {
			calls++
			if calls == 1 {
				return crashpool.Result{Crashed: true, Err: "worker died"}
			}
			return crashpool.Result{Nodes: nil, Edges: nil}
		})
		if calls != 2 {
			t.Fatalf("expected exactly one retry (2 submits), got %d", calls)
		}
		if res.Bad() {
			t.Errorf("retry on a clean worker should have succeeded, got %+v", res)
		}
	})

	t.Run("panic then success retries once", func(t *testing.T) {
		calls := 0
		res := submitWithRetry(func() crashpool.Result {
			calls++
			if calls == 1 {
				return crashpool.Result{Panicked: true, Err: "extractor panic"}
			}
			return crashpool.Result{}
		})
		if calls != 2 {
			t.Fatalf("expected 2 submits, got %d", calls)
		}
		if res.Bad() {
			t.Errorf("retry should have succeeded, got %+v", res)
		}
	})

	t.Run("clean parse does not retry", func(t *testing.T) {
		calls := 0
		submitWithRetry(func() crashpool.Result {
			calls++
			return crashpool.Result{}
		})
		if calls != 1 {
			t.Fatalf("a clean parse must not retry; got %d submits", calls)
		}
	})

	t.Run("persistent crash retries exactly once then gives up", func(t *testing.T) {
		calls := 0
		res := submitWithRetry(func() crashpool.Result {
			calls++
			return crashpool.Result{Crashed: true, Err: "always crashes"}
		})
		if calls != 2 {
			t.Fatalf("a persistently bad file must retry exactly once (2 submits), got %d", calls)
		}
		if !res.Crashed {
			t.Errorf("a persistently crashing file must surface the crash, got %+v", res)
		}
	})

	t.Run("plain extraction error is not retried", func(t *testing.T) {
		calls := 0
		submitWithRetry(func() crashpool.Result {
			calls++
			return crashpool.Result{Err: "unsupported language"}
		})
		if calls != 1 {
			t.Fatalf("a deterministic extraction error must not retry; got %d submits", calls)
		}
	})
}
