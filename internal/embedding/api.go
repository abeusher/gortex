package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIProvider calls an external embedding API (Ollama or OpenAI-compatible).
type APIProvider struct {
	url    string
	model  string
	client *http.Client
	dims   int
	format apiFormat
}

type apiFormat int

const (
	formatOllama apiFormat = iota
	formatOpenAI
)

// NewAPIProvider creates a provider that calls an external embedding API.
// Auto-detects Ollama vs OpenAI format from the URL.
func NewAPIProvider(url, model string) *APIProvider {
	format := formatOpenAI
	if strings.Contains(url, "11434") || strings.Contains(url, "/api/") {
		format = formatOllama
	}

	if model == "" {
		if format == formatOllama {
			model = "nomic-embed-text"
		} else {
			model = "text-embedding-3-small"
		}
	}

	return &APIProvider{
		url:    strings.TrimRight(url, "/"),
		model:  model,
		client: &http.Client{Timeout: 30 * time.Second},
		format: format,
	}
}

func (p *APIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	vecs, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("embedding API returned no results")
	}
	return vecs[0], nil
}

func (p *APIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if p.format == formatOllama {
		return p.embedOllama(ctx, texts)
	}
	return p.embedOpenAI(ctx, texts)
}

func (p *APIProvider) Dimensions() int { return p.dims }
func (p *APIProvider) Close() error    { return nil }

// --- Ollama API ---

type ollamaRequest struct {
	Model string `json:"model"`
	Input any    `json:"input"` // string or []string
}

type ollamaResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (p *APIProvider) embedOllama(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := ollamaRequest{
		Model: p.model,
		Input: texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.url + "/api/embed"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(result.Embeddings) > 0 && p.dims == 0 {
		p.dims = len(result.Embeddings[0])
	}

	return result.Embeddings, nil
}

// --- OpenAI API ---

type openAIRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIResponse struct {
	Data []openAIEmbedding `json:"data"`
}

type openAIEmbedding struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

func (p *APIProvider) embedOpenAI(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody := openAIRequest{
		Model: p.model,
		Input: texts,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := p.url + "/v1/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API call: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	vecs := make([][]float32, len(result.Data))
	for _, d := range result.Data {
		vecs[d.Index] = d.Embedding
	}

	if len(vecs) > 0 && p.dims == 0 && len(vecs[0]) > 0 {
		p.dims = len(vecs[0])
	}

	return vecs, nil
}
