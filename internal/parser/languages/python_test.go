package languages

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestPyExtractor_Function(t *testing.T) {
	src := []byte(`def greet(name):
    return f"Hello {name}"
`)
	e := NewPythonExtractor()
	result, err := e.Extract("app.py", src)
	require.NoError(t, err)

	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	require.Len(t, funcs, 1)
	assert.Equal(t, "greet", funcs[0].Name)
}

func TestPyExtractor_Class(t *testing.T) {
	src := []byte(`class UserService:
    def __init__(self):
        self.users = []

    def get_user(self, user_id):
        return None
`)
	e := NewPythonExtractor()
	result, err := e.Extract("service.py", src)
	require.NoError(t, err)

	types := nodesOfKind(result.Nodes, graph.KindType)
	require.Len(t, types, 1)
	assert.Equal(t, "UserService", types[0].Name)

	// Class methods are extracted as KindMethod, not KindFunction.
	methods := nodesOfKind(result.Nodes, graph.KindMethod)
	assert.Len(t, methods, 2) // __init__, get_user

	// No top-level functions.
	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	assert.Len(t, funcs, 0)

	// EdgeMemberOf edges link methods to the class.
	memberEdges := edgesOfKind(result.Edges, graph.EdgeMemberOf)
	assert.Len(t, memberEdges, 2)
	for _, e := range memberEdges {
		assert.Equal(t, "service.py::UserService", e.To)
	}
}

func TestPyExtractor_Imports(t *testing.T) {
	src := []byte(`import os
from pathlib import Path
`)
	e := NewPythonExtractor()
	result, err := e.Extract("app.py", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	require.Len(t, imports, 2)
}

func TestPyExtractor_TypeEnv_TypeHint(t *testing.T) {
	src := []byte(`
class UserService:
    def save(self):
        pass

def main():
    svc: UserService = get_service()
    svc.save()
`)
	e := NewPythonExtractor()
	result, err := e.Extract("app.py", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var saveCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "save") {
			saveCall = c
			break
		}
	}
	require.NotNil(t, saveCall, "expected a call edge to save")
	require.NotNil(t, saveCall.Meta, "expected Meta on save call edge")
	assert.Equal(t, "UserService", saveCall.Meta["receiver_type"])
}

func TestPyExtractor_TypeEnv_ClassConstructor(t *testing.T) {
	src := []byte(`
class Client:
    def connect(self):
        pass

def main():
    client = Client()
    client.connect()
`)
	e := NewPythonExtractor()
	result, err := e.Extract("app.py", src)
	require.NoError(t, err)

	calls := edgesOfKind(result.Edges, graph.EdgeCalls)
	var connectCall *graph.Edge
	for _, c := range calls {
		if strings.HasSuffix(c.To, "connect") {
			connectCall = c
			break
		}
	}
	require.NotNil(t, connectCall)
	require.NotNil(t, connectCall.Meta)
	assert.Equal(t, "Client", connectCall.Meta["receiver_type"])
}

func TestPyExtractor_TypeEnv_Unknown(t *testing.T) {
	src := []byte(`
def get_service():
    return None

def main():
    svc = get_service()
    svc.process()
`)
	e := NewPythonExtractor()
	result, err := e.Extract("app.py", src)
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
