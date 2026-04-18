package languages

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestMakefileExtractor_Basics(t *testing.T) {
	src := []byte(`include config.mk

CC := gcc
CFLAGS = -O2

all: build test

build:
	$(CC) $(CFLAGS) -o app main.c

test: build
	./app --test

define greet
echo hello
endef
`)
	e := NewMakefileExtractor()
	require.Equal(t, "makefile", e.Language())

	res, err := e.Extract("Makefile", src)
	require.NoError(t, err)

	var gotAll, gotBuild, gotCC, gotGreet bool
	for _, n := range res.Nodes {
		switch n.Name {
		case "all":
			gotAll = true
		case "build":
			gotBuild = true
		case "CC":
			gotCC = true
		case "greet":
			gotGreet = true
		}
	}
	var gotImport bool
	for _, ed := range res.Edges {
		if ed.Kind == graph.EdgeImports && ed.To == "unresolved::import::config.mk" {
			gotImport = true
		}
	}
	assert.True(t, gotAll)
	assert.True(t, gotBuild)
	assert.True(t, gotCC)
	assert.True(t, gotGreet)
	assert.True(t, gotImport)
}

func TestMakefileExtractor_EmptyInput(t *testing.T) {
	res, err := NewMakefileExtractor().Extract("Makefile", []byte(""))
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1)
	assert.Equal(t, graph.KindFile, res.Nodes[0].Kind)
}
