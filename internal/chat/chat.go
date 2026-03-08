// Package chat implements RAG-based conversational QA over an Axon knowledge base.
package chat

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

// Message is a single turn in the conversation.
type Message struct {
	Role    string // "system" | "user" | "assistant"
	Content string
}

// Session holds the full conversation history.
type Session struct {
	Messages []Message
}

func NewSession() *Session {
	return &Session{}
}

// Add appends a message.
func (s *Session) Add(role, content string) {
	s.Messages = append(s.Messages, Message{Role: role, Content: content})
}

// Truncate keeps only the last n turns (user+assistant pairs) plus the system message.
func (s *Session) Truncate(maxTurns int) {
	if maxTurns <= 0 {
		return
	}
	var sys []Message
	var hist []Message
	for _, m := range s.Messages {
		if m.Role == "system" {
			sys = append(sys, m)
		} else {
			hist = append(hist, m)
		}
	}
	// Keep last maxTurns*2 messages (user+assistant each)
	limit := maxTurns * 2
	if len(hist) > limit {
		hist = hist[len(hist)-limit:]
	}
	s.Messages = append(sys, hist...)
}

// ---------------------------------------------------------------------------
// LLM client
// ---------------------------------------------------------------------------

// Client is a simple OpenAI-compatible chat completion client.
type Client struct {
	Endpoint string
	APIKey   string
	Model    string
	client   *http.Client
}

func NewClient(endpoint, apiKey, model string) *Client {
	return &Client{
		Endpoint: strings.TrimRight(endpoint, "/"),
		APIKey:   apiKey,
		Model:    model,
		client:   &http.Client{Timeout: 60 * time.Second},
	}
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Complete sends a non-streaming completion request.
func (c *Client) Complete(ctx context.Context, msgs []Message) (string, error) {
	chatMsgs := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		chatMsgs[i] = chatMessage{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: chatMsgs,
		Stream:   false,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(data))
	}

	var cr chatResponse
	if err := json.Unmarshal(data, &cr); err != nil {
		return "", err
	}
	if len(cr.Choices) == 0 {
		return "", fmt.Errorf("empty LLM response")
	}
	return cr.Choices[0].Message.Content, nil
}

// CompleteStream sends a streaming completion, writing tokens to out as they arrive.
func (c *Client) CompleteStream(ctx context.Context, msgs []Message, out io.Writer) (string, error) {
	chatMsgs := make([]chatMessage, len(msgs))
	for i, m := range msgs {
		chatMsgs[i] = chatMessage{Role: m.Role, Content: m.Content}
	}

	body, err := json.Marshal(chatRequest{
		Model:    c.Model,
		Messages: chatMsgs,
		Stream:   true,
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST",
		c.Endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM API error %d: %s", resp.StatusCode, string(data))
	}

	var full strings.Builder
	buf := make([]byte, 4096)
	leftover := ""

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			chunk := leftover + string(buf[:n])
			leftover = ""
			lines := strings.Split(chunk, "\n")
			for i, line := range lines {
				line = strings.TrimSpace(line)
				if i == len(lines)-1 && !strings.HasSuffix(chunk, "\n") {
					leftover = line
					continue
				}
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := line[6:]
				if data == "[DONE]" {
					goto done
				}
				var cr chatResponse
				if err := json.Unmarshal([]byte(data), &cr); err != nil {
					continue
				}
				if len(cr.Choices) > 0 {
					token := cr.Choices[0].Delta.Content
					if token != "" {
						fmt.Fprint(out, token)
						full.WriteString(token)
					}
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return full.String(), readErr
		}
	}

done:
	return full.String(), nil
}

// ---------------------------------------------------------------------------
// RAG helpers
// ---------------------------------------------------------------------------

// BuildSystemPrompt creates the RAG system prompt with retrieved context.
func BuildSystemPrompt(context []ContextChunk, systemExtra string) string {
	var sb strings.Builder
	sb.WriteString("You are a helpful assistant with access to a personal knowledge base.\n")
	sb.WriteString("Answer questions based on the retrieved context below.\n")
	sb.WriteString("If the context doesn't contain enough information, say so honestly.\n")
	sb.WriteString("Cite sources when relevant by referring to the source title or path.\n")

	if systemExtra != "" {
		sb.WriteString("\n")
		sb.WriteString(systemExtra)
	}

	if len(context) > 0 {
		sb.WriteString("\n\n--- RETRIEVED CONTEXT ---\n")
		for i, c := range context {
			sb.WriteString(fmt.Sprintf("\n[%d] Source: %s\n", i+1, c.Source))
			sb.WriteString(c.Content)
			sb.WriteString("\n")
		}
		sb.WriteString("--- END CONTEXT ---\n")
	}

	return sb.String()
}

// ContextChunk is a retrieved chunk for the RAG prompt.
type ContextChunk struct {
	Source  string
	Content string
	Score   float64
}
