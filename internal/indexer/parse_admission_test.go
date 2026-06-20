package indexer

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
)

func TestClampParseWeight(t *testing.T) {
	const budget = 1000
	require.Equal(t, int64(1), clampParseWeight(0, budget), "empty file floors to weight 1")
	require.Equal(t, int64(1), clampParseWeight(-5, budget), "negative size floors to weight 1")
	require.Equal(t, int64(500), clampParseWeight(500, budget), "under budget weighs its size")
	require.Equal(t, int64(budget), clampParseWeight(budget, budget), "exactly budget weighs budget")
	require.Equal(t, int64(budget), clampParseWeight(budget*4, budget),
		"a file larger than the whole budget is admitted alone (weight clamped) so it can never deadlock")
}

func TestDefaultParseBudgetEnabled(t *testing.T) {
	require.Positive(t, config.Default().Index.MaxParseBytesInFlight,
		"default config must enable the parse-memory budget so a bare `gortex init` bounds peak memory")
}

// TestParseAdmissionIndexesOversizedFiles verifies the bytes-in-flight
// admission semaphore neither drops nor deadlocks on a file larger than the
// whole budget: with a tiny budget, every file — including one far over it —
// must still be indexed.
func TestParseAdmissionIndexesOversizedFiles(t *testing.T) {
	dir := t.TempDir()
	// One file far bigger than the budget (must be admitted alone), plus
	// several small ones that would otherwise all parse concurrently.
	writeFile(t, filepath.Join(dir, "big.go"),
		"package p\n\nvar Big = \""+strings.Repeat("x", 8192)+"\"\n")
	for i := 0; i < 8; i++ {
		writeFile(t, filepath.Join(dir, "small"+strconv.Itoa(i)+".go"),
			"package p\n\nfunc F"+strconv.Itoa(i)+"() {}\n")
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	cfg := config.Default().Index
	cfg.MaxParseBytesInFlight = 256 // tiny: big.go far exceeds it
	idx := New(g, reg, cfg, zap.NewNop())
	_, err := idx.Index(dir)
	require.NoError(t, err)

	present := map[string]bool{}
	for _, n := range g.AllNodes() {
		if n.Kind == graph.KindFile {
			present[filepath.Base(n.FilePath)] = true
		}
	}
	require.True(t, present["big.go"],
		"oversized file must still be indexed (admitted alone under the budget)")
	for i := 0; i < 8; i++ {
		name := "small" + strconv.Itoa(i) + ".go"
		require.True(t, present[name], "small file %s must be indexed", name)
	}
}
