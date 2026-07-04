package procio

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// waitForEntries polls obs until at least n entries have been recorded
// or the deadline elapses.
func waitForEntries(t *testing.T, obs *observer.ObservedLogs, n int) []observer.LoggedEntry {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if obs.Len() >= n {
			return obs.All()
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d log entries, got %d", n, obs.Len())
	return nil
}

func TestStderrWatcher_LogsLines(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r, w := io.Pipe()
	StderrWatcher{Logger: logger, Tag: "testproc"}.Watch(r)

	go func() {
		_, _ = w.Write([]byte("line one\nline two\n"))
		_ = w.Close()
	}()

	entries := waitForEntries(t, obs, 2)
	require.Equal(t, "subprocess stderr", entries[0].Message)
	require.Equal(t, "testproc", entries[0].ContextMap()["tag"])
	require.Equal(t, "line one", entries[0].ContextMap()["line"])
	require.Equal(t, "line two", entries[1].ContextMap()["line"])
}

func TestStderrWatcher_NilLoggerDrainsSilently(t *testing.T) {
	r, w := io.Pipe()
	StderrWatcher{Logger: nil, Tag: "testproc"}.Watch(r)

	done := make(chan struct{})
	go func() {
		_, _ = w.Write([]byte(strings.Repeat("x", 1<<20)))
		_ = w.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write to a nil-logger watcher blocked — pipe was not drained")
	}
}

func TestStderrWatcher_TruncatesOverlongLines(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r, w := io.Pipe()
	StderrWatcher{Logger: logger, Tag: "t", MaxLineBytes: 16}.Watch(r)

	go func() {
		_, _ = w.Write([]byte(strings.Repeat("a", 100) + "\nshort\n"))
		_ = w.Close()
	}()

	entries := waitForEntries(t, obs, 2)
	line0, _ := entries[0].ContextMap()["line"].(string)
	require.LessOrEqual(t, len(line0), 16)
	require.Equal(t, "short", entries[1].ContextMap()["line"])
}

func TestStderrWatcher_BurstSuppressionSummary(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r, w := io.Pipe()
	StderrWatcher{
		Logger:      logger,
		Tag:         "spam",
		BurstLimit:  5,
		BurstWindow: 24 * time.Hour, // never rolls over during the test
	}.Watch(r)

	go func() {
		var sb strings.Builder
		for i := 0; i < 20; i++ {
			sb.WriteString("boom\n")
		}
		_, _ = w.Write([]byte(sb.String()))
		_ = w.Close()
	}()

	// 5 verbatim lines + 1 suppression summary flushed at EOF.
	entries := waitForEntries(t, obs, 6)

	var logged, summaries int
	var suppressedCount int64
	for _, e := range entries {
		switch e.Message {
		case "subprocess stderr":
			logged++
		case "subprocess stderr suppressed":
			summaries++
			if v, ok := e.ContextMap()["suppressed_lines"]; ok {
				suppressedCount, _ = toInt64(v)
			}
		}
	}
	require.Equal(t, 5, logged)
	require.Equal(t, 1, summaries)
	require.EqualValues(t, 15, suppressedCount)
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func TestStderrWatcher_ExitsOnEOF(t *testing.T) {
	core, _ := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	r, w := io.Pipe()

	var wg sync.WaitGroup
	wg.Add(1)
	orig := StderrWatcher{Logger: logger, Tag: "t"}
	// Wrap run in a goroutine we can join on directly (Watch itself
	// fires an untracked goroutine, so re-implement the same call here
	// to observe completion).
	go func() {
		defer wg.Done()
		orig.run(r)
	}()

	_, _ = w.Write([]byte("bye\n"))
	_ = w.Close()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher goroutine did not exit after EOF")
	}
}
