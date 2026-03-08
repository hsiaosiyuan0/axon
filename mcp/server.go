package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// Serve runs the MCP server over stdio (JSON-RPC 2.0).
func Serve(ctx context.Context, cfg *config.Config) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	encoder := json.NewEncoder(os.Stdout)

	searcher, err := hybrid.NewSearcher(cfg)
	if err != nil {
		return err
	}
	defer searcher.Close()

	for scanner.Scan() {
		line := scanner.Bytes()
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}

		resp := handle(ctx, &req, cfg, searcher)
		if err := encoder.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func handle(ctx context.Context, req *Request, cfg *config.Config, searcher *hybrid.Searcher) *Response {
	switch req.Method {
	case "initialize":
		return req.ok(map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "axon", "version": "0.1.0"},
		})

	case "tools/list":
		return req.ok(map[string]any{"tools": tools})

	case "tools/call":
		return handleToolCall(ctx, req, cfg, searcher)

	default:
		return req.err(-32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func handleToolCall(ctx context.Context, req *Request, cfg *config.Config, searcher *hybrid.Searcher) *Response {
	var params struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return req.err(-32600, "invalid params")
	}

	switch params.Name {
	case "memory_query":
		query, _ := params.Arguments["query"].(string)
		collection, _ := params.Arguments["collection"].(string)
		limitF, _ := params.Arguments["limit"].(float64)
		rerankRaw, _ := params.Arguments["rerank"].(bool)
		rerankMode, _ := params.Arguments["rerank_mode"].(string)
		limit := int(limitF)
		if limit == 0 {
			limit = 5
		}
		if rerankMode == "" {
			rerankMode = "token"
		}

		results, err := searcher.Search(ctx, hybrid.SearchOptions{
			Query:      query,
			Collection: collection,
			Limit:      limit,
			Rerank:     rerankRaw,
			RerankMode: rerankMode,
		})
		if err != nil {
			return req.err(-32000, err.Error())
		}

		var out []map[string]any
		for _, r := range results {
			out = append(out, map[string]any{
				"content":      r.Content,
				"source":       r.Source,
				"source_title": r.SourceTitle,
				"collection":   r.Collection,
				"chunk_id":     r.ChunkID,
				"source_id":    r.SourceID,
				"score":        r.Score,
			})
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": mustJSON(out)},
			},
		})

	case "memory_add":
		text, _ := params.Arguments["text"].(string)
		title, _ := params.Arguments["title"].(string)
		collection, _ := params.Arguments["collection"].(string)
		if text == "" {
			return req.err(-32600, "text is required")
		}
		if title == "" {
			if len(text) > 60 {
				title = text[:60] + "…"
			} else {
				title = text
			}
		}

		svc, err := ingest.NewService(cfg)
		if err != nil {
			return req.err(-32000, fmt.Sprintf("open db: %v", err))
		}
		defer svc.Close()

		result, err := svc.AddSnippet(ctx, ingest.AddSnippetOptions{
			Text:       text,
			Title:      title,
			Collection: collection,
		})
		if err != nil {
			return req.err(-32000, err.Error())
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Added %d chunks to collection %q (source: %s)", result.ChunkCount, result.Collection, result.SourceID)},
			},
		})

	case "memory_collections":
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return req.err(-32000, err.Error())
		}
		defer db.Close()

		cols, err := db.Collections().List()
		if err != nil {
			return req.err(-32000, err.Error())
		}

		var out []map[string]any
		for _, c := range cols {
			out = append(out, map[string]any{
				"id":          c.ID,
				"name":        c.Name,
				"type":        c.Type,
				"description": c.Description,
			})
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": mustJSON(out)},
			},
		})

	case "memory_relate":
		chunkID, _ := params.Arguments["chunk_id"].(string)
		if chunkID == "" {
			return req.err(-32600, "chunk_id is required")
		}
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return req.err(-32000, err.Error())
		}
		defer db.Close()

		chunk, err := db.Chunks().GetByID(chunkID)
		if err != nil {
			return req.err(-32000, fmt.Sprintf("chunk not found: %v", err))
		}
		rels, _ := db.Relations().ListByFrom(chunk.SourceID)
		relsTo, _ := db.Relations().ListByTo(chunk.SourceID)
		rels = append(rels, relsTo...)

		var out []map[string]any
		for _, r := range rels {
			out = append(out, map[string]any{
				"rel_type":       r.RelType,
				"from_id":        r.FromID,
				"to_id":          r.ToID,
				"established_by": r.EstablishedBy,
				"evidence":       r.Evidence,
			})
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": mustJSON(out)},
			},
		})

	case "memory_delete":
		// Delete a source by origin path or URL
		origin, _ := params.Arguments["origin"].(string)
		if origin == "" {
			return req.err(-32600, "origin is required")
		}
		svc, err := ingest.NewService(cfg)
		if err != nil {
			return req.err(-32000, fmt.Sprintf("open db: %v", err))
		}
		defer svc.Close()

		if err := svc.Remove(origin); err != nil {
			return req.err(-32000, err.Error())
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": fmt.Sprintf("Deleted source: %s", origin)},
			},
		})

	case "memory_stats":
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return req.err(-32000, err.Error())
		}
		defer db.Close()

		sourceCount, _ := db.Sources().Count()
		chunkCount, _ := db.Chunks().Count()
		cols, _ := db.Collections().List()

		var colSummary []map[string]any
		for _, c := range cols {
			colSummary = append(colSummary, map[string]any{
				"id":   c.ID,
				"name": c.Name,
			})
		}

		stats := map[string]any{
			"sources":     sourceCount,
			"chunks":      chunkCount,
			"collections": len(cols),
			"collection_list": colSummary,
		}
		return req.ok(map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": mustJSON(stats)},
			},
		})

	default:
		return req.err(-32601, fmt.Sprintf("unknown tool: %s", params.Name))
	}
}

// ── JSON-RPC types ────────────────────────────────────────────────────────────

type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type Response struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (r *Request) ok(result any) *Response {
	return &Response{JSONRPC: "2.0", ID: r.ID, Result: result}
}

func (r *Request) err(code int, msg string) *Response {
	return &Response{JSONRPC: "2.0", ID: r.ID, Error: &Error{Code: code, Message: msg}}
}

func mustJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// tools is the MCP tool manifest.
var tools = []map[string]any{
	{
		"name":        "memory_query",
		"description": "Search the personal knowledge base using hybrid BM25 + vector search. Supports optional two-stage reranking for higher quality results.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":       map[string]any{"type": "string", "description": "Search query"},
				"collection":  map[string]any{"type": "string", "description": "Limit to collection (optional)"},
				"limit":       map[string]any{"type": "integer", "description": "Max results (default 5)"},
				"rerank":      map[string]any{"type": "boolean", "description": "Enable two-stage reranking for higher quality (default false)"},
				"rerank_mode": map[string]any{"type": "string", "description": "Reranker: 'token' (fast, default) or 'llm' (slow, best quality)"},
			},
			"required": []string{"query"},
		},
	},
	{
		"name":        "memory_add",
		"description": "Add a text snippet to the knowledge base",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text":       map[string]any{"type": "string", "description": "Text content to add"},
				"title":      map[string]any{"type": "string", "description": "Title/label for this snippet"},
				"collection": map[string]any{"type": "string", "description": "Collection ID or name (optional)"},
			},
			"required": []string{"text"},
		},
	},
	{
		"name":        "memory_collections",
		"description": "List all knowledge collections",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
	{
		"name":        "memory_relate",
		"description": "Get relations for a knowledge chunk",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"chunk_id": map[string]any{"type": "string", "description": "Chunk UUID"},
			},
			"required": []string{"chunk_id"},
		},
	},
	{
		"name":        "memory_delete",
		"description": "Delete a source (and all its chunks/embeddings/relations) from the knowledge base by its origin path or URL",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"origin": map[string]any{"type": "string", "description": "File path or URL that was originally added"},
			},
			"required": []string{"origin"},
		},
	},
	{
		"name":        "memory_stats",
		"description": "Return statistics about the knowledge base: total sources, chunks, collections",
		"inputSchema": map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	},
}
