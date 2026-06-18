package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/platform"
	"github.com/zzet/gortex/internal/telemetry"
)

// withTelemetryDir points the unified data dir at a temp dir and clears the
// telemetry env overrides so a saved choice is what the resolver sees. Returns
// the resolved telemetry directory.
func withTelemetryDir(t *testing.T) string {
	t.Helper()
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("GORTEX_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")
	t.Setenv("GORTEX_TELEMETRY_ENDPOINT", "")
	return platform.TelemetryDir()
}

// runTelemetryCmd invokes one telemetry subcommand's RunE directly and returns
// its captured output. Calling RunE (rather than Execute) keeps the test
// hermetic — cobra's Execute always dispatches from the root, so SetArgs on a
// subcommand is ignored and the root's persistent hooks would fire.
func runTelemetryCmd(t *testing.T, sub string) string {
	t.Helper()
	var cmd *cobra.Command
	switch sub {
	case "on":
		cmd = telemetryOnCmd
	case "off":
		cmd = telemetryOffCmd
	case "status":
		cmd = telemetryStatusCmd
	default:
		t.Fatalf("unknown telemetry subcommand %q", sub)
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("telemetry %s: %v", sub, err)
	}
	return out.String()
}

func mustDay(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestTelemetryOnOffStatus(t *testing.T) {
	withTelemetryDir(t)

	// Default status: disabled, decided by the opt-in default.
	status := runTelemetryCmd(t, "status")
	if !strings.Contains(status, "disabled") || !strings.Contains(status, "default") {
		t.Errorf("default status = %q, want disabled/default", status)
	}

	// Enable, then status reflects it (decided by config = the saved choice).
	runTelemetryCmd(t, "on")
	status = runTelemetryCmd(t, "status")
	if !strings.Contains(status, "enabled") || !strings.Contains(status, "config") {
		t.Errorf("after on, status = %q, want enabled/config", status)
	}

	// Disable again.
	runTelemetryCmd(t, "off")
	status = runTelemetryCmd(t, "status")
	if !strings.Contains(status, "disabled") {
		t.Errorf("after off, status = %q, want disabled", status)
	}
}

func TestTelemetryStatusShowsEndpointBlocked(t *testing.T) {
	withTelemetryDir(t)
	status := runTelemetryCmd(t, "status")
	if !strings.Contains(status, "not configured") {
		t.Errorf("status should report the endpoint as not configured: %q", status)
	}
}

func TestTelemetryOffClearsBuffer(t *testing.T) {
	dir := withTelemetryDir(t)

	// Seed a buffered rollup.
	store := telemetry.NewStore(dir)
	r := telemetry.NewRollup(mustDay("2026-06-17"))
	r.Add("mcp_tool_call", "x")
	if err := store.Save(r); err != nil {
		t.Fatal(err)
	}
	if days, _ := store.Days(); len(days) != 1 {
		t.Fatalf("seed failed (dir=%s)", dir)
	}

	runTelemetryCmd(t, "off")

	if days, _ := store.Days(); len(days) != 0 {
		t.Errorf("telemetry off did not clear buffered days: %v", days)
	}
}

func TestTelemetryFirstRunNoticeOnce(t *testing.T) {
	dir := withTelemetryDir(t)

	var w bytes.Buffer
	if !telemetry.MaybeFirstRunNotice(dir, &w) {
		t.Fatal("first notice should print")
	}
	if !strings.Contains(w.String(), "telemetry") && !strings.Contains(strings.ToLower(w.String()), "anonymous") {
		t.Errorf("notice text unexpected: %q", w.String())
	}
	// Second call is suppressed.
	w.Reset()
	if telemetry.MaybeFirstRunNotice(dir, &w) {
		t.Error("second notice should be suppressed")
	}
	if w.Len() != 0 {
		t.Errorf("suppressed notice still wrote: %q", w.String())
	}
}
