// Package relate — llm.go provides LLM-based relation extraction.
// It sends text chunks to an LLM and asks it to identify named entities
// and semantic relations as triples (subject → predicate → object).
// Supports checkpoint-based resumption via progress.go.
package relate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// Triple is a single relation extracted by the LLM.
type Triple struct {
	Subject   string `json:"subject"`   // source entity name
	Predicate string `json:"predicate"` // relation verb / type
	Object    string `json:"object"`    // target entity name
	Evidence  string `json:"evidence"`  // supporting sentence
}

// LLMOptions controls LLM relation extraction.
type LLMOptions struct {
	Collection string // optional: limit to a collection
	SourceID   string // optional: process only this source
	MaxChunks  int    // max chunks to process per source (0 = all)
	DryRun     bool   // print but don't save
	Verbose    bool
	Resume     bool   // resume from checkpoint (default true when progress file exists)
}

// LLMResult summarises what was extracted.
type LLMResult struct {
	Sources   int
	Chunks    int
	Triples   int
	Relations int // actually saved
}

// ExtractWithLLM uses the configured LLM to extract semantic triples from
// every chunk in the knowledge base (or a filtered subset).
// Progress is automatically checkpointed to disk; if interrupted, re-running
// with the same options will resume from the last completed source.
func ExtractWithLLM(ctx context.Context, cfg *config.Config, opts LLMOptions) (*LLMResult, error) {
	if cfg.LLMAPIKey == "" {
		return nil, fmt.Errorf("AXON_LLM_API_KEY is not set — required for LLM relation extraction")
	}
	if opts.MaxChunks == 0 {
		opts.MaxChunks = 10 // conservative default per source
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	sources, err := loadSources(db, opts.Collection)
	if err != nil {
		return nil, err
	}

	// Filter to single source if requested
	if opts.SourceID != "" {
		var filtered []store.Source
		for _, s := range sources {
			if s.ID == opts.SourceID || s.Origin == opts.SourceID {
				filtered = append(filtered, s)
				break
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("source %q not found", opts.SourceID)
		}
		sources = filtered
	}

	// ── Progress manager (checkpoint / resume) ─────────────────────────────
	axonDir := filepath.Dir(cfg.DBPath)
	pm, err := newProgressManager(axonDir, opts)
	if err != nil {
		// Non-fatal: log and continue without persistence
		fmt.Printf("⚠️  Progress persistence unavailable: %v\n", err)
		pm = nil
	}

	result := &LLMResult{Sources: len(sources)}

	// If resuming, restore previous stats
	if pm != nil && pm.wasResumed() && pm.doneCount() > 0 {
		_, prevChunks, prevTriples, prevRels := pm.resume()
		fmt.Printf("⏩ Resuming from checkpoint — %d sources already done\n", pm.doneCount())
		result.Sources = len(sources)
		result.Chunks = prevChunks
		result.Triples = prevTriples
		result.Relations = prevRels
	}

	client := newLLMClient(cfg)

	for _, src := range sources {
		// Skip already-processed sources
		if pm != nil && pm.isDone(src.ID) {
			continue
		}

		chunks, err := db.Chunks().GetBySourceID(src.ID)
		if err != nil || len(chunks) == 0 {
			if pm != nil && !opts.DryRun {
				_ = pm.markDone(src.ID, 0, 0, 0)
			}
			continue
		}
		if len(chunks) > opts.MaxChunks {
			chunks = chunks[:opts.MaxChunks]
		}

		label := labelFor(src)
		fmt.Printf("🤖 [LLM] %s (%d chunks)\n", label, len(chunks))

		srcChunks, srcTriples, srcRels := 0, 0, 0

		for _, chunk := range chunks {
			srcChunks++
			triples, err := client.extractTriples(ctx, chunk.Content)
			if err != nil {
				fmt.Printf("  ⚠️  chunk %s: %v\n", chunk.ID[:8], err)
				continue
			}
			srcTriples += len(triples)

			for _, triple := range triples {
				if opts.Verbose || opts.DryRun {
					fmt.Printf("  📎 %s → [%s] → %s\n", triple.Subject, triple.Predicate, triple.Object)
				}
				if opts.DryRun {
					continue
				}
				// Store as chunk-level relation (from_type = "chunk")
				_, err := db.Relations().Create(store.CreateRelationParams{
					FromType:       "chunk",
					FromID:         chunk.ID,
					FromCollection: src.Collection,
					ToType:         "concept",
					ToID:           normalizeEntity(triple.Object),
					ToCollection:   src.Collection,
					RelType:        normalizePredicate(triple.Predicate),
					Weight:         1.0,
					EstablishedBy:  "llm",
					Evidence:       triple.Evidence,
				})
				if err == nil {
					srcRels++
				}
			}
		}

		result.Chunks += srcChunks
		result.Triples += srcTriples
		result.Relations += srcRels

		// Checkpoint: mark source as done
		if pm != nil && !opts.DryRun {
			if err := pm.markDone(src.ID, srcChunks, srcTriples, srcRels); err != nil {
				fmt.Printf("  ⚠️  checkpoint save failed: %v\n", err)
			}
		}
	}

	// Remove progress file on successful completion (not dry-run)
	if pm != nil && !opts.DryRun {
		pm.complete()
	}

	return result, nil
}

// ── LLM client ────────────────────────────────────────────────────────────────

type llmClient struct {
	cfg        *config.Config
	httpClient *http.Client
}

func newLLMClient(cfg *config.Config) *llmClient {
	return &llmClient{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

const extractionPrompt = `Extract all semantic relations from the following text as a JSON array of triples.
Each triple must have: "subject" (entity name), "predicate" (relation type, e.g. "is-a", "part-of", "created-by", "uses", "causes", "located-in"), "object" (entity name), "evidence" (the exact supporting sentence).
Only include factual, meaningful relations. Skip trivial ones. Output ONLY valid JSON array, no markdown, no explanation.

Text:
%s`

func (c *llmClient) extractTriples(ctx context.Context, text string) ([]Triple, error) {
	// Truncate very long chunks
	if len(text) > 3000 {
		text = text[:3000] + "…"
	}

	prompt := fmt.Sprintf(extractionPrompt, text)

	payload := map[string]any{
		"model": c.cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.1,
		"max_tokens":  1024,
	}

	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.LLMEndpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.LLMAPIKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(respBody))
	}

	// Parse OpenAI-compatible response
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(result.Choices) == 0 {
		return nil, fmt.Errorf("empty response from LLM")
	}

	content := strings.TrimSpace(result.Choices[0].Message.Content)

	// Strip markdown code block if present
	content = stripCodeBlock(content)

	var triples []Triple
	if err := json.Unmarshal([]byte(content), &triples); err != nil {
		// Sometimes the LLM wraps in an object
		var wrapped struct {
			Relations []Triple `json:"relations"`
			Triples   []Triple `json:"triples"`
		}
		if err2 := json.Unmarshal([]byte(content), &wrapped); err2 == nil {
			if len(wrapped.Relations) > 0 {
				return wrapped.Relations, nil
			}
			return wrapped.Triples, nil
		}
		return nil, fmt.Errorf("parse triples JSON: %w\nContent: %s", err, content)
	}
	return triples, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func normalizeEntity(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizePredicate(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func stripCodeBlock(s string) string {
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}
