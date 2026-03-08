// Package classify provides collection classification for ingested documents.
// Three providers are supported:
//   - "llm"        Remote LLM (default, highest accuracy ~85-90%)
//   - "nli"        Local NLI cross-encoder model (~80% accuracy, no network required)
//   - "bge-cosine" Local BGE embedding cosine similarity (~65% accuracy, zero extra deps)
package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// sharedHTTPClient is reused across all calls to avoid per-request connection
// pool creation and the associated overhead / ephemeral-port exhaustion risk.
var sharedHTTPClient = &http.Client{Timeout: 30 * time.Second}

// ClassifyResult holds the classification result.
type ClassifyResult struct {
	CollectionID  string // set when an existing collection matched
	SuggestNew    bool   // true when the classifier wants a brand-new collection
	SuggestedName string // the new collection name when SuggestNew is true
}

// ClassifyInput is the document information used for classification.
type ClassifyInput struct {
	Title     string
	PlainText string
	Origin    string
}

// Classify dispatches to the provider selected in cfg.ClassifyProvider.
// Returns a ClassifyResult. The caller handles SuggestNew by creating the collection.
func Classify(ctx context.Context, cfg *config.Config, data ClassifyInput, cols []store.Collection) (*ClassifyResult, error) {
	switch cfg.ClassifyProvider {
	case "bge-cosine":
		return classifyBGECosine(ctx, cfg, data, cols)
	case "nli":
		return classifyNLI(ctx, cfg, data, cols)
	default: // "llm" or empty
		return classifyLLM(ctx, cfg, data, cols)
	}
}

// ── Provider: LLM ─────────────────────────────────────────────────────────────

// classifyLLM asks the remote LLM to pick the most appropriate collection.
func classifyLLM(ctx context.Context, cfg *config.Config, data ClassifyInput, cols []store.Collection) (*ClassifyResult, error) {
	if cfg.LLMAPIKey == "" {
		return nil, fmt.Errorf("LLM not configured: set llm.key in config or AXON_LLM_API_KEY")
	}

	// Build collection list for prompt
	var colDesc strings.Builder
	for i, c := range cols {
		desc := c.Description
		if desc == "" {
			desc = "(no description)"
		}
		colDesc.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, c.Name, desc))
	}

	// Truncate content for prompt
	preview := data.PlainText
	if len(preview) > 800 {
		preview = preview[:800] + "…"
	}

	originHint := ""
	if data.Origin != "" {
		originHint = fmt.Sprintf("Document source: %s\n", data.Origin)
	}

	var prompt string
	if len(cols) == 0 {
		// No collections yet — ask LLM to name a new one
		prompt = fmt.Sprintf(`You are a knowledge base librarian. Suggest a collection name for this document.

Document title: %s
%sDocument preview:
%s

Reply with ONLY a short collection name (2-4 words, lowercase, e.g. "research papers", "meeting notes", "dev docs"). Nothing else.`,
			data.Title, originHint, preview)
	} else {
		// Ask LLM to pick existing or suggest new
		prompt = fmt.Sprintf(`You are a knowledge base librarian. Given the following document and a list of existing collections, either:
1. Pick the single most appropriate existing collection (reply with the exact collection name)
2. If none fit well, suggest a new collection name with prefix "NEW: " (e.g. "NEW: research papers")

Document title: %s
%sDocument preview:
%s

Existing collections:
%s
Reply with ONLY the collection name or "NEW: <name>". Nothing else.`,
			data.Title, originHint, preview, colDesc.String())
	}

	name, err := callLLM(ctx, cfg, prompt)
	if err != nil {
		return nil, fmt.Errorf("llm classify: %w", err)
	}
	name = strings.TrimSpace(name)

	return parseLLMResponse(name, cols)
}

// parseLLMResponse maps the raw LLM text response to a ClassifyResult.
func parseLLMResponse(name string, cols []store.Collection) (*ClassifyResult, error) {
	// Check for "NEW: <name>" suggestion (case-insensitive prefix)
	if strings.HasPrefix(strings.ToUpper(name), "NEW:") {
		suggestedName := strings.TrimSpace(name[4:])
		if suggestedName == "" {
			suggestedName = "misc"
		}
		return &ClassifyResult{
			SuggestNew:    true,
			SuggestedName: strings.ToLower(suggestedName),
		}, nil
	}

	// When no collections exist, the entire response is the suggested name
	if len(cols) == 0 {
		return &ClassifyResult{
			SuggestNew:    true,
			SuggestedName: strings.ToLower(name),
		}, nil
	}

	// Match name to existing collection (exact, case-insensitive)
	for _, c := range cols {
		if strings.EqualFold(c.Name, name) {
			return &ClassifyResult{CollectionID: c.ID}, nil
		}
	}

	// Fuzzy: LLM may have returned a partial name
	nameLower := strings.ToLower(name)
	for _, c := range cols {
		cLower := strings.ToLower(c.Name)
		if strings.Contains(cLower, nameLower) || strings.Contains(nameLower, cLower) {
			return &ClassifyResult{CollectionID: c.ID}, nil
		}
	}

	// LLM returned something unrecognised — treat it as a new collection suggestion
	return &ClassifyResult{
		SuggestNew:    true,
		SuggestedName: strings.ToLower(name),
	}, nil
}

// callLLM sends a single-turn chat request to the configured LLM.
func callLLM(ctx context.Context, cfg *config.Config, prompt string) (string, error) {
	reqBody, _ := json.Marshal(map[string]any{
		"model": cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens":  20,
		"temperature": 0,
	})

	endpoint := strings.TrimRight(cfg.LLMEndpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.LLMAPIKey)

	client := sharedHTTPClient
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in LLM response")
	}
	return result.Choices[0].Message.Content, nil
}

// ── Provider: BGE Cosine ──────────────────────────────────────────────────────

// classifyBGECosine uses the local BGE embedding model to classify by cosine similarity.
// The document is embedded, then compared against the embedding of each collection name.
// Accuracy is approximately 65% — suitable when collections have descriptive names.
func classifyBGECosine(ctx context.Context, cfg *config.Config, data ClassifyInput, cols []store.Collection) (*ClassifyResult, error) {
	if len(cols) == 0 {
		// No existing collections: suggest a name based on title
		name := data.Title
		if name == "" {
			name = "default"
		}
		// Truncate to a reasonable collection name length
		words := strings.Fields(name)
		if len(words) > 4 {
			words = words[:4]
		}
		return &ClassifyResult{
			SuggestNew:    true,
			SuggestedName: strings.ToLower(strings.Join(words, " ")),
		}, nil
	}

	embedder, err := embed.New(cfg.DefaultModel, cfg)
	if err != nil {
		return nil, fmt.Errorf("bge-cosine: init embedder: %w", err)
	}

	// Build document text (title + first 400 chars of content)
	docText := data.Title
	if data.PlainText != "" {
		preview := data.PlainText
		if len(preview) > 400 {
			preview = preview[:400]
		}
		if docText != "" {
			docText = docText + "\n" + preview
		} else {
			docText = preview
		}
	}

	// Build texts to embed: [document] + [collection names]
	texts := make([]string, 0, 1+len(cols))
	texts = append(texts, docText)
	for _, c := range cols {
		colText := c.Name
		if c.Description != "" {
			colText = c.Name + ": " + c.Description
		}
		texts = append(texts, colText)
	}

	vectors, err := embedder.Embed(ctx, texts)
	if err != nil {
		return nil, fmt.Errorf("bge-cosine: embed: %w", err)
	}
	if len(vectors) != len(texts) {
		return nil, fmt.Errorf("bge-cosine: expected %d vectors, got %d", len(texts), len(vectors))
	}

	docVec := vectors[0]
	bestIdx := -1
	bestScore := -2.0

	for i, colVec := range vectors[1:] {
		score := cosineSimilarity(docVec, colVec)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return &ClassifyResult{CollectionID: cols[0].ID}, nil
	}
	return &ClassifyResult{CollectionID: cols[bestIdx].ID}, nil
}

// cosineSimilarity computes cosine similarity between two float32 vectors.
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ── Provider: NLI Cross-Encoder ───────────────────────────────────────────────

// classifyNLI uses a local NLI cross-encoder ONNX model (nli-deberta-v3-small)
// to perform zero-shot classification. For each collection, it scores how well
// the document "entails" belonging to that collection.
// Accuracy is approximately 80% across multilingual inputs.
func classifyNLI(ctx context.Context, cfg *config.Config, data ClassifyInput, cols []store.Collection) (*ClassifyResult, error) {
	if len(cols) == 0 {
		name := data.Title
		if name == "" {
			name = "default"
		}
		words := strings.Fields(name)
		if len(words) > 4 {
			words = words[:4]
		}
		return &ClassifyResult{
			SuggestNew:    true,
			SuggestedName: strings.ToLower(strings.Join(words, " ")),
		}, nil
	}

	// Build document preview (title + first 400 chars)
	docText := data.Title
	if data.PlainText != "" {
		preview := data.PlainText
		if len(preview) > 400 {
			preview = preview[:400]
		}
		if docText != "" {
			docText = docText + " " + preview
		} else {
			docText = preview
		}
	}

	scorer, err := newNLIScorer(cfg)
	if err != nil {
		return nil, fmt.Errorf("nli: init scorer: %w", err)
	}
	defer scorer.Close()

	bestIdx := -1
	bestScore := math.Inf(-1)

	for i, col := range cols {
		hypothesis := "This document belongs to the collection: " + col.Name
		if col.Description != "" {
			hypothesis = "This document is about " + col.Name + ": " + col.Description
		}
		score, err := scorer.Score(ctx, docText, hypothesis)
		if err != nil {
			return nil, fmt.Errorf("nli: score collection %q: %w", col.Name, err)
		}
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return &ClassifyResult{CollectionID: cols[0].ID}, nil
	}
	return &ClassifyResult{CollectionID: cols[bestIdx].ID}, nil
}
