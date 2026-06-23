package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func laravelHandle(g *graph.Graph, id, file, class, evType string) {
	meta := map[string]any{"receiver": class}
	if evType != "" {
		meta["laravel_listener_type"] = evType
	}
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindMethod, Name: "handle", FilePath: file, Language: "php", Meta: meta})
	g.AddEdge(&graph.Edge{From: id, To: file + "::" + class, Kind: graph.EdgeMemberOf, FilePath: file})
}

func laravelProvider(g *graph.Graph, id, file, listenMap string) {
	g.AddNode(&graph.Node{ID: id, Kind: graph.KindType, Name: "EventServiceProvider", FilePath: file, Language: "php",
		Meta: map[string]any{"laravel_listen_map": listenMap}})
}

func laravelDispatch(g *graph.Graph, fromID, file, evType string) {
	if g.GetNode(fromID) == nil {
		g.AddNode(&graph.Node{ID: fromID, Kind: graph.KindMethod, Name: lastSeg(fromID), FilePath: file, Language: "php"})
	}
	g.AddEdge(&graph.Edge{From: fromID, To: "unresolved::*.handle", Kind: graph.EdgeCalls, FilePath: file,
		Meta: map[string]any{"via": laravelEventVia, "laravel_event_type": evType}})
}

func synthLaravelEdge(g graph.Store, from, to string) *graph.Edge {
	for e := range g.EdgesByKind(graph.EdgeCalls) {
		if e == nil || e.From != from || e.To != to || e.Meta == nil {
			continue
		}
		if by, _ := e.Meta[MetaSynthesizedBy].(string); by == SynthLaravelEvent {
			return e
		}
	}
	return nil
}

func TestResolveLaravelEventCalls_TypedHandleSource(t *testing.T) {
	g := graph.New()
	laravelHandle(g, "L.php::SendShipmentNotification.handle", "L.php", "SendShipmentNotification", "OrderShipped")
	laravelDispatch(g, "C.php::OrderController.ship", "C.php", "OrderShipped")

	n := ResolveLaravelEventCalls(g)
	require.Equal(t, 1, n)
	e := synthLaravelEdge(g, "C.php::OrderController.ship", "L.php::SendShipmentNotification.handle")
	require.NotNil(t, e)
	assert.Equal(t, ConfidenceTyped, e.Confidence)
	assert.Equal(t, ProvenanceFramework, e.Meta[MetaProvenance])
}

func TestResolveLaravelEventCalls_ListenMapSource(t *testing.T) {
	// A listener with an untyped handle, discovered only via the $listen map.
	g := graph.New()
	laravelHandle(g, "M.php::SendEmail.handle", "M.php", "SendEmail", "")
	laravelProvider(g, "P.php::EventServiceProvider", "P.php", "OrderShipped=>SendEmail")
	laravelDispatch(g, "C.php::Ctrl.ship", "C.php", "OrderShipped")

	require.Equal(t, 1, ResolveLaravelEventCalls(g))
	assert.NotNil(t, synthLaravelEdge(g, "C.php::Ctrl.ship", "M.php::SendEmail.handle"),
		"the $listen map binds an untyped handle")
}

func TestResolveLaravelEventCalls_BothSourcesFanOut(t *testing.T) {
	g := graph.New()
	laravelHandle(g, "L.php::TypedListener.handle", "L.php", "TypedListener", "OrderShipped")
	laravelHandle(g, "M.php::MappedListener.handle", "M.php", "MappedListener", "")
	laravelProvider(g, "P.php::EventServiceProvider", "P.php", "OrderShipped=>MappedListener")
	laravelDispatch(g, "C.php::Ctrl.ship", "C.php", "OrderShipped")

	n := ResolveLaravelEventCalls(g)
	require.Equal(t, 2, n, "both discovery sources fan out")
	assert.NotNil(t, synthLaravelEdge(g, "C.php::Ctrl.ship", "L.php::TypedListener.handle"))
	assert.NotNil(t, synthLaravelEdge(g, "C.php::Ctrl.ship", "M.php::MappedListener.handle"))
}

func TestResolveLaravelEventCalls_UnknownEventStaysPlaceholder(t *testing.T) {
	g := graph.New()
	laravelHandle(g, "L.php::L.handle", "L.php", "L", "KnownEvent")
	laravelDispatch(g, "C.php::Ctrl.go", "C.php", "OtherEvent")

	assert.Equal(t, 0, ResolveLaravelEventCalls(g))
	assert.Nil(t, synthLaravelEdge(g, "C.php::Ctrl.go", "L.php::L.handle"))
}
