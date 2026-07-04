// Package procio routes a spawned subprocess's stderr into structured
// zap logging instead of letting it write raw text wherever the
// parent's own os.Stderr happens to point. For a detached daemon that
// IS its own log file (daemon.log), an inherited raw stderr means a
// crashing child (a language server panic backtrace, hundreds of
// llama.cpp load-time trace lines) interleaves unstructured text with
// structured JSON log lines forever. StderrWatcher scans, bounds, and
// rate-limits each child's stderr into normal Warn-level log entries.
package procio

import (
	"bytes"
	"io"
	"time"

	"go.uber.org/zap"
)

const (
	// DefaultMaxLineBytes bounds a single logged stderr line. Longer
	// lines (or lines with no newline in sight, e.g. a JSON blob) are
	// truncated rather than growing the scan buffer without bound or
	// aborting the scan.
	DefaultMaxLineBytes = 8 * 1024

	// DefaultBurstLimit is how many stderr lines are logged verbatim
	// within one rate-limit window before the rest of the window's
	// lines are counted and suppressed. A crash-looping subprocess
	// (rust-analyzer panicking repeatedly) must not flood the log.
	DefaultBurstLimit = 100

	// DefaultBurstWindow is the rate-limit window duration. Once a
	// window elapses, the burst counter resets and any suppressed count
	// from the previous window is flushed as one summary line.
	DefaultBurstWindow = 10 * time.Second
)

// StderrWatcher scans a subprocess's stderr stream and routes it into
// a zap.Logger at Warn, bounded and rate-limited. Zero value uses the
// package defaults; construct with the fields you want to override.
type StderrWatcher struct {
	// Logger receives one Warn per (unsuppressed) line, plus a
	// suppression summary at each window boundary. A nil Logger drains
	// the stream silently (so the child's write doesn't block) without
	// logging anything.
	Logger *zap.Logger
	// Tag identifies the subprocess in every log entry (e.g. the LSP
	// server name or command).
	Tag string
	// MaxLineBytes bounds a single line; <= 0 uses DefaultMaxLineBytes.
	MaxLineBytes int
	// BurstLimit is the per-window verbatim line cap; <= 0 uses
	// DefaultBurstLimit.
	BurstLimit int
	// BurstWindow is the rate-limit window; <= 0 uses
	// DefaultBurstWindow.
	BurstWindow time.Duration
}

// Watch starts a goroutine that reads r until EOF (i.e. until the
// owning subprocess closes/dies) or a read error, logging lines as
// they arrive. It returns immediately; the goroutine is panic-safe and
// requires no further cleanup from the caller — closing/killing the
// subprocess is enough to end it.
func (w StderrWatcher) Watch(r io.Reader) {
	go w.run(r)
}

func (w StderrWatcher) run(r io.Reader) {
	defer func() { _ = recover() }()

	if w.Logger == nil {
		// Still drain so the child never blocks on a full pipe buffer,
		// we just don't log any of it.
		_, _ = io.Copy(io.Discard, r)
		return
	}

	maxLine := w.MaxLineBytes
	if maxLine <= 0 {
		maxLine = DefaultMaxLineBytes
	}
	burst := w.BurstLimit
	if burst <= 0 {
		burst = DefaultBurstLimit
	}
	window := w.BurstWindow
	if window <= 0 {
		window = DefaultBurstWindow
	}

	lr := &lineReader{r: r, maxLine: maxLine}

	var (
		windowStart = time.Now()
		count       int
		suppressed  int
	)
	flush := func() {
		if suppressed > 0 {
			w.Logger.Warn("subprocess stderr suppressed",
				zap.String("tag", w.Tag),
				zap.Int("suppressed_lines", suppressed),
				zap.Duration("window", window),
			)
			suppressed = 0
		}
	}

	for {
		line, ok := lr.next()
		if !ok {
			break
		}
		now := time.Now()
		if now.Sub(windowStart) > window {
			flush()
			windowStart = now
			count = 0
		}
		count++
		if count > burst {
			suppressed++
			continue
		}
		w.Logger.Warn("subprocess stderr", zap.String("tag", w.Tag), zap.String("line", line))
	}
	flush()
}

// lineReader splits r into newline-delimited (or forced-length)
// strings, capped at maxLine bytes each. Unlike bufio.Scanner with a
// fixed Buffer, it never errors out on an over-long line (bufio's
// ErrTooLong would silently end the whole scan mid-stream) — an
// oversized line is simply truncated and the read continues.
type lineReader struct {
	r       io.Reader
	maxLine int
	buf     []byte // pending, not-yet-newline-terminated bytes
	chunk   [4096]byte
	err     error
}

// next returns the next line (without its trailing newline), or
// ok=false once the underlying reader is exhausted/erroring and no
// buffered data remains.
func (lr *lineReader) next() (string, bool) {
	for {
		if i := bytes.IndexByte(lr.buf, '\n'); i >= 0 {
			line := lr.buf[:i]
			lr.buf = lr.buf[i+1:]
			return lr.emit(line), true
		}
		if len(lr.buf) >= lr.maxLine {
			// No newline yet but already over the cap — force a token
			// now instead of buffering an unbounded fragment.
			line := lr.buf[:lr.maxLine]
			lr.buf = lr.buf[lr.maxLine:]
			return lr.emit(line), true
		}
		if lr.err != nil {
			if len(lr.buf) == 0 {
				return "", false
			}
			line := lr.buf
			lr.buf = nil
			return lr.emit(line), true
		}
		n, err := lr.r.Read(lr.chunk[:])
		if n > 0 {
			lr.buf = append(lr.buf, lr.chunk[:n]...)
		}
		if err != nil {
			lr.err = err
		}
	}
}

// emit trims a trailing '\r' (CRLF streams) and truncates to maxLine,
// copying out of the shared buf so the caller can keep it past the
// next Read.
func (lr *lineReader) emit(line []byte) string {
	line = bytes.TrimSuffix(line, []byte{'\r'})
	if len(line) > lr.maxLine {
		line = line[:lr.maxLine]
	}
	out := make([]byte, len(line))
	copy(out, line)
	return string(out)
}
