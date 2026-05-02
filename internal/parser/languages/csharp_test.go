package languages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestCSharpExtractor_ClassWithMethods(t *testing.T) {
	src := []byte(`public class UserService {
    public User FindById(string id) {
        return null;
    }

    public void Save(User user) {
        _db.Execute(user);
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("UserService.cs", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "UserService", types[0].Name)

	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	require.Len(t, methods, 2)
	assert.Equal(t, "FindById", methods[0].Name)
	assert.Equal(t, "Save", methods[1].Name)

	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	require.Len(t, memberEdges, 2)
	for _, e := range memberEdges {
		assert.Equal(t, "UserService.cs::UserService", e.To)
	}
}

func TestCSharpExtractor_Interface(t *testing.T) {
	src := []byte(`public interface IUserService {
    User FindById(string id);
    void Save(User user);
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("IUserService.cs", src)
	require.NoError(t, err)

	ifaces := nodesOfKind(result.Nodes, graph.KindInterface)
	require.Len(t, ifaces, 1)
	assert.Equal(t, "IUserService", ifaces[0].Name)
	require.NotNil(t, ifaces[0].Meta)
	methods, ok := ifaces[0].Meta["methods"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"FindById", "Save"}, methods)
}

func TestCSharpExtractor_UsingImports(t *testing.T) {
	src := []byte(`using System;
using System.Collections.Generic;

public class App {}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("App.cs", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	require.Len(t, imports, 2)
}

func TestCSharpExtractor_Namespace(t *testing.T) {
	src := []byte(`namespace MyApp.Services
{
    public class Foo {}
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("Foo.cs", src)
	require.NoError(t, err)

	pkgs := nodesOfKind(result.Nodes, graph.KindPackage)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "MyApp.Services", pkgs[0].Name)
}

func TestCSharpExtractor_StructAndEnum(t *testing.T) {
	src := []byte(`public enum Status {
    Active,
    Inactive
}

public struct Point {
    public int X;
    public int Y;
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("Types.cs", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 2)
	names := []string{types[0].Name, types[1].Name}
	assert.Contains(t, names, "Status")
	assert.Contains(t, names, "Point")

	// Struct fields should be extracted.
	fields := nodesOfKind(result.Nodes, graph.KindField)
	assert.Len(t, fields, 2)

	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.Len(t, memberEdges, 2)
	for _, e := range memberEdges {
		assert.Equal(t, "Types.cs::Point", e.To)
	}
}

func TestCSharpExtractor_Constructor(t *testing.T) {
	src := []byte(`public class UserService {
    private readonly Database _db;

    public UserService(Database db) {
        _db = db;
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("UserService.cs", src)
	require.NoError(t, err)

	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	require.Len(t, methods, 1)
	assert.Equal(t, "UserService.<init>", methods[0].Name)

	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	// Constructor + field = 2 MemberOf edges
	require.GreaterOrEqual(t, len(memberEdges), 1)
	found := false
	for _, e := range memberEdges {
		if e.From == "UserService.cs::UserService.<init>" {
			assert.Equal(t, "UserService.cs::UserService", e.To)
			found = true
		}
	}
	assert.True(t, found, "constructor should have MemberOf edge to class")
}

func TestCSharpExtractor_FullSample(t *testing.T) {
	src := []byte(`using System;
using System.Collections.Generic;

namespace MyApp.Services
{
    public interface IUserService
    {
        User FindById(string id);
        void Save(User user);
    }

    public class UserService : IUserService
    {
        private readonly Database _db;

        public UserService(Database db)
        {
            _db = db;
        }

        public User FindById(string id)
        {
            return _db.Query(id);
        }

        public void Save(User user)
        {
            _db.Execute(user);
        }
    }

    public enum Status
    {
        Active,
        Inactive
    }

    public struct Point
    {
        public int X;
        public int Y;
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("Services.cs", src)
	require.NoError(t, err)

	// 1 namespace
	pkgs := nodesOfKind(result.Nodes, graph.KindPackage)
	assert.Len(t, pkgs, 1)

	// 1 interface
	ifaces := nodesOfKind(result.Nodes, graph.KindInterface)
	assert.Len(t, ifaces, 1)

	// 3 types: UserService, Status, Point
	types := nodesOfKind(result.Nodes, graph.KindType)
	assert.Len(t, types, 3)

	// 3 methods: constructor + FindById + Save
	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	assert.Len(t, methods, 3)

	// 2 imports
	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	assert.Len(t, imports, 2)

	// Call edges (Query, Execute)
	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	assert.GreaterOrEqual(t, len(calls), 2)
}

func TestCSharpExtractor_TypeEnv_ExplicitType(t *testing.T) {
	src := []byte(`public class UserService {
    public void Save() {}
}

public class App {
    public void Main() {
        UserService svc = new UserService();
        svc.Save();
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("app.cs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var saveCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "Save") {
			saveCall = c
			break
		}
	}
	require.NotNil(t, saveCall, "expected a call edge to Save")
	require.NotNil(t, saveCall.Meta, "expected Meta on Save call edge")
	assert.Equal(t, "UserService", saveCall.Meta["receiver_type"])
}

func TestCSharpExtractor_TypeEnv_NewExpression(t *testing.T) {
	src := []byte(`public class Client {
    public void Connect() {}
}

public class App {
    public void Main() {
        var client = new Client();
        client.Connect();
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("app.cs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var connectCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "Connect") {
			connectCall = c
			break
		}
	}
	require.NotNil(t, connectCall)
	require.NotNil(t, connectCall.Meta)
	assert.Equal(t, "Client", connectCall.Meta["receiver_type"])
}

func TestCSharpExtractor_TypeEnv_Unknown(t *testing.T) {
	src := []byte(`public class App {
    public object GetService() { return null; }

    public void Main() {
        var svc = GetService();
        svc.Process();
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("app.cs", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var processCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "Process") {
			processCall = c
			break
		}
	}
	require.NotNil(t, processCall)
	assert.Nil(t, processCall.Meta, "unknown type should not produce Meta")
}

func TestCSharpExtractor_TypeEnv_Chain(t *testing.T) {
	src := []byte(`public class Order {
    public int Id;
}

public class UserService {
    public Order GetOrder() { return new Order(); }
}

public class App {
    public void Main() {
        UserService svc = new UserService();
        svc.GetOrder().ToString();
    }
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("app.cs", src)
	require.NoError(t, err)

	// Verify return_type is set on GetOrder method.
	var getOrderNode *graph.Node
	for _, n := range result.Nodes {
		if n.Name == "GetOrder" {
			getOrderNode = n
			break
		}
	}
	require.NotNil(t, getOrderNode, "expected a node for GetOrder")
	assert.Equal(t, "Order", getOrderNode.Meta["return_type"])

	// Verify chain resolution: svc.GetOrder() should resolve to Order.
	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var toStringCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "ToString") {
			toStringCall = c
			break
		}
	}
	require.NotNil(t, toStringCall, "expected a call edge to ToString")
	require.NotNil(t, toStringCall.Meta, "expected Meta on ToString call edge")
	assert.Equal(t, "Order", toStringCall.Meta["receiver_type"])
}

func TestCSharpExtractor_DocAndVisibility(t *testing.T) {
	src := []byte(`namespace X
{
    /// <summary>
    /// Greeter wraps the greeting.
    /// </summary>
    public class Greeter
    {
        /// <summary>Says hi.</summary>
        public void Hello() {}

        private void Secret() {}
    }

    class Internal {}
}
`)
	e := NewCSharpExtractor()
	result, err := e.Extract("Greeter.cs", src)
	require.NoError(t, err)

	byID := map[string]*graph.Node{}
	for _, n := range result.Nodes {
		byID[n.ID] = n
	}

	greeter := byID["Greeter.cs::Greeter"]
	require.NotNil(t, greeter)
	if greeter.Meta["visibility"] != "public" {
		t.Fatalf("Greeter.vis = %q", greeter.Meta["visibility"])
	}
	if greeter.Meta["doc"] != "Greeter wraps the greeting." {
		t.Fatalf("Greeter.doc = %q", greeter.Meta["doc"])
	}

	hello := byID["Greeter.cs::Greeter.Hello"]
	require.NotNil(t, hello)
	if hello.Meta["visibility"] != "public" {
		t.Fatalf("Hello.vis = %q", hello.Meta["visibility"])
	}
	if hello.Meta["doc"] != "Says hi." {
		t.Fatalf("Hello.doc = %q", hello.Meta["doc"])
	}

	secret := byID["Greeter.cs::Greeter.Secret"]
	require.NotNil(t, secret)
	if secret.Meta["visibility"] != "private" {
		t.Fatalf("Secret.vis = %q", secret.Meta["visibility"])
	}

	internalT := byID["Greeter.cs::Internal"]
	require.NotNil(t, internalT)
	if internalT.Meta["visibility"] != "internal" {
		t.Fatalf("Internal.vis = %q", internalT.Meta["visibility"])
	}
}
