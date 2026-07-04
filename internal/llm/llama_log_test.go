//go:build llama

package llm

// These exercise routeLlamaLog — the plain-Go routing/filtering logic
// behind the cgo-exported gortexLlamaLogCallback (llama_log.go) — with
// synthetic level/text values. They do not load a model or touch
// initBackend. The exported callback itself cannot be unit-tested by
// name: this module's toolchain rejects `import "C"` inside a
// _test.go file outright ("use of cgo in test ... not supported"),
// which is exactly why the callback is kept as a thin adapter over
// this function.

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestRouteLlamaLog_DropsInfoDebugNone(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	SetLogger(zap.New(core))
	defer SetLogger(nil)

	for _, lvl := range []int{ggmlLogLevelNone, ggmlLogLevelDebug, ggmlLogLevelInfo} {
		routeLlamaLog(lvl, "tensor load spam")
	}
	require.Equal(t, 0, obs.Len(), "info/debug/none levels must be dropped, not logged")
}

func TestRouteLlamaLog_DropsContinuation(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	SetLogger(zap.New(core))
	defer SetLogger(nil)

	routeLlamaLog(ggmlLogLevelCont, "...continued")
	require.Equal(t, 0, obs.Len(), "GGML_LOG_LEVEL_CONT has unknown severity and must be dropped")
}

func TestRouteLlamaLog_LogsWarnAndError(t *testing.T) {
	core, obs := observer.New(zapcore.DebugLevel)
	SetLogger(zap.New(core))
	defer SetLogger(nil)

	routeLlamaLog(ggmlLogLevelWarn, "careful")
	routeLlamaLog(ggmlLogLevelError, "bad thing")

	entries := obs.All()
	require.Len(t, entries, 2)
	require.Equal(t, "llama log", entries[0].Message)
	require.Equal(t, "careful", entries[0].ContextMap()["text"])
	require.EqualValues(t, ggmlLogLevelWarn, entries[0].ContextMap()["level"])
	require.Equal(t, "bad thing", entries[1].ContextMap()["text"])
	require.EqualValues(t, ggmlLogLevelError, entries[1].ContextMap()["level"])
}

func TestRouteLlamaLog_NilLoggerIsNoop(t *testing.T) {
	SetLogger(nil)
	require.NotPanics(t, func() {
		routeLlamaLog(ggmlLogLevelError, "whatever")
	})
}

func TestSetLogger_LaterCallTakesEffectImmediately(t *testing.T) {
	core1, obs1 := observer.New(zapcore.DebugLevel)
	SetLogger(zap.New(core1))
	routeLlamaLog(ggmlLogLevelError, "first logger")
	require.Equal(t, 1, obs1.Len())

	core2, obs2 := observer.New(zapcore.DebugLevel)
	SetLogger(zap.New(core2))
	defer SetLogger(nil)
	routeLlamaLog(ggmlLogLevelError, "second logger")

	require.Equal(t, 1, obs1.Len(), "first logger must not receive log lines after SetLogger swaps it out")
	require.Equal(t, 1, obs2.Len())
}
