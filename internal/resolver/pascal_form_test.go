package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func pascalFileNode(g graph.Store, path, workspace string) {
	g.AddNode(&graph.Node{
		ID: path, Kind: graph.KindFile, Name: path, FilePath: path,
		Language: "pascal", WorkspaceID: workspace,
	})
}

func pascalFormEdge(g graph.Store, from, to string) *graph.Edge {
	for _, e := range g.GetOutEdges(from) {
		if e.To == to && e.Meta != nil {
			if v, _ := e.Meta["via"].(string); v == pascalFormVia {
				return e
			}
		}
	}
	return nil
}

// TestPascalFormSynth is the C1 named test: a unit and its same-dir
// same-basename form pair, the pairing rides a provenance tier, and a basename
// shared across workspaces (repos) does not cross-pair.
func TestPascalFormSynth(t *testing.T) {
	g := graph.New()
	pascalFileNode(g, "ui/Main.pas", "app")
	pascalFileNode(g, "ui/Main.dfm", "app")
	pascalFileNode(g, "ui/Other.pas", "app") // no form → no pair
	// Same basename, different workspace — must NOT pair.
	pascalFileNode(g, "lib/Main.pas", "vendor")
	pascalFileNode(g, "other/Main.dfm", "vendor")

	n := ResolvePascalForms(g)
	assert.Equal(t, 1, n, "exactly one same-dir same-workspace pair")

	e := pascalFormEdge(g, "ui/Main.pas", "ui/Main.dfm")
	require.NotNil(t, e, "unit should reference its form")
	assert.Equal(t, graph.EdgeReferences, e.Kind)
	assert.Equal(t, graph.OriginASTInferred, e.Origin, "pairing must ride a tier")
	assert.Equal(t, SynthPascalForm, e.Meta[MetaSynthesizedBy])

	assert.Nil(t, pascalFormEdge(g, "ui/Other.pas", "ui/Other.dfm"))
}
