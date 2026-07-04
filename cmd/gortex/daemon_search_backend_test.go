package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/search"
)

// fakeSymbolSearcher is a minimal graph.SymbolSearcher stand-in so
// resolveSearchBackend's SymbolSearcherBackend branch can be exercised
// without a real sqlite store.
type fakeSymbolSearcher struct{}

func (fakeSymbolSearcher) UpsertSymbolFTS(string, string) error                    { return nil }
func (fakeSymbolSearcher) BulkUpsertSymbolFTS(string, []graph.SymbolFTSItem) error { return nil }
func (fakeSymbolSearcher) BuildSymbolIndex() error                                 { return nil }
func (fakeSymbolSearcher) SearchSymbols(string, int) ([]graph.SymbolHit, error)    { return nil, nil }

func TestResolveSearchBackend_SymbolSearcherBackend(t *testing.T) {
	b := search.NewSymbolSearcherBackend(fakeSymbolSearcher{})
	b.Add("node-1")
	b.Add("node-2")

	info := resolveSearchBackend(b)

	assert.Equal(t, "sqlite-fts5", info.Name)
	assert.Equal(t, 2, info.DocCount, "DocCount should come from the adapter's Count()")
	assert.True(t, info.DiskResident, "the FTS5 index lives inside the graph store, not in-process heap")
	assert.Zero(t, info.Bytes, "no fabricated byte count for a disk-resident backend")
}

func TestResolveSearchBackend_SymbolSearcherBackend_ThroughSwappable(t *testing.T) {
	b := search.NewSymbolSearcherBackend(fakeSymbolSearcher{})
	sw := search.NewSwappable(b)

	info := resolveSearchBackend(sw)

	assert.Equal(t, "sqlite-fts5", info.Name)
	assert.True(t, info.DiskResident)
}

func TestRenderDaemonHeader_SearchBackendRow_SymbolSearcher(t *testing.T) {
	st := sampleStatus()
	st.SearchBackend = daemon.SearchBackendStats{
		Name:         "sqlite-fts5",
		DocCount:     48572,
		DiskResident: true,
	}
	var buf bytes.Buffer
	renderDaemonHeader(&buf, st)
	out := buf.String()
	assert.Contains(t, out, "sqlite-fts5")
	assert.Contains(t, out, "48572")
	assert.Contains(t, out, "disk-resident")
	assert.NotContains(t, out, "heap=0 B", "must not print a fabricated zero heap size")
}
