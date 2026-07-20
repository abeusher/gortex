package mcp

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/rerank"
)

func TestExploreSyntacticAnchorsNormalizeFlagsAndIdentifiers(t *testing.T) {
	anchors := exploreSyntacticAnchors("replacement breaks when --replace --only-matching use replace_all through DevtoolsOptions; ignore `quoted_name`")
	require.Len(t, anchors, 3)
	require.Equal(t, "replace", anchors[0].compact)
	require.Equal(t, "onlymatching", anchors[1].compact)
	require.Equal(t, "devtoolsoptions", anchors[2].compact)
}

func TestExploreSyntacticAnchorIdentifierPrefixMorphology(t *testing.T) {
	anchors := exploreSyntacticAnchors("the --replace path fails")
	require.Len(t, anchors, 1)
	anchor := anchors[0]
	for _, identifier := range []string{"Replacer", "replace_all", "replaceAll"} {
		require.True(t, exploreSyntacticAnchorMatchesIdentifier(anchor, identifier), identifier)
	}
	require.False(t, exploreSyntacticAnchorMatchesIdentifier(anchor, "configure_context"))
}

func TestReserveExploreSyntacticAnchorCandidatesPreservesHeadAndDiversity(t *testing.T) {
	makeCandidate := func(id, name, file string) *rerank.Candidate {
		return &rerank.Candidate{Node: &graph.Node{
			ID: id, Name: name, QualName: name, Kind: graph.KindFunction, FilePath: file,
		}}
	}
	candidates := []*rerank.Candidate{
		makeCandidate("semantic", "search_execution", "search.rs"),
		makeCandidate("noise-a", "configure_output", "args.rs"),
		makeCandidate("noise-b", "print_match", "printer.rs"),
		makeCandidate("replace", "replace_all", "util.rs"),
		makeCandidate("only", "only_matching", "config.rs"),
	}

	got := reserveExploreSyntacticAnchorCandidates(
		"interaction between --replace and --only-matching",
		candidates,
		map[int]string{0: "replace", 1: "only"},
		3,
	)
	require.Equal(t, "semantic", got[0].Node.ID)
	window := map[string]bool{}
	for _, candidate := range got[:3] {
		window[candidate.Node.ID] = true
	}
	require.True(t, window["replace"])
	require.True(t, window["only"])
}

func TestExploreSyntacticAnchorCandidatePrefersCallableExactStem(t *testing.T) {
	anchor := exploreSyntacticAnchors("failure in --replace")[0]
	candidates := []*rerank.Candidate{
		{Node: &graph.Node{ID: "replacement", Name: "Replacement", Kind: graph.KindType, FilePath: "args.rs"}},
		{Node: &graph.Node{ID: "replacer", Name: "Replacer", Kind: graph.KindType, FilePath: "replacer.rs"}},
		{Node: &graph.Node{ID: "replace-all", Name: "replace_all", Kind: graph.KindMethod, FilePath: "util.rs"}},
	}

	got := exploreSyntacticAnchorCandidate(anchor, candidates, query.QueryOptions{}, map[string]struct{}{}, map[string]struct{}{})
	require.NotNil(t, got)
	require.Equal(t, "replace-all", got.Node.ID)
}

func TestExploreSyntacticAnchorEvidenceBlocksMissingImplementation(t *testing.T) {
	target := exploreTarget{
		node: &graph.Node{
			ID: "repo/config.rs::multi_line", Name: "multi_line", QualName: "Config.multi_line",
			Kind: graph.KindMethod, FilePath: "repo/config.rs",
		},
		source:                "fn multi_line(&mut self, yes: bool) { if yes { self.multi_line = true; } }",
		conceptImplementation: true,
	}
	require.False(t, exploreSyntacticAnchorEvidenceReady("replacement fails with --replace and --multiline", []exploreTarget{target}))
}

func TestExploreAnswerReadyRequiresAllSyntacticAnchors(t *testing.T) {
	target := exploreTarget{
		node: &graph.Node{
			ID: "repo/util.rs::replace_multiline", Name: "replace_multiline", QualName: "util.replace_multiline",
			Kind: graph.KindFunction, FilePath: "repo/util.rs",
		},
		source: `fn replace_multiline(input: &str) -> String {
		if input.contains('\n') { perform_replacement(input) } else { input.to_owned() }
	}`,
		conceptImplementation: true,
	}
	require.True(t, exploreAnswerReady("replacement behavior for --replace and --multiline", []exploreTarget{target}))

	missing := target
	missing.node = &graph.Node{
		ID: "repo/config.rs::multi_line", Name: "multi_line", QualName: "Config.multi_line",
		Kind: graph.KindMethod, FilePath: "repo/config.rs",
	}
	missing.source = "fn multi_line(&mut self, yes: bool) { if yes { self.multi_line = true; } }"
	require.False(t, exploreAnswerReady("replacement behavior for --replace and --multiline", []exploreTarget{missing}))
}

func TestExploreDraftGenericCandidateRecognizesOneStepForwarders(t *testing.T) {
	fixtures := []struct {
		name   string
		source string
	}{
		{
			name: "rust tail expression",
			source: `fn replace_with_captures_at(&self, caps: &Captures, dst: &mut Vec<u8>, at: usize) {
				(*self).replace_with_captures_at(caps, dst, at)
			}`,
		},
		{name: "csharp expression body", source: "string Replace(string value) => inner.Replace(value);"},
		{name: "kotlin expression body", source: "fun replace(value: String) = delegate.replace(value)"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			node := &graph.Node{ID: fixture.name, Name: "replace_with_captures_at", Kind: graph.KindMethod}
			require.True(t, exploreDraftGenericCandidate(node, fixture.source))
		})
	}
}

func TestExploreAnswerReadyRejectsGenericAnchorForwarder(t *testing.T) {
	target := exploreTarget{
		node: &graph.Node{
			ID: "repo/replacer.rs::replace_with_captures_at", Name: "replace_with_captures_at",
			QualName: "ReplacerRef.replace_with_captures_at", Kind: graph.KindMethod, FilePath: "repo/replacer.rs",
		},
		source: `fn replace_with_captures_at(&self, caps: &Captures, dst: &mut Vec<u8>, at: usize) {
			(*self).replace_with_captures_at(caps, dst, at)
		}`,
		conceptImplementation: true,
	}
	require.False(t, exploreAnswerReady("locate replacement implementation for --replace", []exploreTarget{target}))
}

func TestRefinementPrefersConcreteAnchorOwnerOverForwarder(t *testing.T) {
	wrapper := exploreTarget{
		node: &graph.Node{
			ID: "repo/replacer.rs::ref_forwarder", Name: "replace_with_captures_at",
			QualName: "ReplacerRef.replace_with_captures_at", Kind: graph.KindMethod, FilePath: "repo/replacer.rs",
		},
		source: `fn replace_with_captures_at(&self, caps: &Captures, dst: &mut Vec<u8>, at: usize) {
			(*self).replace_with_captures_at(caps, dst, at)
		}`,
	}
	implementation := exploreTarget{
		node: &graph.Node{
			ID: "repo/util.rs::Replacer.replace_all", Name: "replace_all",
			QualName: "Replacer.replace_all", Kind: graph.KindMethod, FilePath: "repo/util.rs",
		},
		source: `fn replace_all(&self, haystack: &[u8], dst: &mut Vec<u8>) {
			for captures in self.captures_iter(haystack) { self.append(captures, dst); }
		}`,
	}

	require.Equal(t, implementation.node.ID, explorePreferredRefinementSymbol(
		"replacement implementation for --replace", []exploreTarget{wrapper, implementation},
	))
}
