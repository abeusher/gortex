package progress

import (
	"fmt"
	"strconv"
	"time"
)

// fmtCount renders n with thousands separators ("48,210"). Counters in the
// step rows read as magnitudes, not digit strings; the separator makes a
// six-digit symbol count legible at a glance.
func fmtCount(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if len(s) <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	head := len(s) % 3
	out := make([]byte, 0, len(s)+len(s)/3+1)
	if neg {
		out = append(out, '-')
	}
	if head > 0 {
		out = append(out, s[:head]...)
	}
	for i := head; i < len(s); i += 3 {
		if len(out) > 0 && out[len(out)-1] != '-' {
			out = append(out, ',')
		}
		out = append(out, s[i:i+3]...)
	}
	return string(out)
}

// fmtDurationCompact renders a duration at the precision a progress line
// wants: sub-10s with one decimal ("3.2s"), sub-minute in whole seconds,
// then minute granularity. Sub-100ms rounds to "0.1s" floor-less so a fast
// step still shows a real number.
func fmtDurationCompact(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Second:
		ms := d.Milliseconds()
		if ms < 1 {
			ms = 1
		}
		return fmt.Sprintf("%dms", ms)
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()+0.5))
	case d < time.Hour:
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		h := int(d / time.Hour)
		m := int((d % time.Hour) / time.Minute)
		return fmt.Sprintf("%dh%02dm", h, m)
	}
}
