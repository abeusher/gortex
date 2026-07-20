package mcp

import (
	"context"
	"errors"
	"testing"
)

func resetExpensiveAnalysisSlots(t *testing.T) {
	t.Helper()
	for {
		select {
		case <-expensiveAnalysisSlots:
		default:
			return
		}
	}
}

func TestAcquireAnalyzeAdmissionRejectsConcurrentExpensiveWork(t *testing.T) {
	resetExpensiveAnalysisSlots(t)
	t.Cleanup(func() { resetExpensiveAnalysisSlots(t) })

	s := &Server{}
	release, err := s.acquireAnalyzeAdmission(context.Background(), "dead_code")
	if err != nil {
		t.Fatalf("first admission: %v", err)
	}
	defer release()

	if _, err := s.acquireAnalyzeAdmission(context.Background(), "cycles"); !errors.Is(err, errAnalysisBusy) {
		t.Fatalf("second expensive admission error = %v, want %v", err, errAnalysisBusy)
	}
}

func TestAcquireAnalyzeAdmissionDoesNotGateLightweightKinds(t *testing.T) {
	resetExpensiveAnalysisSlots(t)
	t.Cleanup(func() { resetExpensiveAnalysisSlots(t) })

	s := &Server{}
	release, err := s.acquireAnalyzeAdmission(context.Background(), "dead_code")
	if err != nil {
		t.Fatalf("expensive admission: %v", err)
	}
	defer release()

	lightRelease, err := s.acquireAnalyzeAdmission(context.Background(), "todos")
	if err != nil {
		t.Fatalf("lightweight admission: %v", err)
	}
	lightRelease()
}

func TestAcquireAnalyzeAdmissionHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (&Server{}).acquireAnalyzeAdmission(ctx, "dead_code"); !errors.Is(err, context.Canceled) {
		t.Fatalf("admission error = %v, want %v", err, context.Canceled)
	}
}
