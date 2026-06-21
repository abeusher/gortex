package languages

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/zzet/gortex/internal/graph"
)

func TestDataAssetExtractor_MetadataOnly(t *testing.T) {
	data := []byte("PAR1 binary columnar payload not to be parsed")
	res, err := NewDataAssetExtractor().Extract("dataset.parquet", data)
	require.NoError(t, err)
	require.Len(t, res.Nodes, 1, "a data asset is exactly one metadata node")
	require.Empty(t, res.Edges)
	n := res.Nodes[0]
	require.Equal(t, graph.KindFile, n.Kind)
	require.Equal(t, "data", n.Meta["asset_kind"])
	require.Equal(t, "data", n.Meta["data_class"])
	require.Equal(t, len(data), n.Meta["size_bytes"])
	require.NotEmpty(t, n.Meta["sha256"])
}

func TestDataAssetExtractor_StreamHashesSmall(t *testing.T) {
	data := []byte("npy array payload")
	var nodes []*graph.Node
	err := NewDataAssetExtractor().ExtractStream("a.npy", bytes.NewReader(data), int64(len(data)),
		func(n *graph.Node, _ []*graph.Edge) { nodes = append(nodes, n) })
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	require.NotEmpty(t, nodes[0].Meta["sha256"], "small files are stream-hashed")
	require.Equal(t, len(data), nodes[0].Meta["size_bytes"])
}

// TestContentExtractorsTagDataClass pins that content chunks (and their file
// nodes) are tagged data_class=content so the retrieval profile and why-layer
// can scope to them.
func TestContentExtractorsTagDataClass(t *testing.T) {
	data := buildZip(t, map[string]string{
		"ppt/slides/slide1.xml": `<sld xmlns:a="urn:a"><a:t>tag check</a:t></sld>`,
	})
	res, err := NewPptxExtractor().Extract("d.pptx", data)
	require.NoError(t, err)
	file, docs := splitNodes(res.Nodes)
	require.Equal(t, "content", file.Meta["data_class"])
	require.Len(t, docs, 1)
	require.Equal(t, "content", docs[0].Meta["data_class"])

	tres, err := NewTextExtractor().Extract("n.txt", []byte("hello"))
	require.NoError(t, err)
	tf, tsecs := splitNodes(tres.Nodes)
	require.Equal(t, "content", tf.Meta["data_class"])
	require.Len(t, tsecs, 1)
	require.Equal(t, "content", tsecs[0].Meta["data_class"])
}
