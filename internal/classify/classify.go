// Package classify provides LLM-assisted collection classification.
package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// sharedHTTPClient is reused across all calls to avoid per-request connection
// pool creation and the associated overhead / ephemeral-port exhaustion risk.
var sharedHTTPClient = &http.Client{Timeout: 30 * time.Second}

// ClassifyResult holds the classification result.
type ClassifyResult struct {
	CollectionID  string // set when an existing collection matched
	SuggestNew    bool   // true when LLM wants a brand-new collection
	SuggestedName string // the new collection name when SuggestNew is true
}

// Classify asks the LLM to pick the most appropriate collection for the given content,
// or suggest a new collection name if none fit.
// Returns a ClassifyResult. The caller handles SuggestNew by creating the collection.
func Classify(ctx context.Context, cfg *config.Config, data ClassifyInput, cols []store.Collection) (*ClassifyResult, error) {
	if cfg.LLMAPIKey == "" {
		return nil, fmt.Errorf("LLM not configured")
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

// ClassifyInput is the document information used for classification.
type ClassifyInput struct {
	Title     string
	PlainText string
	Origin    string
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
