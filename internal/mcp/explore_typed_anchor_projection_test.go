package mcp

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/search/rerank"
)

type exploreTypedAnchorTestReader struct {
	nodes       map[string]*graph.Node
	in          map[string][]*graph.Edge
	out         map[string][]*graph.Edge
	delay       time.Duration
	nodeBatches int
	inBatches   int
	outBatches  int
}

func newExploreTypedAnchorTestReader(nodes ...*graph.Node) *exploreTypedAnchorTestReader {
	reader := &exploreTypedAnchorTestReader{
		nodes: make(map[string]*graph.Node, len(nodes)),
		in:    make(map[string][]*graph.Edge),
		out:   make(map[string][]*graph.Edge),
	}
	for _, node := range nodes {
		reader.nodes[node.ID] = node
	}
	return reader
}

func (r *exploreTypedAnchorTestReader) addEdge(from, to string, kind graph.EdgeKind) {
	edge := &graph.Edge{From: from, To: to, Kind: kind}
	r.out[from] = append(r.out[from], edge)
	r.in[to] = append(r.in[to], edge)
}

func (r *exploreTypedAnchorTestReader) GetNodesByIDs(ids []string) map[string]*graph.Node {
	r.nodeBatches++
	time.Sleep(r.delay)
	result := make(map[string]*graph.Node, len(ids))
	for _, id := range ids {
		if node := r.nodes[id]; node != nil {
			result[id] = node
		}
	}
	return result
}

func (r *exploreTypedAnchorTestReader) GetInEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	r.inBatches++
	time.Sleep(r.delay)
	result := make(map[string][]*graph.Edge, len(ids))
	for _, id := range ids {
		result[id] = r.in[id]
	}
	return result
}

func (r *exploreTypedAnchorTestReader) GetOutEdgesByNodeIDs(ids []string) map[string][]*graph.Edge {
	r.outBatches++
	time.Sleep(r.delay)
	result := make(map[string][]*graph.Edge, len(ids))
	for _, id := range ids {
		result[id] = r.out[id]
	}
	return result
}

func removeExploreTypedAnchorTestEdge(edges []*graph.Edge, from, to string, kind graph.EdgeKind) []*graph.Edge {
	kept := edges[:0]
	for _, edge := range edges {
		if edge.From == from && edge.To == to && edge.Kind == kind {
			continue
		}
		kept = append(kept, edge)
	}
	return kept
}

func exploreTypedAnchorCandidateIDs(candidates []*rerank.Candidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate != nil && candidate.Node != nil {
			ids = append(ids, candidate.Node.ID)
		}
	}
	return ids
}

type exploreTypedAnchorFixture struct {
	task       string
	reader     *exploreTypedAnchorTestReader
	candidates []*rerank.Candidate
	protected  map[int]string
	field      *graph.Node
	consumer   *graph.Node
	member     *graph.Node
	wrongType  *graph.Node
}

func exploreTypedAnchorNode(id, name, path, language string, kind graph.NodeKind) *graph.Node {
	return &graph.Node{
		ID: id, Name: name, QualName: name, Kind: kind,
		FilePath: path, Language: language,
		WorkspaceID: "workspace", ProjectID: "project", RepoPrefix: "repo",
	}
}

func newExploreTypedAnchorFixture(language, task, fieldName, fieldType, consumerName, memberName string, typedAs bool) exploreTypedAnchorFixture {
	owner := exploreTypedAnchorNode(language+"-owner", "ResponseSink", "sink."+language, language, graph.KindType)
	field := exploreTypedAnchorNode(language+"-field", fieldName, "sink."+language, language, graph.KindField)
	field.Meta = map[string]any{"field_type": fieldType}
	consumer := exploreTypedAnchorNode(language+"-consumer", consumerName, "sink."+language, language, graph.KindMethod)
	targetType := exploreTypedAnchorNode(language+"-target-type", fieldType, "codec."+language, language, graph.KindType)
	member := exploreTypedAnchorNode(language+"-member", memberName, "codec."+language, language, graph.KindMethod)
	wrongType := exploreTypedAnchorNode(language+"-wrong-type", "OutputBuffer", "buffer."+language, language, graph.KindType)
	wrongMember := exploreTypedAnchorNode(language+"-wrong-member", memberName, "buffer."+language, language, graph.KindMethod)
	wrongConsumer := exploreTypedAnchorNode(language+"-wrong-consumer", memberName, "sink."+language, language, graph.KindMethod)
	anchorDistractor := exploreTypedAnchorNode(language+"-anchor-distractor", memberName+"_bytes", "config."+language, language, graph.KindFunction)
	noiseB := exploreTypedAnchorNode(language+"-noise-b", "print_output", "print."+language, language, graph.KindFunction)
	noiseC := exploreTypedAnchorNode(language+"-noise-c", "parse_output", "parse."+language, language, graph.KindFunction)
	noiseD := exploreTypedAnchorNode(language+"-noise-d", "write_output", "write."+language, language, graph.KindFunction)

	reader := newExploreTypedAnchorTestReader(
		owner, field, consumer, targetType, member, wrongType, wrongMember, wrongConsumer,
		anchorDistractor, noiseB, noiseC, noiseD,
	)
	reader.addEdge(field.ID, owner.ID, graph.EdgeMemberOf)
	reader.addEdge(consumer.ID, owner.ID, graph.EdgeMemberOf)
	reader.addEdge(wrongConsumer.ID, owner.ID, graph.EdgeMemberOf)
	reader.addEdge(consumer.ID, field.ID, graph.EdgeReads)
	reader.addEdge(consumer.ID, member.ID, graph.EdgeCalls)
	reader.addEdge(member.ID, targetType.ID, graph.EdgeMemberOf)
	reader.addEdge(wrongConsumer.ID, wrongMember.ID, graph.EdgeCalls)
	reader.addEdge(wrongMember.ID, wrongType.ID, graph.EdgeMemberOf)
	if typedAs {
		reader.addEdge(field.ID, targetType.ID, graph.EdgeTypedAs)
	}

	candidates := []*rerank.Candidate{
		{Node: anchorDistractor, Score: 10},
		{Node: noiseB, Score: 9},
		{Node: field, Score: 8},
		{Node: noiseC, Score: 7},
		{Node: noiseD, Score: 6},
	}
	protected := make(map[int]string)
	for index, anchor := range exploreSyntacticAnchors(task) {
		if exploreSyntacticAnchorMatchesNode(anchor, anchorDistractor) {
			protected[index] = anchorDistractor.ID
			break
		}
	}
	return exploreTypedAnchorFixture{
		task: task, reader: reader, candidates: candidates, protected: protected,
		field: field, consumer: consumer, member: member, wrongType: wrongType,
	}
}

func exploreTypedAnchorTestScope() query.QueryOptions {
	return query.QueryOptions{
		WorkspaceID: "workspace", ProjectID: "project",
		RepoAllow: map[string]bool{"repo": true},
	}
}

func exploreTypedAnchorCandidateByID(candidates []*rerank.Candidate, id string) *rerank.Candidate {
	for _, candidate := range candidates {
		if candidate != nil && candidate.Node != nil && candidate.Node.ID == id {
			return candidate
		}
	}
	return nil
}

func TestProjectExploreTypedAnchorCandidatesAcrossLanguages(t *testing.T) {
	tests := []struct {
		name, language, task, field, fieldType, consumer, member string
		typedAs                                                  bool
	}{
		{
			name: "rust field metadata", language: "rs",
			task:  "--replace causes duplicate output for a multiline match",
			field: "replacer", fieldType: "Replacer<M>", consumer: "replace", member: "replace_all",
		},
		{
			name: "csharp typed edge", language: "cs",
			task:  "--serialize produces duplicate output bytes",
			field: "_serializer", fieldType: "IJsonSerializer", consumer: "WriteResponse", member: "SerializeAsync",
			typedAs: true,
		},
		{
			name: "typescript compound anchor", language: "ts",
			task:  "format_line duplicates the rendered output",
			field: "formatter", fieldType: "LineFormatter", consumer: "writeLine", member: "formatLine",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExploreTypedAnchorFixture(
				test.language, test.task, test.field, test.fieldType, test.consumer, test.member, test.typedAs,
			)
			if len(fixture.protected) != 1 {
				t.Fatalf("fixture failed to protect anchor distractor: %#v", fixture.protected)
			}
			got := projectExploreTypedAnchorCandidates(
				context.Background(), fixture.task, fixture.candidates, fixture.reader,
				exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
			)
			if len(got) != len(fixture.candidates) {
				t.Fatalf("projection changed hard candidate cap: got %d want %d", len(got), len(fixture.candidates))
			}
			for _, node := range []*graph.Node{fixture.field, fixture.consumer, fixture.member} {
				if exploreTypedAnchorCandidateByID(got, node.ID) == nil {
					t.Fatalf("graph-proven projection omitted %s: %#v", node.ID, got)
				}
			}
			for _, node := range []*graph.Node{fixture.consumer, fixture.member} {
				candidate := exploreTypedAnchorCandidateByID(got, node.ID)
				if candidate.Signals[exploreTypedAnchorProjectionSignal] != 1 {
					t.Fatalf("projected candidate %s lacks provenance signal: %#v", node.ID, candidate.Signals)
				}
			}
			if exploreTypedAnchorCandidateByID(got, test.language+"-wrong-member") != nil {
				t.Fatalf("same-name member on unrelated field type was admitted")
			}
		})
	}
}

func TestProjectExploreTypedAnchorCandidatesRequiresCompleteProof(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*exploreTypedAnchorFixture)
	}{
		{
			name: "protected ordinary candidate required",
			mutate: func(f *exploreTypedAnchorFixture) {
				f.protected = nil
			},
		},
		{
			name: "resolved call required",
			mutate: func(f *exploreTypedAnchorFixture) {
				f.reader.out[f.consumer.ID] = nil
			},
		},
		{
			name: "direct field reader must share owner",
			mutate: func(f *exploreTypedAnchorFixture) {
				f.reader.in["rs-owner"] = removeExploreTypedAnchorTestEdge(
					f.reader.in["rs-owner"], f.consumer.ID, "rs-owner", graph.EdgeMemberOf,
				)
				f.reader.out[f.consumer.ID] = removeExploreTypedAnchorTestEdge(
					f.reader.out[f.consumer.ID], f.consumer.ID, "rs-owner", graph.EdgeMemberOf,
				)
			},
		},
		{
			name: "declared type owner required",
			mutate: func(f *exploreTypedAnchorFixture) {
				f.reader.out[f.member.ID] = []*graph.Edge{{From: f.member.ID, To: f.wrongType.ID, Kind: graph.EdgeMemberOf}}
			},
		},
		{
			name: "aligned callee required",
			mutate: func(f *exploreTypedAnchorFixture) {
				f.member.Name = "clear"
				f.member.QualName = "clear"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExploreTypedAnchorFixture(
				"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
			)
			test.mutate(&fixture)
			got := projectExploreTypedAnchorCandidates(
				context.Background(), fixture.task, fixture.candidates, fixture.reader,
				exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
			)
			if exploreTypedAnchorCandidateByID(got, fixture.member.ID) != nil {
				t.Fatalf("incomplete proof admitted member %s", fixture.member.ID)
			}
			if len(got) != len(fixture.candidates) {
				t.Fatalf("conservative no-op changed candidate count: got %d want %d", len(got), len(fixture.candidates))
			}
		})
	}
}

func TestProjectExploreTypedAnchorCandidatesPreservesProtectedAnchorsAndCap(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace with format_line duplicates output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	anchors := exploreSyntacticAnchors(fixture.task)
	for index := range anchors {
		if _, present := fixture.protected[index]; !present {
			fixture.protected[index] = fixture.candidates[0].Node.ID
		}
	}
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if len(got) != len(fixture.candidates) {
		t.Fatalf("got %d candidates, want cap %d", len(got), len(fixture.candidates))
	}
	for _, id := range fixture.protected {
		if exploreTypedAnchorCandidateByID(got, id) == nil {
			t.Fatalf("projection evicted protected anchor %s", id)
		}
	}
}

func TestProjectExploreTypedAnchorCandidatesUsesFixedBatchPipelineDespiteDelay(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	fixture.reader.delay = 3 * time.Millisecond // seven stages exceed the removed 10 ms cutoff
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if exploreTypedAnchorCandidateByID(got, fixture.member.ID) == nil {
		t.Fatal("scheduler/database delay changed a structurally complete projection")
	}
	if fixture.reader.inBatches != 1 || fixture.reader.outBatches != 3 || fixture.reader.nodeBatches != 3 {
		t.Fatalf("batch pipeline = in:%d out:%d nodes:%d, want 1/3/3",
			fixture.reader.inBatches, fixture.reader.outBatches, fixture.reader.nodeBatches)
	}
}

func TestProjectExploreTypedAnchorCandidatesIsIndependentOfEdgeOrder(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	want := exploreTypedAnchorCandidateIDs(projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	))
	rng := rand.New(rand.NewSource(241))
	for iteration := 0; iteration < 50; iteration++ {
		for id := range fixture.reader.in {
			rng.Shuffle(len(fixture.reader.in[id]), func(i, j int) {
				fixture.reader.in[id][i], fixture.reader.in[id][j] = fixture.reader.in[id][j], fixture.reader.in[id][i]
			})
		}
		for id := range fixture.reader.out {
			rng.Shuffle(len(fixture.reader.out[id]), func(i, j int) {
				fixture.reader.out[id][i], fixture.reader.out[id][j] = fixture.reader.out[id][j], fixture.reader.out[id][i]
			})
		}
		got := exploreTypedAnchorCandidateIDs(projectExploreTypedAnchorCandidates(
			context.Background(), fixture.task, fixture.candidates, fixture.reader,
			exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
		))
		if len(got) != len(want) {
			t.Fatalf("iteration %d candidate count = %d, want %d", iteration, len(got), len(want))
		}
		for index := range want {
			if got[index] != want[index] {
				t.Fatalf("iteration %d candidate[%d] = %q, want %q", iteration, index, got[index], want[index])
			}
		}
	}
}

func TestProjectExploreTypedAnchorCandidatesOwnerFallbackRequiresAlignedConsumer(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	fixture.reader.in[fixture.field.ID] = removeExploreTypedAnchorTestEdge(
		fixture.reader.in[fixture.field.ID], fixture.consumer.ID, fixture.field.ID, graph.EdgeReads,
	)
	fixture.reader.out[fixture.consumer.ID] = removeExploreTypedAnchorTestEdge(
		fixture.reader.out[fixture.consumer.ID], fixture.consumer.ID, fixture.field.ID, graph.EdgeReads,
	)
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if exploreTypedAnchorCandidateByID(got, fixture.member.ID) == nil {
		t.Fatal("anchor-aligned owner member did not supply the conservative field-use fallback")
	}

	fixture = newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "flush", "replace_all", false,
	)
	fixture.reader.in[fixture.field.ID] = nil
	fixture.reader.out[fixture.consumer.ID] = removeExploreTypedAnchorTestEdge(
		fixture.reader.out[fixture.consumer.ID], fixture.consumer.ID, fixture.field.ID, graph.EdgeReads,
	)
	got = projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if exploreTypedAnchorCandidateByID(got, fixture.member.ID) != nil {
		t.Fatal("unrelated owner member was treated as proof that it consumes the field")
	}
}

func TestProjectExploreTypedAnchorCandidatesRejectsAmbiguousStrongestProof(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	altConsumer := exploreTypedAnchorNode("rs-alt-consumer", "replace_once", "sink.rs", "rs", graph.KindMethod)
	altMember := exploreTypedAnchorNode("rs-alt-member", "replace_first", "codec.rs", "rs", graph.KindMethod)
	fixture.reader.nodes[altConsumer.ID] = altConsumer
	fixture.reader.nodes[altMember.ID] = altMember
	fixture.reader.addEdge(altConsumer.ID, "rs-owner", graph.EdgeMemberOf)
	fixture.reader.addEdge(altConsumer.ID, fixture.field.ID, graph.EdgeReads)
	fixture.reader.addEdge(altConsumer.ID, altMember.ID, graph.EdgeCalls)
	fixture.reader.addEdge(altMember.ID, "rs-target-type", graph.EdgeMemberOf)
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if exploreTypedAnchorCandidateByID(got, fixture.member.ID) != nil ||
		exploreTypedAnchorCandidateByID(got, altMember.ID) != nil {
		t.Fatal("equally strong behavior routes were resolved by arbitrary ID order")
	}
}

func TestProjectExploreTypedAnchorCandidatesPreservesEarlierEvidenceReservations(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	fixture.candidates[1].Signals = map[string]float64{exploreSourceLiteralSignal: 1}
	noiseE := exploreTypedAnchorNode("rs-noise-e", "render_output", "render.rs", "rs", graph.KindFunction)
	fixture.reader.nodes[noiseE.ID] = noiseE
	fixture.candidates = append(fixture.candidates, &rerank.Candidate{Node: noiseE, Score: 5})
	implementationID := fixture.candidates[3].Node.ID
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, implementationID,
	)
	if len(got) != len(fixture.candidates) {
		t.Fatalf("projection returned %d candidates, want hard cap %d", len(got), len(fixture.candidates))
	}
	for _, id := range []string{
		fixture.candidates[0].Node.ID,
		fixture.candidates[1].Node.ID,
		implementationID,
		fixture.field.ID,
		fixture.consumer.ID,
		fixture.member.ID,
	} {
		if exploreTypedAnchorCandidateByID(got, id) == nil {
			t.Fatalf("projection evicted earlier or graph-mandatory evidence %s", id)
		}
	}
}

func TestProjectExploreTypedAnchorCandidatesFailsClosedOnCapOverflow(t *testing.T) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	for index := 0; index <= exploreTypedAnchorOwnerMemberLimit; index++ {
		fixture.reader.addEdge("overflow-member-"+string(rune(index+65)), "rs-owner", graph.EdgeMemberOf)
	}
	got := projectExploreTypedAnchorCandidates(
		context.Background(), fixture.task, fixture.candidates, fixture.reader,
		exploreTypedAnchorTestScope(), len(fixture.candidates), fixture.protected, "",
	)
	if exploreTypedAnchorCandidateByID(got, fixture.member.ID) != nil {
		t.Fatal("cap-overflowed partial adjacency produced a projection")
	}
}

func TestExploreTypedAnchorCanonicalTypeRequiresExactIdentity(t *testing.T) {
	if got := exploreTypedAnchorCanonicalType("&'a mut crate::util::Replacer<M>"); got != "util.replacer" {
		t.Fatalf("canonical Rust type = %q, want util.replacer", got)
	}
	owner := exploreTypedAnchorNode("owner", "Client", "client.rs", "rs", graph.KindType)
	owner.QualName = "other::transport::Client"
	if !exploreTypedAnchorCanonicalTypeMatches("transport.client", owner) {
		t.Fatal("qualified suffix identity did not match")
	}
	if exploreTypedAnchorCanonicalTypeMatches("common.client", owner) {
		t.Fatal("shared base type name crossed a different qualifier")
	}
}

func BenchmarkProjectExploreTypedAnchorCandidates(b *testing.B) {
	fixture := newExploreTypedAnchorFixture(
		"rs", "--replace causes duplicate output for a multiline match", "replacer", "Replacer<M>", "replace", "replace_all", false,
	)
	scope := exploreTypedAnchorTestScope()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		got := projectExploreTypedAnchorCandidates(
			context.Background(), fixture.task, fixture.candidates, fixture.reader,
			scope, len(fixture.candidates), fixture.protected, "",
		)
		if exploreTypedAnchorCandidateByID(got, fixture.member.ID) == nil {
			b.Fatal("projection lost graph-proven member")
		}
	}
}
