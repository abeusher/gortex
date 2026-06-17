package main

import (
	"encoding/json"
	"testing"

	"github.com/zzet/gortex/internal/eval/parity"
)

func TestParityBaselineCheck(t *testing.T) {
	base := parity.Baseline{"go": 0.95, "rust": 0.80}

	// rust regressed well below baseline; go beats it.
	regs := base.Check([]parity.LanguageCoverage{
		{Language: "go", Coverage: 0.96},
		{Language: "rust", Coverage: 0.70},
	}, 0.005)
	if len(regs) != 1 || regs[0].Language != "rust" {
		t.Fatalf("expected a single rust regression, got %+v", regs)
	}

	// A dip within epsilon is not a regression; an at-baseline language is fine.
	if r := base.Check([]parity.LanguageCoverage{
		{Language: "go", Coverage: 0.949},
		{Language: "rust", Coverage: 0.80},
	}, 0.005); len(r) != 0 {
		t.Errorf("within-epsilon dip was flagged: %+v", r)
	}

	// A baseline language missing from the measured set counts as a regression.
	if r := base.Check([]parity.LanguageCoverage{{Language: "go", Coverage: 0.96}}, 0.005); len(r) != 1 || r[0].Language != "rust" {
		t.Errorf("a dropped-out language should regress: %+v", r)
	}

	// The committed baseline parses (currently empty → nothing enforced).
	if b, err := parity.LoadBaseline(); err != nil {
		t.Errorf("LoadBaseline: %v", err)
	} else if len(b) != 0 {
		t.Logf("committed baseline carries %d languages", len(b))
	}

	// MarshalBaseline round-trips through JSON.
	out, err := parity.MarshalBaseline([]parity.LanguageCoverage{{Language: "go", Coverage: 0.96}})
	if err != nil {
		t.Fatal(err)
	}
	var rt parity.Baseline
	if err := json.Unmarshal(out, &rt); err != nil {
		t.Fatal(err)
	}
	if rt["go"] != 0.96 {
		t.Errorf("round-trip lost go coverage: %+v", rt)
	}
}
