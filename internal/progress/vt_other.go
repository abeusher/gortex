//go:build !windows

package progress

import "io"

// enableVT is a no-op outside Windows: every unix terminal that passed the
// TTY gate interprets VT sequences natively.
func enableVT(io.Writer) bool { return true }
