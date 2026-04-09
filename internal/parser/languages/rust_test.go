package languages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestRsExtractor_Function(t *testing.T) {
	src := []byte(`fn greet(name: &str) -> String {
    format!("Hello {}", name)
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("main.rs", src)
	require.NoError(t, err)

	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	require.Len(t, funcs, 1)
	assert.Equal(t, "greet", funcs[0].Name)
}

func TestRsExtractor_Struct(t *testing.T) {
	src := []byte(`struct Config {
    port: u16,
    host: String,
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("config.rs", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "Config", types[0].Name)
}

func TestRsExtractor_Trait(t *testing.T) {
	src := []byte(`trait Repository {
    fn find_by_id(&self, id: &str) -> Option<User>;
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("store.rs", src)
	require.NoError(t, err)

	ifaces := nodesOfKind(result.Nodes, graph.KindInterface)
	require.Len(t, ifaces, 1)
	assert.Equal(t, "Repository", ifaces[0].Name)
}

func TestRsExtractor_ImplMethods(t *testing.T) {
	src := []byte(`struct Server {
    port: u16,
}

impl Server {
    fn new(port: u16) -> Self {
        Server { port }
    }

    fn start(&self) {
        println!("Starting on {}", self.port);
    }
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("server.rs", src)
	require.NoError(t, err)

	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	assert.Len(t, methods, 2) // new, start

	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.Len(t, memberEdges, 2)
	for _, e := range memberEdges {
		assert.Equal(t, "server.rs::Server", e.To)
	}

	// Methods should NOT appear as top-level functions.
	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	assert.Len(t, funcs, 0)
}

func TestRsExtractor_ImplMethodMeta(t *testing.T) {
	src := []byte(`struct Foo {}

impl Foo {
    fn bar(&self) {}
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("foo.rs", src)
	require.NoError(t, err)

	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	require.Len(t, methods, 1)
	assert.Equal(t, "bar", methods[0].Name)
	assert.Equal(t, "foo.rs::Foo.bar", methods[0].ID)
	assert.Equal(t, "Foo", methods[0].Meta["receiver"])
}

func TestRsExtractor_TraitMethods(t *testing.T) {
	src := []byte(`trait Repository {
    fn find_by_id(&self, id: &str) -> Option<User>;
    fn save(&mut self, user: User);
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("store.rs", src)
	require.NoError(t, err)

	ifaces := nodesOfKind(result.Nodes, graph.KindInterface)
	require.Len(t, ifaces, 1)
	methods, ok := ifaces[0].Meta["methods"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"find_by_id", "save"}, methods)
}

func TestRsExtractor_Use(t *testing.T) {
	src := []byte(`use std::collections::HashMap;
use tokio::net::TcpListener;
`)
	e := NewRustExtractor()
	result, err := e.Extract("main.rs", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	require.Len(t, imports, 2)
}

func TestRsExtractor_TypeEnv_ExplicitType(t *testing.T) {
	src := []byte(`struct Config {
    port: u16,
}

impl Config {
    fn start(&self) {}
}

fn main() {
    let cfg: Config = Config { port: 8080 };
    cfg.start();
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("main.rs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var startCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "start") {
			startCall = c
			break
		}
	}
	require.NotNil(t, startCall, "expected a call edge to start")
	require.NotNil(t, startCall.Meta, "expected Meta on start call edge")
	assert.Equal(t, "Config", startCall.Meta["receiver_type"])
}

func TestRsExtractor_TypeEnv_NewCall(t *testing.T) {
	src := []byte(`struct Server {
    port: u16,
}

impl Server {
    fn new(port: u16) -> Self {
        Server { port }
    }

    fn listen(&self) {}
}

fn main() {
    let srv = Server::new(8080);
    srv.listen();
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("main.rs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var listenCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "listen") {
			listenCall = c
			break
		}
	}
	require.NotNil(t, listenCall)
	require.NotNil(t, listenCall.Meta)
	assert.Equal(t, "Server", listenCall.Meta["receiver_type"])
}

func TestRsExtractor_TypeEnv_Unknown(t *testing.T) {
	src := []byte(`fn get_service() -> Box<dyn std::any::Any> {
    todo!()
}

fn main() {
    let svc = get_service();
    svc.process();
}
`)
	e := NewRustExtractor()
	result, err := e.Extract("main.rs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var processCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "process") {
			processCall = c
			break
		}
	}
	require.NotNil(t, processCall)
	assert.Nil(t, processCall.Meta, "unknown type should not produce Meta")
}
