package parity

import "testing"

// parityFenceCount is the frozen number of languages held at or beyond their
// captured parity coverage in baseline.json. It is a CI-enforced golden, the
// same discipline as a wire-contract fingerprint: a DROP means a language was
// silently removed from the fence (un-protecting it from regression) and a RISE
// means a new benchmark language was fenced — either way an intentional change
// must bump this constant deliberately, which is the audit trail.
const parityFenceCount = 17

// TestParityCount freezes the at-or-beyond-parity language count. The committed
// baseline is the set of languages whose coverage CI refuses to let regress;
// pinning its size here makes any change to that set a deliberate, reviewed act
// rather than a silent erosion of the fence.
func TestParityCount(t *testing.T) {
	b, err := LoadBaseline()
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}
	if got := len(b); got != parityFenceCount {
		t.Fatalf("parity fence count = %d, want %d (frozen golden).\n"+
			"If a benchmark language was intentionally added or removed, update parityFenceCount.",
			got, parityFenceCount)
	}
	// Every fenced language must carry a real coverage floor in (0, 1]. A zero
	// or negative floor fences nothing; a floor above 1 is not a valid ratio.
	for lang, cov := range b {
		if cov <= 0 {
			t.Errorf("language %q baseline %.4f is non-positive — it fences nothing", lang, cov)
		}
		if cov > 1 {
			t.Errorf("language %q baseline %.4f exceeds 1.0 (coverage is a ratio)", lang, cov)
		}
	}
}

// TestBaselineRepoExhaustive ensures the fence is exactly the benchmark corpus:
// every benchmark language has a baseline floor (none is measured but unfenced),
// and no baseline entry lacks a benchmark repo (no dead fence that is never
// measured). Together with TestParityCount this keeps the fence complete and
// honest as the corpus evolves.
func TestBaselineRepoExhaustive(t *testing.T) {
	b, err := LoadBaseline()
	if err != nil {
		t.Fatalf("load baseline: %v", err)
	}

	repoLangs := map[string]bool{}
	for _, repo := range BenchRepos() {
		repoLangs[repo.Language] = true
		if _, ok := b[repo.Language]; !ok {
			t.Errorf("benchmark language %q (%s) has no baseline entry — unfenced, could regress undetected",
				repo.Language, repo.URL)
		}
	}
	for lang := range b {
		if !repoLangs[lang] {
			t.Errorf("baseline carries %q but no benchmark repo measures it — dead fence entry", lang)
		}
	}
}
