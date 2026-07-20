package mcp

import (
	"testing"

	"github.com/zzet/gortex/internal/analysis"
)

func TestReleaseTransientAnalysisDropsPublishedCompatibilityViews(t *testing.T) {
	s := &Server{
		analysisGenerationReady: true,
		communities:             &analysis.CommunityResult{},
		processes:               &analysis.ProcessResult{},
		hotspots:                []analysis.HotspotEntry{{ID: "hot"}},
		hotspotsReady:           true,
	}

	if !s.releaseTransientAnalysisIfIdle() {
		t.Fatal("expected published transient analysis to be released")
	}
	if s.communities != nil || s.processes != nil || s.hotspots != nil || s.hotspotsReady {
		t.Fatalf("transient analysis retained: communities=%v processes=%v hotspots=%v ready=%v",
			s.communities != nil, s.processes != nil, s.hotspots != nil, s.hotspotsReady)
	}
}

func TestReleaseTransientAnalysisKeepsUnpublishedCompatibilityViews(t *testing.T) {
	communities := &analysis.CommunityResult{}
	s := &Server{communities: communities}

	if s.releaseTransientAnalysisIfIdle() {
		t.Fatal("unpublished transient analysis must remain available")
	}
	if s.communities != communities {
		t.Fatal("unpublished compatibility view was dropped")
	}
}
