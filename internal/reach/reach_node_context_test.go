package reach

import (
	"context"
	"errors"
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

type contextNodeStoreStub struct {
	graph.Store
	err    error
	called bool
}

func (s *contextNodeStoreStub) GetNodeContext(context.Context, string) (*graph.Node, error) {
	s.called = true
	return nil, s.err
}

func TestGetNodeContextUsesOptionalStoreMethod(t *testing.T) {
	s := &contextNodeStoreStub{err: context.Canceled}
	_, err := getNodeContext(context.Background(), s, "seed")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getNodeContext error = %v, want context.Canceled", err)
	}
	if !s.called {
		t.Fatal("optional GetNodeContext was not called")
	}
}

type legacyNodeStoreStub struct {
	graph.Store
	called bool
}

func (s *legacyNodeStoreStub) GetNode(string) *graph.Node {
	s.called = true
	return nil
}

func TestGetNodeContextChecksCancellationBeforeLegacyFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &legacyNodeStoreStub{}

	_, err := getNodeContext(ctx, s, "seed")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("getNodeContext error = %v, want context.Canceled", err)
	}
	if s.called {
		t.Fatal("legacy GetNode called after cancellation")
	}
}
