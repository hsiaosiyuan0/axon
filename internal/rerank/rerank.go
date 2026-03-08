// Package rerank provides a two-stage reranker for Axon search results.
//
// Stage 1: BM25 + vector hybrid search (existing, in hybrid package)
// Stage 2: Rerank the candidate set using a lightweight cross-encoder-style scorer.
//
// Two rerankers are provided:
//   - TokenOverlap: pure Go, no deps, uses token-level BM25 between query and chunk
//   - LLMReranker:  uses the configured LLM to score relevance (higher quality, slower)
package rerank

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/tokenize"
)

// Candidate is a search result to be reranked.
type Candidate struct {
	ID         string  // chunk ID
	Content    string  // chunk text
	Source     string  // source label
	Collection string  // collection name
	Score      float64 // original RRF score
}

// Reranker ranks a list of candidates against a query.
type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error)
	Name() string
}

// ── Token Overlap Reranker ────────────────────────────────────────────────────

// TokenOverlapReranker is a fast, pure-Go reranker based on BM25 scoring
// between the query tokens and each candidate's content.
// No external dependencies, <1ms per candidate.
type TokenOverlapReranker struct {
	K1 float64 // BM25 term frequency saturation (default 1.5)
	B  float64 // BM25 length normalization (default 0.75)

	// RerankWeight controls the blend between the BM25 rerank score and the
	// original RRF score: finalScore = RerankWeight*bm25 + (1-RerankWeight)*rrfScaled.
	// Default: 0.7 (favour rerank signal).
	RerankWeight float64

	// RRFScaleFactor scales the original RRF score (typically ~0.016) to a
	// comparable magnitude as the BM25 score before blending. Default: 100.
	RRFScaleFactor float64
}

func NewTokenOverlap() *TokenOverlapReranker {
	return &TokenOverlapReranker{K1: 1.5, B: 0.75, RerankWeight: 0.7, RRFScaleFactor: 100}
}

func (r *TokenOverlapReranker) Name() string { return "token-overlap-bm25" }

func (r *TokenOverlapReranker) Rerank(_ context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if len(candidates) == 0 {
		return candidates, nil
	}

	queryTokens := tokenize.WordsNoStop(query)
	if len(queryTokens) == 0 {
		return candidates, nil
	}

	// Compute average document length
	totalLen := 0
	docTokens := make([][]string, len(candidates))
	for i, c := range candidates {
		tokens := tokenize.WordsNoStop(c.Content)
		docTokens[i] = tokens
		totalLen += len(tokens)
	}
	avgLen := float64(totalLen) / float64(len(candidates))

	// Build IDF from the candidate set (mini-corpus)
	df := make(map[string]int)
	for _, tokens := range docTokens {
		seen := make(map[string]bool)
		for _, t := range tokens {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}
	N := float64(len(candidates))
	idf := func(term string) float64 {
		d := float64(df[term])
		if d == 0 {
			return 0
		}
		return math.Log((N-d+0.5)/(d+0.5) + 1)
	}

	// Score each candidate
	result := make([]Candidate, len(candidates))
	copy(result, candidates)

	for i, tokens := range docTokens {
		// Term frequency map
		tf := make(map[string]int)
		for _, t := range tokens {
			tf[t]++
		}
		docLen := float64(len(tokens))

		score := 0.0
		for _, qt := range queryTokens {
			tfq := float64(tf[qt])
			if tfq == 0 {
				continue
			}
			idfq := idf(qt)
			// BM25 formula
			score += idfq * (tfq * (r.K1 + 1)) / (tfq + r.K1*(1-r.B+r.B*docLen/avgLen))
		}

		// Blend with original RRF score (RerankWeight% rerank, remainder original)
		result[i].Score = r.RerankWeight*score + (1-r.RerankWeight)*candidates[i].Score*r.RRFScaleFactor
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result, nil
}

// ── LLM Reranker ─────────────────────────────────────────────────────────────

// LLMReranker asks the configured LLM to score each candidate's relevance
// to the query on a 0-10 scale, then re-sorts.
type LLMReranker struct {
	cfg        *config.Config
	httpClient *http.Client
	BatchSize  int // max candidates per LLM call (default 5)
}

func NewLLMReranker(cfg *config.Config) *LLMReranker {
	return &LLMReranker{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		BatchSize:  5,
	}
}

func (r *LLMReranker) Name() string { return "llm-reranker" }

func (r *LLMReranker) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Candidate, error) {
	if r.cfg.LLMAPIKey == "" {
		// Fallback to token overlap if no LLM key
		return NewTokenOverlap().Rerank(ctx, query, candidates)
	}

	result := make([]Candidate, len(candidates))
	copy(result, candidates)

	// Process in batches
	for start := 0; start < len(candidates); start += r.BatchSize {
		end := start + r.BatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]

		scores, err := r.scoreBatch(ctx, query, batch)
		if err != nil {
			// Fallback: keep original scores for this batch
			fmt.Printf("  ⚠️  LLM rerank batch failed: %v (keeping original scores)\n", err)
			continue
		}
		for i, score := range scores {
			result[start+i].Score = score
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result, nil
}

// RerankStream is like Rerank but streams raw LLM tokens via onToken callback.
// onToken is called on the goroutine that drives the HTTP response, so callers
// must be safe for concurrent use (e.g. send to a channel).
// batchProgress is called at the start of each batch with (current, total).
func (r *LLMReranker) RerankStream(
	ctx context.Context,
	query string,
	candidates []Candidate,
	onToken func(token string),
	batchProgress func(current, total int),
) ([]Candidate, error) {
	if r.cfg.LLMAPIKey == "" {
		return NewTokenOverlap().Rerank(ctx, query, candidates)
	}

	result := make([]Candidate, len(candidates))
	copy(result, candidates)

	totalBatches := (len(candidates) + r.BatchSize - 1) / r.BatchSize

	for start := 0; start < len(candidates); start += r.BatchSize {
		end := start + r.BatchSize
		if end > len(candidates) {
			end = len(candidates)
		}
		batch := candidates[start:end]
		batchIdx := start/r.BatchSize + 1

		if batchProgress != nil {
			batchProgress(batchIdx, totalBatches)
		}

		scores, err := r.scoreBatchStream(ctx, query, batch, onToken)
		if err != nil {
			if onToken != nil {
				onToken(fmt.Sprintf("\n⚠️  batch %d failed: %v\n", batchIdx, err))
			}
			continue
		}
		for i, score := range scores {
			result[start+i].Score = score
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Score > result[j].Score
	})
	return result, nil
}

// scoreBatchStream scores one batch via streaming SSE and calls onToken for each chunk.
func (r *LLMReranker) scoreBatchStream(
	ctx context.Context,
	query string,
	batch []Candidate,
	onToken func(string),
) ([]float64, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Query: %q\n\n", query))
	sb.WriteString("Rate each passage's relevance to the query from 0 to 10.\n")
	sb.WriteString("Output ONLY a JSON array of numbers, e.g. [8, 3, 7, 2, 5]\n\n")
	for i, c := range batch {
		content := c.Content
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		sb.WriteString(fmt.Sprintf("Passage %d:\n%s\n\n", i+1, content))
	}

	payload := map[string]any{
		"model": r.cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "user", "content": sb.String()},
		},
		"temperature": 0.0,
		"max_tokens":  64,
		"stream":      true,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.cfg.LLMEndpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.LLMAPIKey)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		return nil, fmt.Errorf("LLM error %d: %s", resp.StatusCode, string(errBody))
	}

	// Read SSE stream, accumulate full content
	var fullContent strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 {
			token := chunk.Choices[0].Delta.Content
			if token != "" {
				fullContent.WriteString(token)
				if onToken != nil {
					onToken(token)
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("stream read error: %w", err)
	}

	// Parse the accumulated JSON scores
	content := strings.TrimSpace(fullContent.String())
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var scores []float64
	if err := json.Unmarshal([]byte(content), &scores); err != nil {
		return nil, fmt.Errorf("parse scores JSON: %w (got: %s)", err, content)
	}
	if len(scores) != len(batch) {
		return nil, fmt.Errorf("expected %d scores, got %d", len(batch), len(scores))
	}
	return scores, nil
}

func (r *LLMReranker) scoreBatch(ctx context.Context, query string, batch []Candidate) ([]float64, error) {
	// Build prompt listing all candidates
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Query: %q\n\n", query))
	sb.WriteString("Rate each passage's relevance to the query from 0 to 10.\n")
	sb.WriteString("Output ONLY a JSON array of numbers, e.g. [8, 3, 7, 2, 5]\n\n")
	for i, c := range batch {
		content := c.Content
		if len(content) > 500 {
			content = content[:500] + "…"
		}
		sb.WriteString(fmt.Sprintf("Passage %d:\n%s\n\n", i+1, content))
	}

	payload := map[string]any{
		"model": r.cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "user", "content": sb.String()},
		},
		"temperature": 0.0,
		"max_tokens":  64,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		r.cfg.LLMEndpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.cfg.LLMAPIKey)

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM error %d: %s", resp.StatusCode, string(respBody))
	}

	var llmResp struct {
		Choices []struct {
			Message struct{ Content string } `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &llmResp); err != nil || len(llmResp.Choices) == 0 {
		return nil, fmt.Errorf("parse LLM response")
	}

	content := strings.TrimSpace(llmResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var scores []float64
	if err := json.Unmarshal([]byte(content), &scores); err != nil {
		return nil, fmt.Errorf("parse scores JSON: %w (got: %s)", err, content)
	}
	if len(scores) != len(batch) {
		return nil, fmt.Errorf("expected %d scores, got %d", len(batch), len(scores))
	}
	return scores, nil
}
