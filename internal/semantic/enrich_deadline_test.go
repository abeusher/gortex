package semantic

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/graph"
)

// TestManager_EnrichOne_AbandonsOnDeadline verifies the per-repo enrichment
// deadline: a provider that blocks past the deadline is abandoned (the
// enrichment WaitGroup proceeds) rather than pinning it indefinitely — the
// MSBuild/Roslyn-stuck failure mode, generalised to "slow across many
// symbols".
func TestManager_EnrichOne_AbandonsOnDeadline(t *testing.T) {
	t.Setenv("GORTEX_LSP_ENRICH_TIMEOUT", "50ms")

	cfg := Config{
		Enabled: true,
		Providers: []ProviderConfig{
			{Name: "slow-go", Languages: []string{"go"}, Priority: 1, Enabled: true},
		},
	}
	mgr := NewManager(cfg, zap.NewNop())

	release := make(chan struct{})
	var enrichReturned atomic.Bool
	mgr.RegisterProvider(&mockProvider{
		name:      "slow-go",
		languages: []string{"go"},
		available: true,
		enrichFunc: func(g graph.Store, root string) (*EnrichResult, error) {
			<-release // block well past the 50ms deadline
			enrichReturned.Store(true)
			return &EnrichResult{Provider: "slow-go", Language: "go"}, nil
		},
	})

	g := graph.New()
	g.AddNode(&graph.Node{ID: "main.go::main", Kind: graph.KindFunction, Name: "main", FilePath: "main.go", Language: "go"})
	roots := map[string]string{"default": "/tmp/test"}

	resultCh := make(chan []*EnrichResult, 1)
	go func() {
		res, _ := mgr.EnrichAll(g, roots)
		resultCh <- res
	}()

	select {
	case res := <-resultCh:
		// The abandoned provider contributes no result.
		assert.Empty(t, res, "enrichment past the per-repo deadline must be abandoned, yielding no result")
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("EnrichAll blocked on a slow provider instead of abandoning it at the deadline")
	}

	// Unblock the detached goroutine so it unwinds cleanly.
	close(release)
	require.Eventually(t, enrichReturned.Load, time.Second, 10*time.Millisecond,
		"the abandoned enrichment goroutine should still drain and return")
}

// TestManager_EnrichOne_DisabledDeadline verifies the bound can be switched
// off: with GORTEX_LSP_ENRICH_TIMEOUT=off a provider runs to completion even
// if slow.
func TestManager_EnrichOne_DisabledDeadline(t *testing.T) {
	t.Setenv("GORTEX_LSP_ENRICH_TIMEOUT", "off")

	cfg := Config{
		Enabled: true,
		Providers: []ProviderConfig{
			{Name: "go", Languages: []string{"go"}, Priority: 1, Enabled: true},
		},
	}
	mgr := NewManager(cfg, zap.NewNop())
	mgr.RegisterProvider(&mockProvider{
		name:      "go",
		languages: []string{"go"},
		available: true,
		enrichFunc: func(g graph.Store, root string) (*EnrichResult, error) {
			time.Sleep(80 * time.Millisecond)
			return &EnrichResult{Provider: "go", Language: "go", EdgesConfirmed: 7}, nil
		},
	})

	g := graph.New()
	g.AddNode(&graph.Node{ID: "main.go::main", Kind: graph.KindFunction, Name: "main", FilePath: "main.go", Language: "go"})

	results, err := mgr.EnrichAll(g, map[string]string{"default": "/tmp/test"})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, 7, results[0].EdgesConfirmed)
}
