package parity

import (
	_ "embed"
	"encoding/json"
	"sort"
)

//go:embed baseline.json
var baselineJSON []byte

// Baseline maps a language to its committed minimum resolved-cross-file-dependent
// coverage. The parity run refuses to let any language regress below its
// baseline, which is how COVERED stays COVERED over time.
type Baseline map[string]float64

// LoadBaseline returns the committed baseline. An empty baseline ("{}") means
// none has been captured yet — informational, not a failure.
func LoadBaseline() (Baseline, error) {
	b := Baseline{}
	if len(baselineJSON) == 0 {
		return b, nil
	}
	if err := json.Unmarshal(baselineJSON, &b); err != nil {
		return nil, err
	}
	return b, nil
}

// Regression records a language whose measured coverage fell below its baseline.
type Regression struct {
	Language string
	Baseline float64
	Measured float64
}

// Check returns the languages whose measured coverage is below their baseline by
// more than epsilon. A language carried in the baseline but absent from the
// measured set is treated as 0 (it dropped out entirely). Languages with no
// baseline are not enforced.
func (b Baseline) Check(measured []LanguageCoverage, epsilon float64) []Regression {
	got := make(map[string]float64, len(measured))
	for _, m := range measured {
		got[m.Language] = m.Coverage
	}
	var regs []Regression
	for lang, base := range b {
		m := got[lang] // absent → 0
		if m < base-epsilon {
			regs = append(regs, Regression{Language: lang, Baseline: base, Measured: m})
		}
	}
	sort.Slice(regs, func(i, j int) bool { return regs[i].Language < regs[j].Language })
	return regs
}

// MarshalBaseline renders a measured coverage set as a baseline JSON document
// (stable, indented) suitable for committing to baseline.json.
func MarshalBaseline(measured []LanguageCoverage) ([]byte, error) {
	b := Baseline{}
	for _, m := range measured {
		b[m.Language] = m.Coverage
	}
	return json.MarshalIndent(b, "", "  ")
}
