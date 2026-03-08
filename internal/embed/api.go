package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
)

// APIEmbedder calls an OpenAI-compatible embedding API.
type APIEmbedder struct {
	modelName  string
	apiName    string // the actual model name sent to API
	endpoint   string
	apiKey     string
	dim        int
	httpClient *http.Client
}

func NewAPIEmbedder(modelName string, cfg *config.Config) (*APIEmbedder, error) {
	apiName := strings.TrimPrefix(modelName, "api:")

	dim, ok := apiModelDims[apiName]
	if !ok {
		dim = 1536 // default
	}

	// Prefer dedicated embed endpoint/key if configured;
	// fall back to LLM endpoint/key for backward compatibility.
	endpoint := cfg.EmbedAPIEndpoint
	if endpoint == "" {
		endpoint = cfg.LLMEndpoint
	}
	apiKey := cfg.EmbedAPIKey
	if apiKey == "" {
		apiKey = cfg.LLMAPIKey
	}

	return &APIEmbedder{
		modelName: modelName,
		apiName:   apiName,
		endpoint:  strings.TrimRight(endpoint, "/"),
		apiKey:    apiKey,
		dim:       dim,
		// 60s timeout: generous enough for large batches, prevents indefinite hang
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}, nil
}

// APIEmbedderInfo returns a human-readable description of the API embedder config.
// Used by `axon status` to show embedding backend details.
func APIEmbedderInfo(cfg *config.Config) string {
	model := cfg.EmbedAPIModel
	if model == "" {
		model = "text-embedding-3-small"
	}
	endpoint := cfg.EmbedAPIEndpoint
	if endpoint == "" {
		endpoint = cfg.LLMEndpoint
	}
	// Mask the key
	key := cfg.EmbedAPIKey
	if key == "" {
		key = cfg.LLMAPIKey
	}
	masked := "(not set)"
	if len(key) >= 8 {
		masked = key[:4] + "…" + key[len(key)-4:]
	} else if key != "" {
		masked = "***"
	}
	return fmt.Sprintf("api:%s @ %s (key: %s)", model, endpoint, masked)
}

func (e *APIEmbedder) ModelName() string { return e.modelName }
func (e *APIEmbedder) Provider() string  { return "openai" }
func (e *APIEmbedder) Dim() int          { return e.dim }

func (e *APIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	reqBody, err := json.Marshal(map[string]any{
		"model": e.apiName,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.endpoint+"/embeddings", bytes.NewReader(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
			Index     int       `json:"index"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	// Sort by index to maintain order; validate that every slot is filled.
	embeddings := make([][]float32, len(texts))
	for _, d := range result.Data {
		if d.Index >= 0 && d.Index < len(embeddings) {
			embeddings[d.Index] = d.Embedding
		}
	}
	// Ensure no slot was left nil (API returned fewer items than requested).
	for i, vec := range embeddings {
		if vec == nil {
			return nil, fmt.Errorf("api response missing embedding at index %d", i)
		}
	}
	return embeddings, nil
}

var apiModelDims = map[string]int{
	"text-embedding-3-small": 1536,
	"text-embedding-3-large": 3072,
	"text-embedding-ada-002": 1536,
}
