//go:build llama

package llm

/*
#include <stdlib.h>
*/
import "C"

import (
	"unsafe"

	"go.uber.org/zap"
)

// ggml log levels (ggml.h's enum ggml_log_level). Mirrored here as
// plain ints since the exported callback below must use a C-portable
// signature (no C enum type) to stay assignable to ggml_log_callback
// via the shim in llama.go.
const (
	ggmlLogLevelNone  = 0
	ggmlLogLevelDebug = 1
	ggmlLogLevelInfo  = 2
	ggmlLogLevelWarn  = 3
	ggmlLogLevelError = 4
	ggmlLogLevelCont  = 5 // continues the previous line at an unknown level
)

// llamaLogger receives routed llama.cpp / ggml log lines. nil (the
// zero value, and the state before SetLogger is ever called) drops
// every line — matching "no visibility" rather than "print to
// stderr", since the daemon's stderr is its own structured log file.
var llamaLogger *zap.Logger

// SetLogger installs the logger that llama.cpp / ggml log output is
// routed to (see initBackend's llama_log_set/ggml_log_set install).
// Info/debug/none/cont lines — the tensor-load and ggml_metal
// pipeline-compile spam — are dropped; warn/error land as Warn. Safe
// to call before or after LoadModel: the callback reads the package
// variable on every line, so a later SetLogger takes effect
// immediately for subsequent log output.
func SetLogger(logger *zap.Logger) {
	llamaLogger = logger
}

// gortexLlamaLogCallback is invoked by gortexLlamaLogShim (llama.go's
// cgo preamble) for every llama.cpp / ggml log line. Exported to C via
// the cgo //export directive below; the plain (int, *C.char,
// unsafe.Pointer) signature avoids depending on the generated enum
// type for ggml_log_level. All the actual filtering/routing logic
// lives in routeLlamaLog, a plain-Go function with no cgo types, kept
// separate so it (and therefore this callback's behaviour) is
// unit-testable — a _test.go file cannot itself `import "C"" in this
// module's build (go/build rejects cgo in test files outright), so
// the exported function itself must stay a thin, untestable-by-name
// adapter.
//
//export gortexLlamaLogCallback
func gortexLlamaLogCallback(level C.int, text *C.char, _ unsafe.Pointer) {
	// The callback fires on whatever goroutine/thread ggml's internal
	// logging call happens to run on (potentially a C thread with no Go
	// scheduler awareness); never let a logging bug propagate as a
	// crash across the cgo boundary.
	defer func() { _ = recover() }()
	if text == nil {
		return
	}
	routeLlamaLog(int(level), C.GoString(text))
}

// routeLlamaLog applies the level filter and emits the line to the
// configured logger. Dropping info/debug/none/cont here (rather than
// in the cgo callback) keeps every branch of the actual decision logic
// reachable from a plain Go test.
func routeLlamaLog(level int, text string) {
	logger := llamaLogger
	if logger == nil {
		return
	}
	if level < ggmlLogLevelWarn || level == ggmlLogLevelCont {
		return
	}
	logger.Warn("llama log", zap.Int("level", level), zap.String("text", text))
}
