package progress

import (
	"testing"
	"time"
)

func TestFmtCount(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{48210, "48,210"},
		{612940, "612,940"},
		{1234567, "1,234,567"},
		{-1234, "-1,234"},
		{-12, "-12"},
	}
	for _, c := range cases {
		if got := fmtCount(c.in); got != c.want {
			t.Errorf("fmtCount(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFmtDurationCompact(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-time.Second, "1ms"},
		{0, "1ms"},
		{450 * time.Millisecond, "450ms"},
		{3200 * time.Millisecond, "3.2s"},
		{42 * time.Second, "42s"},
		{72 * time.Second, "1m12s"},
		{time.Hour + 5*time.Minute, "1h05m"},
	}
	for _, c := range cases {
		if got := fmtDurationCompact(c.in); got != c.want {
			t.Errorf("fmtDurationCompact(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
