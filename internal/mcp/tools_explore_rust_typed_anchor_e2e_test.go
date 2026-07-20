package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/graph/store_sqlite"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
)

func newIndexedRustTypedAnchorLocalizationServer(t *testing.T) *Server {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "src"), 0o755))
	files := map[string]string{
		"lib.rs": `
pub mod duplicate_output;
pub mod line_buffer;
pub mod multiline_printer;
pub mod repeated_match;
pub mod replacement_span;
pub mod same_match;
pub mod standard;
pub mod util;
`,
		"util.rs": `
pub struct Replacer<M> {
    matcher: M,
}

impl<M> Replacer<M> {
    pub fn replace_all(&mut self, haystack: &[u8], replacement: &[u8]) -> Vec<u8> {
        let _ = (&self.matcher, replacement);
        haystack.to_vec()
    }
}
`,
		"standard.rs": `
use crate::util::Replacer;

pub struct DefaultMatcher;
pub struct StandardSink {
    /// Applies one replacement to a multiline match without duplicate output.
    replacer: Replacer<DefaultMatcher>,
    replacement: Vec<u8>,
}

impl StandardSink {
    pub fn replace(&mut self, bytes: &[u8]) -> Vec<u8> {
        self.replacer.replace_all(bytes, &self.replacement)
    }

    pub fn print_match(&self, bytes: &[u8]) -> bool {
        !bytes.is_empty()
    }
}
`,
		"line_buffer.rs": `
pub fn replace_bytes(bytes: &[u8]) -> Vec<u8> { bytes.to_vec() }
pub fn multiline_match(bytes: &[u8]) -> bool { bytes.contains(&b'\n') }
pub fn print_matching_line(bytes: &[u8]) -> bool { !bytes.is_empty() }
pub fn duplicate_match(bytes: &[u8]) -> Vec<u8> { bytes.to_vec() }
pub fn replacement_for_match(bytes: &[u8]) -> Vec<u8> { bytes.to_vec() }
pub fn match_across_lines(bytes: &[u8]) -> bool { bytes.windows(2).any(|w| w == b"\n\n") }
`,
		"duplicate_output.rs": `
pub fn prevent_duplicate_output_for_multiline_match(bytes: &[u8]) -> bool { !bytes.is_empty() }
`,
		"multiline_printer.rs": `
pub fn print_multiline_match_once(bytes: &[u8]) -> bool { !bytes.is_empty() }
`,
		"repeated_match.rs": `
pub fn avoid_printing_same_match_multiple_times(bytes: &[u8]) -> bool { !bytes.is_empty() }
`,
		"replacement_span.rs": `
pub fn replace_match_spanning_multiple_lines(bytes: &[u8]) -> Vec<u8> { bytes.to_vec() }
`,
		"same_match.rs": `
pub fn printer_repeats_same_match(bytes: &[u8]) -> bool { !bytes.is_empty() }
`,
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, "src", name), []byte(content), 0o644))
	}

	store, err := store_sqlite.Open(filepath.Join(t.TempDir(), "graph.sqlite"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	registry := parser.NewRegistry()
	registry.Register(languages.NewRustExtractor())
	idx := indexer.New(store, registry, config.IndexConfig{Workers: 1}, zap.NewNop())
	idx.SetRepoPrefix("rust-fixture")
	idx.SetWorkspaceID("rust-fixture")
	idx.SetProjectID("rust-fixture")
	_, err = idx.IndexCtx(context.Background(), root)
	require.NoError(t, err)

	engine := query.NewEngine(store)
	engine.SetSearchProvider(idx.Search)
	return NewServer(engine, store, idx, nil, zap.NewNop(), nil)
}

func callIndexedRustTypedAnchorLocalization(t *testing.T, server *Server, task string, maxSymbols int) localizationExploreEnvelope {
	t.Helper()

	request := mcpgo.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"task":          task,
		"localize":      true,
		"max_symbols":   maxSymbols,
		"token_budget":  1600,
		"repository_id": "rust-fixture",
	}
	result, err := server.handleExplore(context.Background(), request)
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)

	text, ok := result.Content[0].(mcpgo.TextContent)
	require.True(t, ok)
	var envelope localizationExploreEnvelope
	require.NoError(t, json.Unmarshal([]byte(text.Text), &envelope))
	return envelope
}

func TestIndexedRustLocalizationProjectsTypedAnchorThroughFinalEnvelope(t *testing.T) {
	const (
		maxSymbols = 10
		task       = "--multiline with --replace causes duplicate output when a match spans multiple lines and printer prints the same match multiple times"
	)
	server := newIndexedRustTypedAnchorLocalizationServer(t)
	const (
		memberID = "rust-fixture/src/util.rs::Replacer<M>.replace_all"
		ownerID  = "rust-fixture/src/util.rs::Replacer"
	)
	var memberOfTargets []string
	for _, edge := range server.graph.GetOutEdges(memberID) {
		if edge.Kind == graph.EdgeMemberOf {
			memberOfTargets = append(memberOfTargets, edge.To)
		}
	}
	require.Equal(t, []string{ownerID}, memberOfTargets)
	require.NotNil(t, server.graph.GetNode(ownerID), "generic impl member_of target must resolve to the bare declaration")

	envelope := callIndexedRustTypedAnchorLocalization(t, server, task, maxSymbols)

	require.LessOrEqual(t, len(envelope.Evidence), maxSymbols, "max_symbols must bound the packed evidence: %#v", envelope.Evidence)
	require.LessOrEqual(t, len(envelope.Symbols), maxSymbols, "max_symbols must bound the symbol projection: %#v", envelope.Symbols)

	found := false
	for _, evidence := range envelope.Evidence {
		if filepath.Base(evidence.File) != "util.rs" || evidence.Name != "replace_all" {
			continue
		}
		found = true
		require.Equal(t, localizationProvenanceTypedAnchorProjection, evidence.Provenance, "expected projected evidence, got %#v", envelope.Evidence)
		break
	}
	require.True(t, found, "expected typed-anchor projection to retain util.rs::replace_all, got %#v", envelope.Evidence)
}
