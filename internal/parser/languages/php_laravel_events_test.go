package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func laravelTaggedHandle(nodes []*graph.Node, evType string) *graph.Node {
	for _, n := range nodes {
		if n.Name == "handle" && n.Meta != nil {
			if t, _ := n.Meta["laravel_listener_type"].(string); t == evType {
				return n
			}
		}
	}
	return nil
}

func laravelListenMapNode(nodes []*graph.Node) *graph.Node {
	for _, n := range nodes {
		if n.Meta != nil && n.Meta["laravel_listen_map"] != nil {
			return n
		}
	}
	return nil
}

func laravelDispatchPlaceholder(edges []*graph.Edge, evType string) *graph.Edge {
	for _, e := range edges {
		if e.Meta == nil {
			continue
		}
		if v, _ := e.Meta["via"].(string); v != "laravel-event" {
			continue
		}
		if t, _ := e.Meta["laravel_event_type"].(string); t == evType {
			return e
		}
	}
	return nil
}

func TestLaravel_TypedHandleUnderListenersNamespace(t *testing.T) {
	src := `<?php
namespace App\Listeners;
class SendShipmentNotification {
  public function handle(OrderShipped $event) {}
}
`
	res, err := NewPHPExtractor().Extract("app/Listeners/SendShipmentNotification.php", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if laravelTaggedHandle(res.Nodes, "OrderShipped") == nil {
		t.Errorf("typed handle under Listeners namespace not tagged")
	}
}

func TestLaravel_HandleOutsideListenersNotTagged(t *testing.T) {
	src := `<?php
namespace App\Services;
class Thing {
  public function handle(OrderShipped $event) {}
}
`
	res, err := NewPHPExtractor().Extract("app/Services/Thing.php", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if laravelTaggedHandle(res.Nodes, "OrderShipped") != nil {
		t.Errorf("a handle outside a Listeners namespace must not be tagged")
	}
}

func TestLaravel_ListenMapEncoded(t *testing.T) {
	src := `<?php
namespace App\Providers;
class EventServiceProvider {
  protected $listen = [
    OrderShipped::class => [SendEmail::class, SendSms::class],
  ];
}
`
	res, err := NewPHPExtractor().Extract("app/Providers/EventServiceProvider.php", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	n := laravelListenMapNode(res.Nodes)
	if n == nil {
		t.Fatalf("EventServiceProvider $listen map not stamped")
	}
	m, _ := n.Meta["laravel_listen_map"].(string)
	if m != "OrderShipped=>SendEmail,SendSms" {
		t.Errorf("listen map = %q (want OrderShipped=>SendEmail,SendSms)", m)
	}
}

func TestLaravel_DispatchSites(t *testing.T) {
	src := `<?php
namespace App\Http;
class OrderController {
  public function ship($order) {
    event(new OrderShipped($order));
  }
  public function cancel($order) {
    OrderCancelled::dispatch($order);
  }
}
`
	res, err := NewPHPExtractor().Extract("app/Http/OrderController.php", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if e := laravelDispatchPlaceholder(res.Edges, "OrderShipped"); e == nil {
		t.Errorf("no placeholder for event(new OrderShipped())")
	} else if e.From != "app/Http/OrderController.php::OrderController.ship" {
		t.Errorf("event() placeholder From = %q", e.From)
	}
	if laravelDispatchPlaceholder(res.Edges, "OrderCancelled") == nil {
		t.Errorf("no placeholder for OrderCancelled::dispatch()")
	}
}
