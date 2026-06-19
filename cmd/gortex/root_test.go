package main

import (
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/zzet/gortex/internal/telemetry"
)

// TestRecordSiteCLIExclusions proves recordCLIUsage records a normal CLI
// command but excludes the two self-referential / double-counting cases: the
// `telemetry` subcommand itself, and a detached-daemon re-spawn
// (GORTEX_DAEMON_CHILD=1). Consent is forced on via the env override so the
// test exercises the exclusion logic, not the consent gate.
func TestRecordSiteCLIExclusions(t *testing.T) {
	root := &cobra.Command{Use: "gortex"}
	review := &cobra.Command{Use: "review"}
	telem := &cobra.Command{Use: "telemetry"}
	telemOff := &cobra.Command{Use: "off"}
	telem.AddCommand(telemOff)
	daemonCmd := &cobra.Command{Use: "daemon"}
	daemonStart := &cobra.Command{Use: "start"}
	daemonCmd.AddCommand(daemonStart)
	root.AddCommand(review, telem, daemonCmd)

	getenv := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if k == telemetry.EnvTelemetry {
				return "1" // force consent on
			}
			return extra[k]
		}
	}
	today := telemetry.DayKey(time.Now())

	// 1. A normal command records under cli_command.
	store := telemetry.NewStore(t.TempDir())
	recordCLIUsage(review, store, getenv(nil))
	roll, err := store.Load(today)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if roll.Counts["cli_command:review"] != 1 {
		t.Errorf("review should record cli_command:review=1; counts=%v", roll.Counts)
	}

	// 2. The telemetry subcommand is excluded — no day file written at all.
	storeT := telemetry.NewStore(t.TempDir())
	recordCLIUsage(telemOff, storeT, getenv(nil))
	if days, _ := storeT.Days(); len(days) != 0 {
		t.Errorf("telemetry subcommand must record nothing; wrote days %v", days)
	}

	// 3. A detached-daemon re-spawn is excluded.
	storeD := telemetry.NewStore(t.TempDir())
	recordCLIUsage(daemonStart, storeD, getenv(map[string]string{"GORTEX_DAEMON_CHILD": "1"}))
	if days, _ := storeD.Days(); len(days) != 0 {
		t.Errorf("daemon re-spawn must record nothing; wrote days %v", days)
	}

	// 4. Same daemon command WITHOUT the re-spawn marker records normally —
	// proves the exclusion is the marker, not the command.
	storeD2 := telemetry.NewStore(t.TempDir())
	recordCLIUsage(daemonStart, storeD2, getenv(nil))
	roll2, err := storeD2.Load(today)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if roll2.Counts["cli_command:daemon.start"] != 1 {
		t.Errorf("a foreground daemon start should record; counts=%v", roll2.Counts)
	}
}
