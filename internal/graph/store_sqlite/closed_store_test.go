package store_sqlite_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

// TestClosedStoreReadsDoNotPanic pins the teardown-race fix: after Close()
// has shut the store (daemon shutdown / restart / store swap), an in-flight
// reader — e.g. a deferred parallel-enrich goroutine still holding a cached
// *sql.Stmt — must degrade to an empty result, never panic the whole daemon.
// Before the fix this surfaced as `panic: store_sqlite: sql: statement is
// closed` from GetNode under runDeferredEnrichParallel.
func TestClosedStoreReadsDoNotPanic(t *testing.T) {
	s := openTestStore(t)
	s.AddNode(&graph.Node{ID: "p/a.go::Foo", Kind: graph.KindType, Name: "Foo", FilePath: "p/a.go"})
	require.NotNil(t, s.GetNode("p/a.go::Foo"), "sanity: node readable before close")

	require.NoError(t, s.Close())

	assert.NotPanics(t, func() {
		assert.Nil(t, s.GetNode("p/a.go::Foo"))
		assert.Empty(t, s.FindNodesByName("Foo"))
		assert.Empty(t, s.GetFileNodes("p/a.go"))
	}, "reads after Close must degrade gracefully, not panic")
}
