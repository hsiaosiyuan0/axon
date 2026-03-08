package ingest

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/chunk"
	"github.com/hsiaosiyuan0/axon/internal/classify"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/modelreg"
	"github.com/hsiaosiyuan0/axon/internal/obsidian"
	"github.com/hsiaosiyuan0/axon/internal/plugin"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// Service handles ingesting new sources into the knowledge base.
type Service struct {
	cfg     *config.Config
	db      *store.DB
	plugins *plugin.Registry
	// ConfirmFn is called before auto-classifying a document into a collection.
	// It receives the suggested collection name (or ID) and a flag indicating
	// whether it is a new collection. It should return (true, nil) to accept,
	// (false, nil) to abort, or (false, err) on I/O error.
	// If nil, classification proceeds without confirmation (non-interactive mode).
	ConfirmFn func(collectionName string, isNew bool) (bool, error)
}

// AddOptions controls how a source is added.
type AddOptions struct {
	Origin      string
	Collection  string             // ID or name; empty = auto-classify
	SnippetData *plugin.SourceData // if set, skip fetch and use this data directly
}

// AddResult summarizes what was added.
type AddResult struct {
	SourceID      string
	Title         string
	Collection    string
	ChunkCount    int
	RelationCount int
	TopChunks     []string // first up to 3 chunk previews (50 chars each)
}

func NewService(cfg *config.Config) (*Service, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		return nil, err
	}
	return &Service{
		cfg:     cfg,
		db:      db,
		plugins: plugin.NewRegistry(),
	}, nil
}

func (s *Service) Close() error { return s.db.Close() }

func (s *Service) Add(ctx context.Context, opts AddOptions) (*AddResult, error) {
	// 1. Detect plugin and fetch source
	sourceType := plugin.DetectSourceType(opts.Origin)
	p, err := s.plugins.Get(sourceType)
	if err != nil {
		return nil, err
	}

	var data *plugin.SourceData
	if opts.SnippetData != nil {
		data = opts.SnippetData
		sourceType = "snippet"
	} else {
		fmt.Printf("📥 Fetching: %s\n", opts.Origin)
		data, err = p.Fetch(ctx, opts.Origin, nil)
		if err != nil {
			return nil, fmt.Errorf("fetch: %w", err)
		}
	}

	// 2. Check if already exists (update path)
	existing, _ := s.db.Sources().GetByOrigin(opts.Origin)
	if existing != nil {
		hash := plugin.ContentHash(data.RawContent)
		if existing.OriginHash == hash {
			fmt.Println("ℹ️  No changes detected, skipping.")
			return &AddResult{
				SourceID:   existing.ID,
				Title:      existing.Title,
				Collection: existing.Collection,
			}, nil
		}
		// Content changed: delete old source (cascades to chunks, embeddings, relations)
		// so we can re-ingest cleanly without duplicate records.
		fmt.Printf("🔄 Content changed, re-ingesting: %s\n", opts.Origin)
		if err := s.db.Sources().Delete(existing.ID); err != nil {
			return nil, fmt.Errorf("remove stale source: %w", err)
		}
	}

	// 3. Resolve collection
	collectionID, err := s.resolveCollection(ctx, opts.Collection, data, opts.Origin)
	if err != nil {
		return nil, fmt.Errorf("resolve collection: %w", err)
	}

	col, err := s.db.Collections().Get(collectionID)
	if err != nil {
		return nil, fmt.Errorf("get collection: %w", err)
	}

	// 4. Save source (raw data preserved forever)
	src, err := s.db.Sources().Create(store.CreateSourceParams{
		Collection: collectionID,
		SourceType: sourceType,
		Origin:     opts.Origin,
		OriginHash: plugin.ContentHash(data.RawContent),
		RawContent: data.RawContent,
		RawMime:    data.RawMime,
		PlainText:  data.PlainText,
		Title:      data.Title,
		Lang:       data.Lang,
		Meta:       data.Meta,
	})
	if err != nil {
		return nil, fmt.Errorf("save source: %w", err)
	}

	// 5. Chunk the content
	// PDF sources always use paragraph chunker regardless of collection strategy
	chunkStrategy := chunk.Strategy(col.ChunkStrategy)
	if sourceType == "pdf" {
		chunkStrategy = chunk.StrategyParagraph
	}
	chunker := chunk.New(chunkStrategy)
	chunks, err := chunker.Chunk(data.PlainText)
	if err != nil {
		return nil, fmt.Errorf("chunk: %w", err)
	}

	fmt.Printf("✂️  Split into %d chunks\n", len(chunks))

	// 6. Save chunks
	var chunkParams []store.CreateChunkParams
	for _, c := range chunks {
		chunkParams = append(chunkParams, store.CreateChunkParams{
			SourceID:   src.ID,
			Collection: collectionID,
			Content:    c.Content,
			Position:   c.Position,
			CharStart:  c.CharStart,
			CharEnd:    c.CharEnd,
			Section:    c.Section,
		})
	}
	savedChunks, err := s.db.Chunks().BatchCreate(chunkParams)
	if err != nil {
		return nil, fmt.Errorf("save chunks: %w", err)
	}

	// 7. Embed chunks
	if err := s.embedChunks(ctx, savedChunks, col.ModelName); err != nil {
		fmt.Printf("⚠️  Embedding failed: %v\n", err)
		// Non-fatal: BM25 still works
	}

	// 8. Extract and save relations
	hints, _ := p.ExtractRelations(data.PlainText)

	// Extra: Obsidian wikilink parsing for markdown files
	if strings.HasSuffix(strings.ToLower(opts.Origin), ".md") {
		obsHints := extractObsidianRelations(opts.Origin, data.PlainText)
		hints = append(hints, obsHints...)
	}

	relCount := s.saveRelations(src, hints)

	// Build top chunk previews (first 3, trimmed to 60 chars)
	var topChunks []string
	limit := 3
	if len(chunks) < limit {
		limit = len(chunks)
	}
	for i := 0; i < limit; i++ {
		preview := strings.TrimSpace(chunks[i].Content)
		if len([]rune(preview)) > 60 {
			runes := []rune(preview)
			preview = string(runes[:60]) + "…"
		}
		topChunks = append(topChunks, preview)
	}

	return &AddResult{
		SourceID:      src.ID,
		Title:         src.Title,
		Collection:    col.Name,
		ChunkCount:    len(savedChunks),
		RelationCount: relCount,
		TopChunks:     topChunks,
	}, nil
}

func (s *Service) embedChunks(ctx context.Context, chunks []store.Chunk, modelName string) error {
	if modelName == "" {
		modelName = s.cfg.DefaultModel
	}

	embedder, err := embed.New(modelName, s.cfg)
	if err != nil {
		return err
	}

	// Batch embed
	batchSize := 32
	for i := 0; i < len(chunks); i += batchSize {
		end := i + batchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		batch := chunks[i:end]

		texts := make([]string, len(batch))
		for j, c := range batch {
			texts[j] = c.Content
		}

		vectors, err := embedder.Embed(ctx, texts)
		if err != nil {
			return err
		}
		// Guard: API must return exactly one vector per input text.
		if len(vectors) != len(batch) {
			return fmt.Errorf("embedder returned %d vectors for %d texts", len(vectors), len(batch))
		}

		for j, vec := range vectors {
			if err := s.db.Embeddings().Upsert(
				batch[j].ID,
				embedder.ModelName(),
				embedder.Provider(),
				vec,
			); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) saveRelations(src *store.Source, hints []plugin.RelationHint) int {
	count := 0

	// First resolve any pending wikilinks that point to this newly-added source
	_ = s.db.Relations().ResolvePendingWikilinks(src)

	for _, hint := range hints {
		// Try to find the target source by origin
		target, err := s.db.Sources().GetByOrigin(hint.ToOrigin)
		if err != nil {
			if err == sql.ErrNoRows {
				// Target not yet ingested → save as pending wikilink
				if hint.RelType == "wikilink" || hint.RelType == "cite" || hint.RelType == "ref" {
					_, _ = s.db.Relations().Create(store.CreateRelationParams{
						FromType:       "source",
						FromID:         src.ID,
						FromCollection: src.Collection,
						ToType:         "source",
						ToID:           "", // pending
						ToCollection:   src.Collection,
						ToOrigin:       hint.ToOrigin,
						RelType:        hint.RelType,
						EstablishedBy:  "parser",
						Evidence:       hint.Evidence,
					})
					count++
				}
				continue
			}
			continue
		}
		_, err = s.db.Relations().Create(store.CreateRelationParams{
			FromType:       "source",
			FromID:         src.ID,
			FromCollection: src.Collection,
			ToType:         "source",
			ToID:           target.ID,
			ToCollection:   target.Collection,
			ToOrigin:       hint.ToOrigin,
			RelType:        hint.RelType,
			EstablishedBy:  "parser",
			Evidence:       hint.Evidence,
		})
		if err == nil {
			count++
		}
	}
	return count
}

// extractObsidianRelations parses a Markdown file with the Obsidian parser
// and returns RelationHints for all [[wikilinks]].
func extractObsidianRelations(path, content string) []plugin.RelationHint {
	note := obsidian.Parse(path, content)
	var hints []plugin.RelationHint
	for _, link := range note.Links {
		target := link.Target
		// Convert note name to likely file origin (without vault path — resolved later)
		if !strings.HasSuffix(target, ".md") {
			target = target + ".md"
		}
		relType := "wikilink"
		if link.IsEmbed {
			relType = "embed"
		}
		hints = append(hints, plugin.RelationHint{
			ToOrigin: target,
			RelType:  relType,
			Evidence: link.Raw,
		})
	}
	return hints
}

func (s *Service) resolveCollection(ctx context.Context, collectionHint string, data *plugin.SourceData, origin string) (string, error) {
	// If explicit collection given, use it
	if collectionHint != "" {
		col, err := s.db.Collections().Get(collectionHint)
		if err != nil {
			return "", fmt.Errorf("collection %q not found", collectionHint)
		}
		return col.ID, nil
	}

	// List available collections
	cols, err := s.db.Collections().List()
	if err != nil {
		return "", err
	}

	// No collections yet: create default
	if len(cols) == 0 {
		col, err := s.db.Collections().Create(store.CreateCollectionParams{
			Name:        "default",
			Type:        "custom",
			Description: "Default collection",
		})
		if err != nil {
			return "", err
		}
		fmt.Printf("📁 Created default collection\n")
		return col.ID, nil
	}

	// Only one collection: use it
	if len(cols) == 1 {
		return cols[0].ID, nil
	}

	// ── Provider-specific startup checks ─────────────────────────────────────
	provider := s.cfg.ClassifyProvider
	if provider == "" {
		provider = "llm"
	}

	switch provider {
	case "bge-cosine":
		// Warn the user about accuracy on first classification attempt
		fmt.Println()
		fmt.Println("⚠️  Collection auto-classification is set to: bge-cosine (local embedding)")
		fmt.Println("   Estimated accuracy: ~65%  (roughly 65 correct out of every 100 documents)")
		fmt.Println("   This means about 1 in 3 documents may be placed in the wrong collection.")
		fmt.Println("   For better results, set classify.provider = \"nli\" or \"llm\" in your config.")
		fmt.Println()

	case "nli":
		// Ensure the NLI model is downloaded
		if err := s.ensureNLIModel(); err != nil {
			return "", fmt.Errorf("NLI model setup: %w", err)
		}

	case "llm":
		// Require API key — no fallback
		if s.cfg.LLMAPIKey == "" {
			return "", fmt.Errorf(
				"collection classification is set to \"llm\" but no API key is configured.\n" +
					"  Set your key:  axon config set llm.key <your-key>\n" +
					"  Or switch to a local classifier:\n" +
					"    axon config set classify.provider nli         # ~80%% accuracy, downloads ~44 MB model\n" +
					"    axon config set classify.provider bge-cosine  # ~65%% accuracy, uses built-in model\n" +
					"  Or specify a collection manually: axon add -c <collection> <file>",
			)
		}
	}

	// ── Run classification ────────────────────────────────────────────────────
	classResult, err := classify.Classify(ctx, s.cfg, classify.ClassifyInput{
		Title:     data.Title,
		PlainText: data.PlainText,
		Origin:    origin,
	}, cols)
	if err != nil {
		// Classification failed — fall back gracefully only for non-LLM providers
		if provider == "llm" {
			return "", fmt.Errorf("LLM classification failed: %w\n"+
				"  Use -c to specify a collection manually, or check your API key", err)
		}
		fmt.Printf("⚠️  %s classification failed (%v), using first collection\n", provider, err)
		return cols[0].ID, nil
	}

	// ── User confirmation ─────────────────────────────────────────────────────
	if classResult.SuggestNew {
		suggestedName := classResult.SuggestedName
		confirmed, err := s.askConfirm(suggestedName, true)
		if err != nil {
			return "", err
		}
		if !confirmed {
			return "", fmt.Errorf("classification cancelled — use -c to specify a collection manually")
		}
		newCol, createErr := s.db.Collections().Create(store.CreateCollectionParams{
			Name:        suggestedName,
			Type:        "custom",
			Description: "Auto-created by " + provider + " classification",
		})
		if createErr != nil {
			return "", fmt.Errorf("create collection %q: %w", suggestedName, createErr)
		}
		fmt.Printf("📁 Created new collection: %s\n", suggestedName)
		return newCol.ID, nil
	}

	// Existing collection: find its name for confirmation
	var matchedCol store.Collection
	for _, c := range cols {
		if c.ID == classResult.CollectionID {
			matchedCol = c
			break
		}
	}

	confirmed, err := s.askConfirm(matchedCol.Name, false)
	if err != nil {
		return "", err
	}
	if !confirmed {
		return "", fmt.Errorf("classification cancelled — use -c to specify a collection manually")
	}

	return classResult.CollectionID, nil
}

// ensureNLIModel checks if the NLI model is on disk and downloads it if not.
func (s *Service) ensureNLIModel() error {
	spec := modelreg.NLIModel()
	if spec == nil {
		return fmt.Errorf("NLI model not found in registry")
	}

	modelPath := filepath.Join(s.cfg.ModelsDir, spec.Name, "model.onnx")
	if fi, err := os.Stat(modelPath); err == nil && fi.Size() > 1024*1024 {
		return nil // already present
	}

	fmt.Printf("⬇️  NLI model not found locally. Downloading %q (~%d MB)…\n", spec.Name, spec.SizeMB)
	fmt.Println("   This is a one-time download required for local collection classification.")
	fmt.Println("   Tip: use --mirror hf-mirror for faster downloads in mainland China")
	fmt.Println()

	opts := modelreg.DownloadOptions{}
	_, err := modelreg.DownloadModel(spec, s.cfg.ModelsDir, opts)
	if err != nil {
		return fmt.Errorf("download NLI model: %w", err)
	}
	fmt.Printf("✅ NLI model %q ready\n\n", spec.Name)
	return nil
}

// askConfirm calls ConfirmFn if set, otherwise prompts via stdin.
func (s *Service) askConfirm(collectionName string, isNew bool) (bool, error) {
	if s.ConfirmFn != nil {
		return s.ConfirmFn(collectionName, isNew)
	}
	// Default: interactive stdin prompt
	return stdinConfirm(collectionName, isNew)
}

// stdinConfirm prints a [y/N] prompt and reads a single line from stdin.
func stdinConfirm(collectionName string, isNew bool) (bool, error) {
	if isNew {
		fmt.Printf("🤖 Suggested new collection: %q\n", collectionName)
		fmt.Print("   Create this collection and add the document? [y/N] ")
	} else {
		fmt.Printf("🤖 Suggested collection: %q\n", collectionName)
		fmt.Print("   Add document to this collection? [y/N] ")
	}

	reader := bufio.NewReader(os.Stdin)
	answer, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}

// AddSnippetOptions controls snippet ingestion.
type AddSnippetOptions struct {
	Text       string
	Title      string
	Collection string
}

// AddSnippet adds a raw text snippet to the knowledge base (used by MCP memory_add).
func (s *Service) AddSnippet(ctx context.Context, opts AddSnippetOptions) (*AddResult, error) {
	title := opts.Title
	if title == "" {
		title = "snippet"
	}
	return s.Add(ctx, AddOptions{
		Origin:     "snippet:" + title,
		Collection: opts.Collection,
		SnippetData: &plugin.SourceData{
			RawContent: []byte(opts.Text),
			RawMime:    "text/plain",
			PlainText:  opts.Text,
			Title:      title,
		},
	})
}

// AddWithData ingests a source using pre-fetched SourceData (e.g. from the Notion parser).
// This skips the plugin fetch step and uses the provided data directly.
func (s *Service) AddWithData(ctx context.Context, opts AddOptions, data *plugin.SourceData) (*AddResult, error) {
	return s.Add(ctx, AddOptions{
		Origin:      opts.Origin,
		Collection:  opts.Collection,
		SnippetData: data,
	})
}

// Remove deletes a source (and all its chunks/embeddings/relations) by origin path.
// Used by watch mode when a file is deleted.
func (s *Service) Remove(origin string) error {
	src, err := s.db.Sources().GetByOrigin(origin)
	if err != nil {
		return nil // not in DB, nothing to do
	}
	return s.db.Sources().Delete(src.ID)
}
