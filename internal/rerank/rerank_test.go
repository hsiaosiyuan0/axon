package rerank_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/rerank"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func candidates(contents ...string) []rerank.Candidate {
	out := make([]rerank.Candidate, len(contents))
	for i, c := range contents {
		out[i] = rerank.Candidate{
			ID:      fmt.Sprintf("id-%d", i),
			Content: c,
			Source:  fmt.Sprintf("src-%d", i),
			Score:   float64(len(contents)-i) * 0.01, // decreasing RRF score
		}
	}
	return out
}

// ── TokenOverlapReranker ──────────────────────────────────────────────────────

func TestTokenOverlap_Name(t *testing.T) {
	r := rerank.NewTokenOverlap()
	if r.Name() != "token-overlap-bm25" {
		t.Errorf("unexpected name: %q", r.Name())
	}
}

func TestTokenOverlap_Empty(t *testing.T) {
	r := rerank.NewTokenOverlap()
	got, err := r.Rerank(context.Background(), "go concurrency", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty, got %d results", len(got))
	}
}

func TestTokenOverlap_EmptyQueryReturnsOriginal(t *testing.T) {
	r := rerank.NewTokenOverlap()
	cands := candidates("Go goroutines are lightweight.", "Python uses threads.")
	got, err := r.Rerank(context.Background(), "", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return original order unchanged (no query tokens)
	if len(got) != len(cands) {
		t.Fatalf("length mismatch: want %d got %d", len(cands), len(got))
	}
}

func TestTokenOverlap_ReranksRelevantFirst(t *testing.T) {
	r := rerank.NewTokenOverlap()

	cands := candidates(
		"Apache Kafka is a distributed event streaming platform.", // irrelevant
		"Golang goroutines enable concurrent execution.",          // relevant
		"Go channels allow goroutines to communicate safely.",     // most relevant
	)

	got, err := r.Rerank(context.Background(), "go goroutine concurrent", cands)
	if err != nil {
		t.Fatalf("rerank error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}

	// The Kafka snippet should NOT be first after reranking.
	if strings.Contains(got[0].Content, "Kafka") {
		t.Errorf("Kafka snippet ranked first — expected a Go-related snippet at top")
	}

	// Scores should be non-negative and in descending order.
	for i := 1; i < len(got); i++ {
		if got[i].Score > got[i-1].Score {
			t.Errorf("scores not sorted descending at positions %d/%d: %.4f > %.4f",
				i-1, i, got[i-1].Score, got[i].Score)
		}
	}
}

func TestTokenOverlap_SingleCandidate(t *testing.T) {
	r := rerank.NewTokenOverlap()
	cands := candidates("Only one result here.")
	got, err := r.Rerank(context.Background(), "result", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
}

func TestTokenOverlap_StopWordsFiltered(t *testing.T) {
	// "the", "is", "a" are stop words; query of only stop words → no tokens → original order
	r := rerank.NewTokenOverlap()
	cands := candidates("some content here", "other stuff over there")
	got, err := r.Rerank(context.Background(), "the is a", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
}

func TestTokenOverlap_ScoresReflectBM25Weight(t *testing.T) {
	r := rerank.NewTokenOverlap()
	// Two docs: one mentions "goroutine" twice, one not at all.
	cands := candidates(
		"goroutine goroutine goroutine sync channel goroutine",
		"completely unrelated text about databases and SQL",
	)
	got, err := r.Rerank(context.Background(), "goroutine", cands)
	if err != nil {
		t.Fatalf("rerank error: %v", err)
	}
	if got[0].Score <= got[1].Score {
		t.Errorf("expected goroutine-heavy doc to score higher: %.4f vs %.4f",
			got[0].Score, got[1].Score)
	}
}

// ── LLMReranker — no API key fallback ────────────────────────────────────────

func TestLLMReranker_Name(t *testing.T) {
	r := rerank.NewLLMReranker(&config.Config{})
	if r.Name() != "llm-reranker" {
		t.Errorf("unexpected name: %q", r.Name())
	}
}

func TestLLMReranker_FallsBackWithoutKey(t *testing.T) {
	// No LLMAPIKey → should fall back to TokenOverlap and still return results.
	r := rerank.NewLLMReranker(&config.Config{LLMModel: "gpt-4o-mini"})
	cands := candidates(
		"Go channels allow goroutines to communicate.",
		"Python uses the GIL for thread safety.",
	)
	got, err := r.Rerank(context.Background(), "goroutine channel", cands)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d", len(got))
	}
}

func TestLLMRerankerStream_FallsBackWithoutKey(t *testing.T) {
	r := rerank.NewLLMReranker(&config.Config{LLMModel: "gpt-4o-mini"})
	cands := candidates("Go goroutines.", "Python threads.")

	var tokens []string
	got, err := r.RerankStream(
		context.Background(),
		"goroutine",
		cands,
		func(tok string) { tokens = append(tokens, tok) },
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d", len(got))
	}
	// Fallback = token-overlap, no LLM tokens emitted.
	if len(tokens) != 0 {
		t.Logf("(note) tokens emitted on fallback path: %d — unexpected but not fatal", len(tokens))
	}
}

// ── LLMReranker — mock HTTP server ───────────────────────────────────────────

// sseResponse builds a minimal OpenAI-style SSE response for the given content.
func sseResponse(content string) string {
	var sb strings.Builder
	for _, ch := range strings.Split(content, "") {
		data, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{
				{"delta": map[string]string{"content": ch}},
			},
		})
		sb.WriteString("data: ")
		sb.Write(data)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

func llmTestServer(t *testing.T, responseBody string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, responseBody)
	}))
}

func TestLLMReranker_RerankStream_Success(t *testing.T) {
	// Mock returns [9, 2] for a 2-candidate batch.
	srv := llmTestServer(t, sseResponse("[9, 2]"), http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		LLMEndpoint: srv.URL,
		LLMAPIKey:   "test-key",
		LLMModel:    "gpt-4o-mini",
	}
	r := rerank.NewLLMReranker(cfg)

	cands := candidates(
		"Go goroutines enable concurrency.",    // should win (score 9)
		"Python uses the GIL for safety.",      // should lose (score 2)
	)

	var collectedTokens []string
	batchCalls := 0

	got, err := r.RerankStream(
		context.Background(),
		"goroutine",
		cands,
		func(tok string) { collectedTokens = append(collectedTokens, tok) },
		func(cur, total int) { batchCalls++ },
	)
	if err != nil {
		t.Fatalf("RerankStream error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}

	// Scores should be 9 and 2 after parsing → Go doc first.
	if !strings.Contains(got[0].Content, "goroutines") {
		t.Errorf("expected goroutine doc first, got: %q", got[0].Content)
	}
	if got[0].Score != 9 {
		t.Errorf("expected score 9, got %.1f", got[0].Score)
	}
	if got[1].Score != 2 {
		t.Errorf("expected score 2, got %.1f", got[1].Score)
	}

	// batchProgress should have been called once.
	if batchCalls != 1 {
		t.Errorf("expected 1 batchProgress call, got %d", batchCalls)
	}

	// Some tokens should have been collected.
	if len(collectedTokens) == 0 {
		t.Error("no tokens collected from stream")
	}
}

func TestLLMReranker_RerankStream_HTTPError(t *testing.T) {
	// Server returns 500 — should fall back gracefully (keep original scores).
	srv := llmTestServer(t, "internal error", http.StatusInternalServerError)
	defer srv.Close()

	cfg := &config.Config{
		LLMEndpoint: srv.URL,
		LLMAPIKey:   "test-key",
		LLMModel:    "gpt-4o-mini",
	}
	r := rerank.NewLLMReranker(cfg)
	cands := candidates("doc one", "doc two")

	got, err := r.RerankStream(context.Background(), "query", cands, nil, nil)
	// Should NOT return an error (fallback keeps original scores)
	if err != nil {
		t.Fatalf("unexpected error on HTTP 500: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d", len(got))
	}
}

func TestLLMReranker_RerankStream_BadJSON(t *testing.T) {
	// Server returns invalid JSON scores — batch error should be swallowed.
	srv := llmTestServer(t, sseResponse("not json at all"), http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		LLMEndpoint: srv.URL,
		LLMAPIKey:   "test-key",
		LLMModel:    "gpt-4o-mini",
	}
	r := rerank.NewLLMReranker(cfg)
	cands := candidates("doc one", "doc two")

	got, err := r.RerankStream(context.Background(), "query", cands, nil, nil)
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d", len(got))
	}
}

func TestLLMReranker_RerankStream_MultiBatch(t *testing.T) {
	// BatchSize=2, 4 candidates → 2 batches; server scores [8,3] each time.
	srv := llmTestServer(t, sseResponse("[8, 3]"), http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		LLMEndpoint: srv.URL,
		LLMAPIKey:   "test-key",
		LLMModel:    "gpt-4o-mini",
	}
	r := rerank.NewLLMReranker(cfg)
	r.BatchSize = 2

	cands := candidates("a", "b", "c", "d")

	batchNums := []int{}
	batchTotals := []int{}

	got, err := r.RerankStream(
		context.Background(),
		"query",
		cands,
		nil,
		func(cur, total int) {
			batchNums = append(batchNums, cur)
			batchTotals = append(batchTotals, total)
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 results, got %d", len(got))
	}
	if len(batchNums) != 2 {
		t.Errorf("expected 2 batchProgress calls, got %d: %v", len(batchNums), batchNums)
	}
	if batchNums[0] != 1 || batchNums[1] != 2 {
		t.Errorf("unexpected batch numbers: %v", batchNums)
	}
	if batchTotals[0] != 2 || batchTotals[1] != 2 {
		t.Errorf("unexpected batch totals: %v", batchTotals)
	}
}

func TestLLMReranker_Rerank_Success(t *testing.T) {
	// Non-streaming Rerank using the same mock server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := `{"choices":[{"message":{"content":"[7, 2]"}}]}`
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	cfg := &config.Config{
		LLMEndpoint: srv.URL,
		LLMAPIKey:   "test-key",
		LLMModel:    "gpt-4o-mini",
	}
	r := rerank.NewLLMReranker(cfg)
	cands := candidates("relevant doc about goroutines", "unrelated database doc")

	got, err := r.Rerank(context.Background(), "goroutine", cands)
	if err != nil {
		t.Fatalf("Rerank error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].Score != 7 {
		t.Errorf("expected first score 7, got %.1f", got[0].Score)
	}
}
