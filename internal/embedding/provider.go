// Package embedding provides pluggable embedding providers for semantic search.
// The default build includes StaticProvider (word vector averaging) and APIProvider
// (Ollama/OpenAI). Build tags enable transformer backends: embeddings_onnx,
// embeddings_gomlx, embeddings_hugot.
package embedding

import "context"

// Provider generates embedding vectors from text.
type Provider interface {
	// Embed returns the embedding vector for the given text.
	Embed(ctx context.Context, text string) ([]float32, error)

	// EmbedBatch returns embeddings for multiple texts.
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)

	// Dimensions returns the embedding vector size.
	Dimensions() int

	// Close releases resources.
	Close() error
}

// NewLocalProvider returns the best available local embedding provider.
// Build tags determine which transformer providers are compiled in.
// Falls back to StaticProvider if no transformer backend is available.
func NewLocalProvider() (Provider, error) {
	// Try transformer backends (compiled in via build tags).
	if p, err := newONNXProvider(); err == nil {
		return p, nil
	}
	if p, err := newGoMLXProvider(); err == nil {
		return p, nil
	}
	if p, err := newHugotProvider(); err == nil {
		return p, nil
	}
	// Fallback: static word vectors (always available).
	return NewStaticProvider()
}
