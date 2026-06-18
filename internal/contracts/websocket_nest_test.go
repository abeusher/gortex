package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestWebSocketSubscribeMessage(t *testing.T) {
	src := []byte(`import { SubscribeMessage, WebSocketGateway } from '@nestjs/websockets';

@WebSocketGateway()
export class EventsGateway {
  @SubscribeMessage('events')
  handleEvent(@MessageBody() data: string): string {
    return data;
  }
}
`)
	nodes := []*graph.Node{nestMethodNode("g.ts", "handleEvent", 5)}
	cs := (&WebSocketExtractor{}).Extract("g.ts", src, nodes, nil)

	c := nestFindByID(cs, "ws::events")
	if c == nil {
		t.Fatalf("expected ws::events from @SubscribeMessage, got %+v", cs)
	}
	if c.Type != ContractWS || c.Role != RoleProvider {
		t.Errorf("type/role = %v/%v", c.Type, c.Role)
	}
	if c.Meta["framework"] != "nestjs" {
		t.Errorf("framework = %v, want nestjs", c.Meta["framework"])
	}
	if c.SymbolID != "g.ts::handleEvent" {
		t.Errorf("handler = %q, want the decorated method", c.SymbolID)
	}
}
