package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func nestFindByID(cs []Contract, id string) *Contract {
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	return nil
}

func nestMethodNode(file, name string, line int) *graph.Node {
	return &graph.Node{ID: file + "::" + name, Name: name, Kind: graph.KindMethod, FilePath: file, StartLine: line, EndLine: line + 2}
}

func TestMessagePatternHandlers(t *testing.T) {
	src := []byte(`import { MessagePattern, EventPattern } from '@nestjs/microservices';

@Controller()
export class MathController {
  @MessagePattern({ cmd: 'sum' })
  accumulate(data: number[]): number {
    return 0;
  }

  @EventPattern('user_created')
  handleUserCreated(data: any) {}
}
`)
	nodes := []*graph.Node{
		nestMethodNode("c.ts", "accumulate", 6),
		nestMethodNode("c.ts", "handleUserCreated", 10),
	}
	cs := (&NestMicroserviceExtractor{}).Extract("c.ts", src, nodes, nil)

	sum := nestFindByID(cs, "topic::sum")
	if sum == nil {
		t.Fatalf("expected topic::sum from @MessagePattern, got %+v", cs)
	}
	if sum.Type != ContractTopic || sum.Role != RoleProvider {
		t.Errorf("sum type/role = %v/%v", sum.Type, sum.Role)
	}
	if sum.Meta["message_kind"] != "MessagePattern" {
		t.Errorf("sum message_kind = %v", sum.Meta["message_kind"])
	}
	if sum.SymbolID != "c.ts::accumulate" {
		t.Errorf("sum handler = %q", sum.SymbolID)
	}

	evt := nestFindByID(cs, "topic::user_created")
	if evt == nil {
		t.Fatalf("expected topic::user_created from @EventPattern, got %+v", cs)
	}
	if evt.Meta["message_kind"] != "EventPattern" {
		t.Errorf("event message_kind = %v", evt.Meta["message_kind"])
	}
	if evt.SymbolID != "c.ts::handleUserCreated" {
		t.Errorf("event handler = %q", evt.SymbolID)
	}
}

func TestNestMicroservice_SupportedLanguages(t *testing.T) {
	langs := (&NestMicroserviceExtractor{}).SupportedLanguages()
	found := false
	for _, l := range langs {
		if l == "typescript" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected typescript in SupportedLanguages, got %v", langs)
	}
	// A file without the decorators yields nothing (prefilter).
	if cs := (&NestMicroserviceExtractor{}).Extract("x.ts", []byte("const a = 1\n"), nil, nil); len(cs) != 0 {
		t.Errorf("non-microservice file should produce no contracts, got %+v", cs)
	}
}
