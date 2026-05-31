package store_ladybug

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

// runDDL runs a write/DDL Cypher statement, recovering the binding's
// panic-on-error into a returned error (self-contained; the tagged
// fts_probe_test.go's tryRunCypher isn't in the default build).
func runDDL(s *Store, q string) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	s.runWriteLocked(q, nil)
	return nil
}

// TestProbeSecondaryHashIndex explores whether the bundled go-ladybug
// (v0.13.1) accepts a SECONDARY hash index on a non-PK Node column (per
// LadybugDB PR #484) and, critically, whether the bulk COPY path the
// cold-load depends on survives such an index. Exploratory: it logs what
// each shape does rather than asserting a specific outcome, so it answers
// "is a real secondary index viable here?" empirically.
func TestProbeSecondaryHashIndex(t *testing.T) {
	tryShapes := func(s *Store) (string, bool) {
		shapes := []string{
			`CREATE HASH INDEX idx_node_name IF NOT EXISTS FOR (n:Node) ON (n.name)`,
			`CREATE HASH INDEX idx_node_name FOR (n:Node) ON (n.name)`,
			`CREATE INDEX idx_node_name IF NOT EXISTS FOR (n:Node) ON (n.name)`,
			`CREATE INDEX idx_node_name ON (n:Node) (n.name)`,
			`CALL CREATE_HASH_INDEX('Node', 'idx_node_name', 'name')`,
		}
		for _, q := range shapes {
			err := runDDL(s, q)
			t.Logf("CREATE shape %-70q -> err=%v", q, err)
			if err == nil {
				return q, true
			}
		}
		return "", false
	}

	// --- Order A: create the index on the empty table, then bulk COPY. ---
	t.Run("index_then_copy", func(t *testing.T) {
		s, err := Open(filepath.Join(t.TempDir(), "a.kuzu"))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })

		shape, ok := tryShapes(s)
		if !ok {
			t.Log("RESULT: no CREATE [HASH] INDEX shape accepted on this go-ladybug version — secondary indexes unavailable, in-memory nameIdx is the only option")
			return
		}
		t.Logf("RESULT: secondary index CREATED via %q", shape)

		s.BeginBulkLoad()
		s.AddBatch([]*graph.Node{
			{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go", Language: "go"},
			{ID: "b.go::Bar", Name: "Bar", Kind: graph.KindFunction, FilePath: "b.go", Language: "go"},
		}, nil)
		if err := s.FlushBulk(); err != nil {
			t.Logf("RESULT: bulk COPY FAILED with the secondary index present: %v  (=> index would break the cold-load COPY path)", err)
			return
		}
		t.Log("RESULT: bulk COPY survived the secondary index")
		if got := s.FindNodesByName("Foo"); len(got) != 1 {
			t.Errorf("FindNodesByName(Foo) = %d, want 1", len(got))
		} else {
			t.Log("RESULT: name lookup correct with the index present")
		}
	})

	// --- Order B: bulk COPY first, then create the index on a populated table. ---
	t.Run("copy_then_index", func(t *testing.T) {
		s, err := Open(filepath.Join(t.TempDir(), "b.kuzu"))
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })

		s.BeginBulkLoad()
		s.AddBatch([]*graph.Node{
			{ID: "a.go::Foo", Name: "Foo", Kind: graph.KindFunction, FilePath: "a.go", Language: "go"},
		}, nil)
		if err := s.FlushBulk(); err != nil {
			t.Fatalf("flush: %v", err)
		}
		if _, ok := tryShapes(s); ok {
			t.Log("RESULT: secondary index created on a POPULATED table (post-bulk-load order works)")
		} else {
			t.Log("RESULT: could not create the index on a populated table")
		}
	})
}
