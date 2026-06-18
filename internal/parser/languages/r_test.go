package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zzet/gortex/internal/graph"
)

func TestRExtractor_Functions(t *testing.T) {
	src := []byte(`add <- function(a, b) {
  a + b
}

multiply = function(x, y) {
  x * y
}
`)
	e := NewRExtractor()
	result, err := e.Extract("math.R", src)
	require.NoError(t, err)

	funcs := nodesOfKind(result.Nodes, graph.KindFunction)
	names := make([]string, len(funcs))
	for i, f := range funcs {
		names[i] = f.Name
	}
	assert.Contains(t, names, "add")
	assert.Contains(t, names, "multiply")
}

func TestRExtractor_Imports(t *testing.T) {
	src := []byte(`library(ggplot2)
require(dplyr)
source("utils.R")
`)
	e := NewRExtractor()
	result, err := e.Extract("main.R", src)
	require.NoError(t, err)

	imports := edgesOfKind(result.Edges, graph.EdgeImports)
	assert.Len(t, imports, 3)
}

func TestRExtractor_Variables(t *testing.T) {
	src := []byte(`max_size <- 100
threshold = 0.5
name <- "test"
`)
	e := NewRExtractor()
	result, err := e.Extract("config.R", src)
	require.NoError(t, err)

	vars := nodesOfKind(result.Nodes, graph.KindVariable)
	varNames := make([]string, len(vars))
	for i, v := range vars {
		varNames[i] = v.Name
	}
	assert.Contains(t, varNames, "max_size")
	assert.Contains(t, varNames, "threshold")
	assert.Contains(t, varNames, "name")
}

// TestRClassSystemsAndDispatch is the C6 test: S4 (setClass+contains,
// setMethod), R6/Reference classes, and S3 generic.class methods all extract,
// and a call to a generic reaches its methods through dispatch edges.
func TestRClassSystemsAndDispatch(t *testing.T) {
	src := []byte("setClass(\"Circle\", contains = \"Shape\")\n" +
		"setGeneric(\"area\", function(obj) standardGeneric(\"area\"))\n" +
		"setMethod(\"area\", \"Circle\", function(obj) pi)\n" +
		"Counter <- R6Class(\"Counter\", public = list())\n" +
		"Acc <- setRefClass(\"Account\")\n" +
		"print.Circle <- function(x) cat(\"c\")\n")
	res, err := NewRExtractor().Extract("m.R", src)
	require.NoError(t, err)

	kind := map[string]graph.NodeKind{}
	system := map[string]string{}
	for _, n := range res.Nodes {
		kind[n.ID] = n.Kind
		if n.Meta != nil {
			if s, _ := n.Meta["class_system"].(string); s != "" {
				system[n.ID] = s
			}
		}
	}
	assert.Equal(t, graph.KindType, kind["m.R::Circle"], "S4 class")
	assert.Equal(t, "S4", system["m.R::Circle"])
	assert.Equal(t, graph.KindMethod, kind["m.R::area.Circle"], "S4 method")
	assert.Equal(t, graph.KindType, kind["m.R::Counter"], "R6 class")
	assert.Equal(t, "R6", system["m.R::Counter"])
	assert.Equal(t, graph.KindType, kind["m.R::Account"], "Reference class")

	var s4Dispatch, s3Dispatch, inherit bool
	for _, e := range res.Edges {
		if e.Kind == graph.EdgeCalls && e.From == "m.R::area" && e.To == "m.R::area.Circle" {
			s4Dispatch = true
		}
		if e.Kind == graph.EdgeCalls && e.From == "m.R::print" && e.To == "m.R::print.Circle" {
			s3Dispatch = true
		}
		if e.Kind == graph.EdgeExtends && e.From == "m.R::Circle" && e.To == "unresolved::Shape" {
			inherit = true
		}
	}
	assert.True(t, s4Dispatch, "the S4 generic area should dispatch to its Circle method")
	assert.True(t, s3Dispatch, "the S3 generic print should dispatch to print.Circle")
	assert.True(t, inherit, "setClass contains= should produce an inheritance edge")
}
