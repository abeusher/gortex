package graph

import "testing"

func TestNodeRetrievalMetadataFallbackAndOverride(t *testing.T) {
	n := &Node{
		QualName: "pkg.Service.Handle",
		Meta: map[string]any{
			"signature": "func (s *Service) Handle()",
			"doc":       "Handles requests.",
		},
	}
	fallback := n.RetrievalMetadata()
	if fallback.Signature != "func (s *Service) Handle()" || fallback.QualName != n.QualName || fallback.Doc != "Handles requests." {
		t.Fatalf("fallback metadata = %#v", fallback)
	}

	SetRetrievalMetadata(n, RetrievalMetadata{
		Signature: "func Service.Handle()",
		QualName:  "Service.Handle",
		Doc:       "Handle a request.",
	})
	got := n.RetrievalMetadata()
	if got.Signature != "func Service.Handle()" || got.QualName != "Service.Handle" || got.Doc != "Handle a request." {
		t.Fatalf("normalized metadata = %#v", got)
	}
	if n.QualName != "pkg.Service.Handle" || n.Meta["signature"] != "func (s *Service) Handle()" || n.Meta["doc"] != "Handles requests." {
		t.Fatalf("parser metadata mutated: %#v", n)
	}

	SetRetrievalMetadata(n, RetrievalMetadata{})
	if got := n.RetrievalMetadata(); got != fallback {
		t.Fatalf("fallback after clearing = %#v, want %#v", got, fallback)
	}
}

func TestSetRetrievalMetadataEmptyDoesNotAllocateMeta(t *testing.T) {
	n := &Node{}
	SetRetrievalMetadata(n, RetrievalMetadata{})
	if n.Meta != nil {
		t.Fatalf("empty metadata allocated map: %#v", n.Meta)
	}
}
