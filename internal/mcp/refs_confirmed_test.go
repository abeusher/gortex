package mcp

import "testing"

// TestResetConfirmedRefs asserts the lazy-enrichment ledger is emptied
// when the graph is rebuilt so it stays scoped to one analysis epoch
// instead of growing for the daemon's lifetime.
func TestResetConfirmedRefs(t *testing.T) {
	s := &Server{}
	for _, id := range []string{"a.go::A", "b.go::B", "c.go::C"} {
		s.refsConfirmed.Store(id, struct{}{})
	}

	s.resetConfirmedRefs()

	n := 0
	s.refsConfirmed.Range(func(_, _ any) bool { n++; return true })
	if n != 0 {
		t.Fatalf("refsConfirmed has %d entries after reset, want 0", n)
	}

	// Reusable after clearing: a later store is cleared by a second reset.
	s.refsConfirmed.Store("d.go::D", struct{}{})
	s.resetConfirmedRefs()
	if _, ok := s.refsConfirmed.Load("d.go::D"); ok {
		t.Fatal("entry stored after reset should be cleared by a second reset")
	}
}
