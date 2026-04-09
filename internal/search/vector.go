package search

import (
	"sync"

	"github.com/coder/hnsw"
)

// VectorBackend stores and searches embedding vectors using HNSW index.
type VectorBackend struct {
	graph *hnsw.Graph[string]
	count int
	dims  int
	mu    sync.RWMutex
}

// NewVector creates a vector search backend for the given embedding dimensions.
func NewVector(dims int) *VectorBackend {
	g := hnsw.NewGraph[string]()
	g.Distance = hnsw.CosineDistance
	return &VectorBackend{
		graph: g,
		dims:  dims,
	}
}

// Add indexes a symbol with its embedding vector.
func (v *VectorBackend) Add(id string, vector []float32) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.graph.Add(hnsw.Node[string]{
		Key:   id,
		Value: hnsw.Vector(vector),
	})
	v.count++
}

// Search returns the k nearest neighbors to the query vector.
func (v *VectorBackend) Search(query []float32, k int) []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.count == 0 {
		return nil
	}
	results := v.graph.Search(hnsw.Vector(query), k)
	ids := make([]string, len(results))
	for i, r := range results {
		ids[i] = r.Key
	}
	return ids
}

// Count returns the number of indexed vectors.
func (v *VectorBackend) Count() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.count
}

// Dims returns the embedding dimensionality.
func (v *VectorBackend) Dims() int { return v.dims }
