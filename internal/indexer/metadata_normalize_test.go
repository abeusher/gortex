package indexer

import (
	"reflect"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

type metadataFixtureExtractor struct {
	result *parser.ExtractionResult
}

func (e metadataFixtureExtractor) Language() string     { return "rust" }
func (e metadataFixtureExtractor) Extensions() []string { return []string{".rs"} }
func (e metadataFixtureExtractor) Extract(string, []byte) (*parser.ExtractionResult, error) {
	return e.result, nil
}

func TestExtractFileNormalizesMetadataAtSharedBoundary(t *testing.T) {
	src := []byte("fn resolve(value: Input) -> Output { value.into() }\n")
	n := &graph.Node{
		ID: "src/lib.rs::resolve", Kind: graph.KindFunction, Name: "resolve",
		FilePath: "src/lib.rs", StartLine: 1, EndLine: 1, Language: "rust",
		Meta: map[string]any{"signature": "fn resolve(...)"},
	}
	idx := &Indexer{}
	result, skipped, err := idx.extractFile(nil, nil, "src/lib.rs", "src/lib.rs", "rust", metadataFixtureExtractor{
		result: &parser.ExtractionResult{Nodes: []*graph.Node{n}},
	}, src)
	if err != nil || skipped {
		t.Fatalf("extractFile() err = %v, skipped = %v", err, skipped)
	}
	if got := result.Nodes[0].RetrievalMetadata().Signature; got != "fn resolve(value: Input) -> Output" {
		t.Fatalf("search signature = %q", got)
	}
}

func TestNormalizeExtractionMetadataRustMethod(t *testing.T) {
	src := []byte("impl Worker {\n    /// Runs a queued job.\n    pub fn run<T>(\n        &self,\n        item: T,\n    ) -> Result<(), Error> {\n    }\n}\n")
	owner := &graph.Node{ID: "src/worker.rs::Worker", Name: "Worker", FilePath: "src/worker.rs", StartLine: 1, Language: "rust"}
	method := &graph.Node{
		ID: "src/worker.rs::Worker.run", Kind: graph.KindMethod, Name: "run",
		FilePath: "src/worker.rs", StartLine: 3, EndLine: 7, Language: "rust",
		Meta: map[string]any{"signature": "fn run(...)", "receiver": "Worker", "doc": "/// Runs a queued job."},
	}
	result := &parser.ExtractionResult{
		Nodes: []*graph.Node{owner, method},
		Edges: []*graph.Edge{{From: method.ID, To: owner.ID, Kind: graph.EdgeMemberOf}},
	}

	normalizeExtractionMetadata(result, src)

	if got := method.Meta["signature"]; got != "fn run(...)" {
		t.Fatalf("parser signature mutated: %v", got)
	}
	if method.QualName != "" {
		t.Fatalf("resolver QualName mutated: %q", method.QualName)
	}
	retrieval := method.RetrievalMetadata()
	if retrieval.Signature != "pub fn run<T>( &self, item: T, ) -> Result<(), Error>" {
		t.Fatalf("search signature = %q", retrieval.Signature)
	}
	if retrieval.QualName != "Worker.run" {
		t.Fatalf("search qualifier = %q", retrieval.QualName)
	}
	if retrieval.Doc != "Runs a queued job." {
		t.Fatalf("search doc = %q", retrieval.Doc)
	}
}

func TestNormalizeExtractionMetadataTypeScriptFallbacks(t *testing.T) {
	src := []byte("/**\n * Validates an incoming request.\n */\nexport async function validate(\n  input: Request,\n): Promise<Result> {\n  return check(input)\n}\n")
	n := &graph.Node{
		ID: "src/auth/index.ts::validate", Kind: graph.KindFunction, Name: "validate",
		FilePath: "src/auth/index.ts", StartLine: 4, EndLine: 8, Language: "typescript",
		Meta: map[string]any{"signature": "function validate()"},
	}

	normalizeExtractionMetadata(&parser.ExtractionResult{Nodes: []*graph.Node{n}}, src)

	retrieval := n.RetrievalMetadata()
	if retrieval.Signature != "export async function validate( input: Request, ): Promise<Result>" {
		t.Fatalf("search signature = %q", retrieval.Signature)
	}
	if retrieval.Doc != "Validates an incoming request." {
		t.Fatalf("search doc = %q", retrieval.Doc)
	}
	if retrieval.QualName != "" {
		t.Fatalf("unowned top-level qualifier = %q", retrieval.QualName)
	}
}

func TestNormalizeExtractionMetadataPreservesExplicitQualName(t *testing.T) {
	n := &graph.Node{
		ID: "service.go::Service.Handle", Kind: graph.KindMethod, Name: "Handle",
		QualName: "example.Service.Handle", FilePath: "service.go", StartLine: 1,
		Meta: map[string]any{"signature": "func (s *Service) Handle(ctx context.Context)", "doc": "  Handles   requests.  "},
	}

	normalizeExtractionMetadata(&parser.ExtractionResult{Nodes: []*graph.Node{n}}, nil)

	if n.QualName != "example.Service.Handle" {
		t.Fatalf("QualName mutated: %q", n.QualName)
	}
	retrieval := n.RetrievalMetadata()
	if retrieval.QualName != n.QualName {
		t.Fatalf("search qualifier = %q", retrieval.QualName)
	}
	if retrieval.Doc != "Handles requests." {
		t.Fatalf("search doc = %q", retrieval.Doc)
	}
}

func TestNormalizeExtractionMetadataDoesNotCopyOwnerTextIntoParam(t *testing.T) {
	src := []byte("/// Resolves an input value.\nfn resolve(value: Input) -> Output { value.into() }\n")
	fn := &graph.Node{
		ID: "src/lib.rs::resolve", Kind: graph.KindFunction, Name: "resolve",
		FilePath: "src/lib.rs", StartLine: 2, EndLine: 2, Language: "rust",
		Meta: map[string]any{"signature": "fn resolve(...)"},
	}
	param := &graph.Node{
		ID: fn.ID + "#param:value", Kind: graph.KindParam, Name: "value",
		FilePath: fn.FilePath, StartLine: fn.StartLine, EndLine: fn.EndLine, Language: fn.Language,
	}

	normalizeExtractionMetadata(&parser.ExtractionResult{Nodes: []*graph.Node{fn, param}}, src)

	owner := fn.RetrievalMetadata()
	if owner.Signature != "fn resolve(value: Input) -> Output" || owner.Doc != "Resolves an input value." {
		t.Fatalf("owner metadata = %#v", owner)
	}
	child := param.RetrievalMetadata()
	if child.Signature != "" || child.Doc != "" || child.QualName != "" {
		t.Fatalf("parameter inherited owner metadata: %#v", child)
	}
	fields := searchIndexFields(param, "")
	if len(fields) != 5 || fields[2] != "" || fields[3] != "" || fields[4] != "" {
		t.Fatalf("parameter search fields contain owner payload: %#v", fields)
	}
	if joined := strings.Join(fields, " "); strings.Contains(joined, "resolve") || strings.Contains(joined, "Resolves") {
		t.Fatalf("parameter duplicated enclosing declaration: %q", joined)
	}
}

func TestShouldNormalizeDefinitionMetadata(t *testing.T) {
	allowed := []graph.NodeKind{
		graph.KindFunction, graph.KindMethod, graph.KindType, graph.KindInterface,
		graph.KindVariable, graph.KindField, graph.KindClosure, graph.KindConstant,
		graph.KindEnumMember, graph.KindMacro,
	}
	for _, kind := range allowed {
		if !shouldNormalizeDefinitionMetadata(kind) {
			t.Errorf("definition kind %q rejected", kind)
		}
	}
	denied := []graph.NodeKind{
		graph.KindParam, graph.KindLocal, graph.KindImport, graph.KindBuiltin,
		graph.KindFile, graph.KindPackage, graph.KindGenericParam, graph.KindContract,
		graph.KindModule, graph.KindDoc, graph.KindEvent, graph.KindString,
	}
	for _, kind := range denied {
		if shouldNormalizeDefinitionMetadata(kind) {
			t.Errorf("non-definition kind %q accepted", kind)
		}
	}
}

func TestSearchIndexFieldsUseNormalizedRetrievalMetadata(t *testing.T) {
	n := &graph.Node{
		Kind: graph.KindFunction, Name: "validate", QualName: "legacy.validate", FilePath: "repo/src/auth.ts",
		Meta: map[string]any{
			"signature": "function validate()",
			"doc":       "legacy docs",
		},
	}
	graph.SetRetrievalMetadata(n, graph.RetrievalMetadata{
		Signature: "function validate(input: Request): Result",
		QualName:  "auth.validate",
		Doc:       "Validates incoming requests.",
	})

	want := []string{"validate", "repo/src/auth.ts", "auth.validate", "function validate(input: Request): Result", "Validates incoming requests."}
	if got := searchIndexFields(n, ""); !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
	tokens := strings.Fields(ftsTokensFor(n, ""))
	count := 0
	for _, token := range tokens {
		if token == "auth" {
			count++
		}
	}
	if count != 2 { // path plus retrieval qualifier; no duplicate QualName append
		t.Fatalf("auth token count = %d in %q", count, strings.Join(tokens, " "))
	}
}
