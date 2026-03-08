// Package integration provides end-to-end integration tests for Axon.
// Tests run against a temporary SQLite database and do NOT require network access
// or API keys — they use the built-in PureGo embedder.
package integration

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/store"
	axsync "github.com/hsiaosiyuan0/axon/internal/sync"
)

// newTestCfg creates a temporary config for tests.
func newTestCfg(t *testing.T) *config.Config {
	t.Helper()
	dir := t.TempDir()
	return &config.Config{
		DBPath:       filepath.Join(dir, "test.db"),
		ModelsDir:    filepath.Join(dir, "models"),
		PluginsDir:   filepath.Join(dir, "plugins"),
		DefaultModel: "purego",
		LLMEndpoint:  "https://api.openai.com/v1",
		LLMAPIKey:    "",
		LLMModel:     "gpt-4o-mini",
	}
}

// openTestStore opens a store and runs migrations.
func openTestStore(t *testing.T, cfg *config.Config) *store.DB {
	t.Helper()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestStoreCollectionCRUD tests basic collection CRUD operations.
func TestStoreCollectionCRUD(t *testing.T) {
	cfg := newTestCfg(t)
	db := openTestStore(t, cfg)

	// Create
	col, err := db.Collections().Create(store.CreateCollectionParams{
		Name:        "test-collection",
		Type:        "custom",
		Description: "Integration test collection",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}
	if col.ID == "" {
		t.Fatal("collection ID is empty")
	}
	if col.Name != "test-collection" {
		t.Errorf("expected name 'test-collection', got %q", col.Name)
	}

	// List
	cols, err := db.Collections().List()
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(cols) != 1 {
		t.Errorf("expected 1 collection, got %d", len(cols))
	}

	// Get by name
	found, err := db.Collections().Get("test-collection")
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if found.ID != col.ID {
		t.Errorf("ID mismatch: %q != %q", found.ID, col.ID)
	}

	// Get by ID
	byID, err := db.Collections().Get(col.ID)
	if err != nil {
		t.Fatalf("get by ID: %v", err)
	}
	if byID.Name != col.Name {
		t.Errorf("name mismatch: %q != %q", byID.Name, col.Name)
	}

	// Delete
	if err := db.Collections().Delete(col.ID); err != nil {
		t.Fatalf("delete collection: %v", err)
	}
	cols2, _ := db.Collections().List()
	if len(cols2) != 0 {
		t.Errorf("expected 0 collections after delete, got %d", len(cols2))
	}
}

// TestStoreSourceChunkCRUD tests source and chunk CRUD.
func TestStoreSourceChunkCRUD(t *testing.T) {
	cfg := newTestCfg(t)
	db := openTestStore(t, cfg)

	col, err := db.Collections().Create(store.CreateCollectionParams{
		Name: "notes", Type: "custom",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	// Create source
	src, err := db.Sources().Create(store.CreateSourceParams{
		Collection: col.ID,
		SourceType: "file",
		Origin:     "/tmp/test.md",
		PlainText:  "Hello world this is test content.",
		Title:      "Test Note",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if src.ID == "" {
		t.Fatal("source ID empty")
	}

	// Create chunks
	contents := []string{
		"Hello world this is chunk one.",
		"This is chunk two with different content.",
		"Third chunk about something completely different.",
	}
	for i, content := range contents {
		_, err := db.Chunks().Create(store.CreateChunkParams{
			SourceID:   src.ID,
			Collection: col.ID,
			Content:    content,
			Position:   i,
		})
		if err != nil {
			t.Fatalf("create chunk %d: %v", i, err)
		}
	}

	// BM25 search
	results, err := db.Chunks().BM25Search("chunk two", col.ID, 5)
	if err != nil {
		t.Fatalf("BM25 search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected BM25 results, got 0")
	}

	// GetByCollectionID
	chunks, err := db.Chunks().GetByCollectionID(col.ID)
	if err != nil {
		t.Fatalf("get chunks by collection: %v", err)
	}
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}

	// Delete source (cascade)
	if err := db.Sources().Delete(src.ID); err != nil {
		t.Fatalf("delete source: %v", err)
	}
	chunks2, _ := db.Chunks().GetByCollectionID(col.ID)
	if len(chunks2) != 0 {
		t.Errorf("expected 0 chunks after source delete, got %d", len(chunks2))
	}
}

// TestEmbedderPureGo verifies the PureGo embedder produces valid vectors.
func TestEmbedderPureGo(t *testing.T) {
	cfg := newTestCfg(t)

	embedder, err := embed.New("purego", cfg)
	if err != nil {
		t.Fatalf("create embedder: %v", err)
	}

	texts := []string{
		"The quick brown fox jumps over the lazy dog.",
		"机器学习是人工智能的一个分支。",
		"Golang is a compiled language with garbage collection.",
	}

	vecs, err := embedder.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(vecs))
	}
	for i, v := range vecs {
		if len(v) == 0 {
			t.Errorf("vector %d is empty", i)
		}
		var sum float64
		for _, x := range v {
			sum += float64(x) * float64(x)
		}
		if sum == 0 {
			t.Errorf("vector %d is all zeros", i)
		}
	}

	// Sanity: just log similarities
	sim := func(a, b []float32) float64 {
		var dot, na, nb float64
		for i := range a {
			dot += float64(a[i]) * float64(b[i])
			na += float64(a[i]) * float64(a[i])
			nb += float64(b[i]) * float64(b[i])
		}
		if na == 0 || nb == 0 {
			return 0
		}
		return dot / (math.Sqrt(na) * math.Sqrt(nb))
	}
	t.Logf("cos(fox, ML) = %.4f", sim(vecs[0], vecs[1]))
	t.Logf("cos(fox, golang) = %.4f", sim(vecs[0], vecs[2]))
}

// TestIngestAndSearch tests the full ingest → search pipeline.
func TestIngestAndSearch(t *testing.T) {
	cfg := newTestCfg(t)
	db := openTestStore(t, cfg)

	col, err := db.Collections().Create(store.CreateCollectionParams{
		Name: "test", Type: "custom",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	svc, err := ingest.NewService(cfg)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	defer svc.Close()

	ctx := context.Background()

	snippets := []struct{ text, title string }{
		{"Go channels provide safe communication between goroutines.", "goroutines-channels"},
		{"Python uses the GIL to manage memory safety in concurrent code.", "python-gil"},
		{"Rust ownership model eliminates data races at compile time.", "rust-ownership"},
	}
	for _, s := range snippets {
		_, err := svc.AddSnippet(ctx, ingest.AddSnippetOptions{
			Text: s.text, Title: s.title, Collection: col.ID,
		})
		if err != nil {
			t.Fatalf("add snippet %s: %v", s.title, err)
		}
	}

	searcher, err := hybrid.NewSearcher(cfg)
	if err != nil {
		t.Fatalf("new searcher: %v", err)
	}
	defer searcher.Close()

	results, err := searcher.Search(ctx, hybrid.SearchOptions{
		Query:      "goroutine channel",
		Collection: col.ID,
		Limit:      3,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results, got 0")
	}

	top := results[0].Content
	if len(top) > 60 {
		top = top[:60]
	}
	t.Logf("Top result: %s (score %.4f)", top, results[0].Score)
}

// TestHybridRerank verifies token-overlap reranking doesn't break results.
func TestHybridRerank(t *testing.T) {
	cfg := newTestCfg(t)
	db := openTestStore(t, cfg)

	col, err := db.Collections().Create(store.CreateCollectionParams{
		Name: "rerank-test", Type: "custom",
	})
	if err != nil {
		t.Fatalf("create collection: %v", err)
	}

	svc, err := ingest.NewService(cfg)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	defer svc.Close()

	ctx := context.Background()

	docs := []struct{ title, content string }{
		{"go-channels", "Go channels allow goroutines to communicate safely and efficiently."},
		{"python-async", "Python asyncio provides asynchronous I/O without threads."},
		{"rust-async", "Rust async/await enables zero-cost asynchronous programming."},
		{"go-goroutines", "Goroutines are lightweight threads managed by the Go runtime."},
		{"kafka-streams", "Apache Kafka Streams processes real-time data as continuous streams."},
	}
	for _, d := range docs {
		_, err := svc.AddSnippet(ctx, ingest.AddSnippetOptions{
			Text: d.content, Title: d.title, Collection: col.ID,
		})
		if err != nil {
			t.Fatalf("add snippet %s: %v", d.title, err)
		}
	}

	searcher, err := hybrid.NewSearcher(cfg)
	if err != nil {
		t.Fatalf("new searcher: %v", err)
	}
	defer searcher.Close()

	base, err := searcher.Search(ctx, hybrid.SearchOptions{
		Query: "go concurrency goroutine", Collection: col.ID, Limit: 3,
	})
	if err != nil {
		t.Fatalf("base search: %v", err)
	}

	reranked, err := searcher.Search(ctx, hybrid.SearchOptions{
		Query: "go concurrency goroutine", Collection: col.ID, Limit: 3,
		Rerank: true, RerankMode: "token",
	})
	if err != nil {
		t.Fatalf("rerank search: %v", err)
	}

	if len(reranked) == 0 {
		t.Error("reranked results empty")
	}

	snippet := func(s string) string {
		if len(s) > 45 {
			return s[:45] + "…"
		}
		return s
	}
	t.Logf("Base top  : %s", snippet(base[0].Content))
	t.Logf("Rerank top: %s", snippet(reranked[0].Content))
}

// TestSyncLocalBackend tests the local sync backend round-trip.
func TestSyncLocalBackend(t *testing.T) {
	cfg := newTestCfg(t)
	db := openTestStore(t, cfg)

	// Seed some data
	db.Collections().Create(store.CreateCollectionParams{Name: "sync-test", Type: "custom"}) //nolint

	syncDir := t.TempDir()
	backend := &axsync.LocalBackend{Dir: syncDir}

	ctx := context.Background()

	// Push
	result, err := axsync.Run(ctx, backend, axsync.SyncOptions{
		LocalPath:  cfg.DBPath,
		RemotePath: "axon/test.db",
		Direction:  "push",
		Verbose:    true,
	})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if result.Action != "uploaded" {
		t.Errorf("expected 'uploaded', got %q", result.Action)
	}
	if result.Bytes == 0 {
		t.Error("uploaded 0 bytes")
	}

	// Pull to a different path
	pullCfg := newTestCfg(t)
	result2, err := axsync.Run(ctx, backend, axsync.SyncOptions{
		LocalPath:  pullCfg.DBPath,
		RemotePath: "axon/test.db",
		Direction:  "pull",
		Verbose:    true,
	})
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if result2.Action != "downloaded" {
		t.Errorf("expected 'downloaded', got %q", result2.Action)
	}
	if result2.Bytes == 0 {
		t.Error("pulled 0 bytes")
	}

	// Auto sync — already in sync
	result3, err := axsync.Run(ctx, backend, axsync.SyncOptions{
		LocalPath:  cfg.DBPath,
		RemotePath: "axon/test.db",
		Direction:  "auto",
		Verbose:    true,
	})
	if err != nil {
		t.Fatalf("auto sync: %v", err)
	}
	if result3.Action != "already-in-sync" {
		t.Errorf("expected 'already-in-sync', got %q", result3.Action)
	}
}
