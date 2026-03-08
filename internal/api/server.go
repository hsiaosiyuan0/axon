// Package api provides a lightweight HTTP REST server for Axon.
// It exposes knowledge base operations over a simple JSON API,
// allowing external tools (Claude, Cursor, scripts) to query and
// add knowledge without using the CLI directly.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/graph"
	"github.com/hsiaosiyuan0/axon/internal/hub"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/hsiaosiyuan0/axon/internal/ui"
)

// Server is the Axon HTTP API server.
type Server struct {
	cfg  *config.Config
	mux  *http.ServeMux
	addr string
	hub  *hub.Hub
	db   *store.DB // shared connection; opened once, closed on shutdown
}

// New creates a new API server bound to addr (e.g. "localhost:7474").
func New(cfg *config.Config, addr string) *Server {
	h := hub.New()
	s := &Server{cfg: cfg, addr: addr, mux: http.NewServeMux(), hub: h}
	s.routes()
	return s
}

// authMiddleware checks for a valid X-API-Key header when cfg.APIKey is set.
// /health is always public (for liveness probes).
func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" || r.URL.Path == "/health" {
			next.ServeHTTP(w, r)
			return
		}
		key := r.Header.Get("X-API-Key")
		if key == "" {
			// Also accept Bearer token for convenience
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				key = strings.TrimPrefix(auth, "Bearer ")
			}
		}
		if key != s.cfg.APIKey {
			w.Header().Set("WWW-Authenticate", `ApiKey realm="Axon"`)
			jsonError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ListenAndServe starts the server. Blocks until ctx is cancelled.
func (s *Server) ListenAndServe(ctx context.Context) error {
	// Open shared DB connection for the lifetime of the server.
	db, err := store.Open(s.cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	s.db = db

	go s.hub.Run()
	defer func() {
		s.hub.Close()
		s.db.Close()
	}()

	srv := &http.Server{
		Addr:         s.addr,
		Handler:      s.authMiddleware(s.mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// ── Routes ────────────────────────────────────────────────────────────────────

func (s *Server) routes() {
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/ui", s.handleUI)
	s.mux.HandleFunc("/ui/", s.handleUI)
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/v1/query", s.handleQuery)
	s.mux.HandleFunc("/v1/add", s.handleAdd)
	s.mux.HandleFunc("/v1/collections", s.handleCollections)
	s.mux.HandleFunc("/v1/sources", s.handleSources)
	s.mux.HandleFunc("/v1/status", s.handleStatus)
	s.mux.HandleFunc("/v1/graph", s.handleGraph)
	s.mux.HandleFunc("/v1/watch", s.hub.ServeSSE)
}

// ── Handlers ──────────────────────────────────────────────────────────────────

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	jsonOK(w, map[string]any{
		"name":    "Axon API",
		"version": "v1",
		"ui":      "/ui",
		"endpoints": []string{
			"GET  /health",
			"GET  /ui                    (Web interface)",
			"GET  /v1/status",
			"GET  /v1/collections",
			"GET  /v1/sources?collection=<name>&limit=<n>",
			"GET  /v1/query?q=<text>&collection=<name>&limit=<n>",
			"POST /v1/query  {\"q\":\"...\",\"collection\":\"...\",\"limit\":5}",
			"POST /v1/add    {\"origin\":\"...\",\"collection\":\"...\"}",
			"GET  /v1/graph?collection=<name>&root=<id>&depth=<n>&max_nodes=<n>",
			"GET  /v1/watch  (Server-Sent Events stream)",
		},
	})
}

// handleUI serves the embedded single-page web interface.
// GET /ui  or  GET /ui/
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(ui.IndexHTML)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.db.Ping(); err != nil {
		jsonError(w, http.StatusServiceUnavailable, "db unavailable: "+err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "ok", "db": s.cfg.DBPath})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	cols, _ := s.db.Collections().List()

	// Single aggregated COUNT queries — avoids N² round-trips over sources/chunks.
	totalSrc, _ := s.db.Sources().Count()
	totalChunk, _ := s.db.Chunks().Count()
	relCount, _ := s.db.Relations().Count()

	jsonOK(w, map[string]any{
		"collections": len(cols),
		"sources":     totalSrc,
		"chunks":      totalChunk,
		"relations":   relCount,
		"db_path":     s.cfg.DBPath,
	})
}

func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	cols, err := s.db.Collections().List()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]any{"collections": cols})
}

func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	collectionHint := r.URL.Query().Get("collection")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	var sources []store.Source
	var err error
	if collectionHint != "" {
		col, err := s.db.Collections().Get(collectionHint)
		if err != nil {
			jsonError(w, http.StatusNotFound, fmt.Sprintf("collection %q not found", collectionHint))
			return
		}
		sources, err = s.db.Sources().ListByCollection(col.ID)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	} else {
		sources, err = s.db.Sources().List()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if len(sources) > limit {
		sources = sources[:limit]
	}

	// Return lightweight view (no raw content)
	type srcView struct {
		ID         string    `json:"id"`
		Collection string    `json:"collection"`
		SourceType string    `json:"source_type"`
		Origin     string    `json:"origin"`
		Title      string    `json:"title"`
		Lang       string    `json:"lang,omitempty"`
		CreatedAt  time.Time `json:"created_at"`
	}
	views := make([]srcView, len(sources))
	for i, src := range sources {
		views[i] = srcView{
			ID:         src.ID,
			Collection: src.Collection,
			SourceType: src.SourceType,
			Origin:     src.Origin,
			Title:      src.Title,
			Lang:       src.Lang,
			CreatedAt:  src.CreatedAt,
		}
	}
	jsonOK(w, map[string]any{"sources": views, "total": len(views)})
}

// handleQuery supports both GET (?q=...) and POST (JSON body).
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	var q, collection string
	limit := 5
	var rerank bool
	var rerankMode string

	switch r.Method {
	case http.MethodGet:
		q = r.URL.Query().Get("q")
		collection = r.URL.Query().Get("collection")
		if l := r.URL.Query().Get("limit"); l != "" {
			if n, err := strconv.Atoi(l); err == nil && n > 0 {
				limit = n
			}
		}
	case http.MethodPost:
		var body struct {
			Q          string `json:"q"`
			Collection string `json:"collection"`
			Limit      int    `json:"limit"`
			Rerank     bool   `json:"rerank"`
			RerankMode string `json:"rerank_mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		q = body.Q
		collection = body.Collection
		if body.Limit > 0 {
			limit = body.Limit
		}
		rerank = body.Rerank
		rerankMode = body.RerankMode
	default:
		jsonError(w, http.StatusMethodNotAllowed, "GET or POST only")
		return
	}

	if strings.TrimSpace(q) == "" {
		jsonError(w, http.StatusBadRequest, "q is required")
		return
	}

	searcher, err := hybrid.NewSearcher(s.cfg)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer searcher.Close()

	results, err := searcher.Search(r.Context(), hybrid.SearchOptions{
		Query:      q,
		Collection: collection,
		Limit:      limit,
		Rerank:     rerank,
		RerankMode: rerankMode,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonOK(w, map[string]any{
		"query":      q,
		"collection": collection,
		"results":    resultsToJSON(results),
		"count":      len(results),
	})
}

// handleAdd ingests a new source. POST /v1/add
// Body: {"origin":"<url or path>","collection":"<name>"}
// Also supports text snippet: {"text":"...","title":"...","collection":"..."}
func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	var body struct {
		Origin     string `json:"origin"`
		Collection string `json:"collection"`
		Text       string `json:"text"`    // snippet mode
		Title      string `json:"title"`   // snippet mode
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	svc, err := ingest.NewService(s.cfg)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer svc.Close()

	var result *ingest.AddResult

	if body.Text != "" {
		// Snippet mode
		result, err = svc.AddSnippet(r.Context(), ingest.AddSnippetOptions{
			Text:       body.Text,
			Title:      body.Title,
			Collection: body.Collection,
		})
	} else if body.Origin != "" {
		// URL / file mode
		result, err = svc.Add(r.Context(), ingest.AddOptions{
			Origin:     body.Origin,
			Collection: body.Collection,
		})
	} else {
		jsonError(w, http.StatusBadRequest, "origin or text is required")
		return
	}

	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusCreated)
	jsonOK(w, map[string]any{
		"source_id":      result.SourceID,
		"title":          result.Title,
		"collection":     result.Collection,
		"chunk_count":    result.ChunkCount,
		"relation_count": result.RelationCount,
	})

	// Broadcast ingest event to SSE subscribers
	s.hub.Publish(hub.Event{
		Type:       "ingest",
		SourceID:   result.SourceID,
		Title:      result.Title,
		Collection: result.Collection,
	})
}

// ── helpers ───────────────────────────────────────────────────────────────────

// handleGraph returns a JSON graph of nodes and edges.
// GET /v1/graph?collection=<name>&root=<nodeID>&depth=<n>&max_nodes=<n>
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "GET only")
		return
	}

	opts := graph.BuildOptions{
		Collection: r.URL.Query().Get("collection"),
		RootID:     r.URL.Query().Get("root"),
	}
	if d := r.URL.Query().Get("depth"); d != "" {
		if n, err := strconv.Atoi(d); err == nil {
			opts.Depth = n
		}
	}
	if m := r.URL.Query().Get("max_nodes"); m != "" {
		if n, err := strconv.Atoi(m); err == nil {
			opts.MaxNodes = n
		}
	}

	g, err := graph.Build(s.db, opts)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	jsonOK(w, map[string]any{
		"nodes": g.Nodes,
		"edges": g.Edges,
		"meta": map[string]any{
			"node_count": len(g.Nodes),
			"edge_count": len(g.Edges),
			"collection": opts.Collection,
		},
	})
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// resultsToJSON converts hybrid.SearchResult slice to JSON-friendly maps,
// exposing all fields (including source_title) to the API and Web UI.
func resultsToJSON(results []hybrid.SearchResult) []map[string]any {
	out := make([]map[string]any, len(results))
	for i, r := range results {
		out[i] = map[string]any{
			"chunk_id":     r.ChunkID,
			"source_id":    r.SourceID,
			"collection":   r.Collection,
			"content":      r.Content,
			"source":       r.Source,
			"source_title": r.SourceTitle,
			"score":        r.Score,
		}
	}
	return out
}
