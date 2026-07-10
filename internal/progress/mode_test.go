package progress

import (
	"bytes"
	"testing"
)

func TestEnvDisablesColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	if envDisablesColor() {
		t.Error("plain xterm env must not disable color")
	}
	t.Setenv("NO_COLOR", "1")
	if !envDisablesColor() {
		t.Error("NO_COLOR must disable color")
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if !envDisablesColor() {
		t.Error("TERM=dumb must disable color")
	}
}

func TestAnimationAllowedGates(t *testing.T) {
	var buf bytes.Buffer
	t.Setenv("GORTEX_FORCE_ANIMATION", "")
	if animationAllowed(&buf) {
		t.Error("a non-TTY writer must not animate")
	}
	t.Setenv("GORTEX_FORCE_ANIMATION", "1")
	if !animationAllowed(&buf) {
		t.Error("GORTEX_FORCE_ANIMATION must override TTY detection")
	}
}

func TestRestoreTerminalIdempotent(t *testing.T) {
	cursorHiddenOnStderr.Store(true)
	RestoreTerminal()
	if cursorHiddenOnStderr.Load() {
		t.Error("RestoreTerminal must clear the hidden-cursor flag")
	}
	RestoreTerminal() // second call is a no-op
}
