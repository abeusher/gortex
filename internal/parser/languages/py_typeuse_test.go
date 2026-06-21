package languages

import (
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// TestPyTypeUse_VariableAnnotationEmitsTypedAs pins the LSP-free recall
// fix for Python: `x: Session` references type Session and must emit an
// EdgeTypedAs to unresolved::Session. Before the fix it only seeded the
// local type-env map (recall ~0 without an LSP).
func TestPyTypeUse_VariableAnnotationEmitsTypedAs(t *testing.T) {
	src := `from .db import Session

def handler():
    conn: Session = open_session()
    use(conn)
`
	_, edges := runPyExtract(t, "app/handler.py", src)

	var hits []*graph.Edge
	for _, e := range edges {
		if e.Kind == graph.EdgeTypedAs && e.To == "unresolved::Session" {
			hits = append(hits, e)
		}
	}
	if len(hits) == 0 {
		t.Fatalf("expected EdgeTypedAs -> unresolved::Session for `conn: Session`, got none")
	}
	for _, e := range hits {
		if !strings.Contains(e.From, "handler") {
			t.Errorf("EdgeTypedAs From = %q, want it attributed to handler()", e.From)
		}
	}
}
