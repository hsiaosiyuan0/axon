package hybrid

import (
	"context"
	"sort"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/rerank"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// SearchOptions controls how search is performed.
type SearchOptions struct {
	Query      string
	Collection string
	Limit      int
	Rerank     bool   // enable two-stage reranking (default: false)
	RerankMode string // "token" (default, fast) or "llm" (slow, high quality)
}

// SearchResult is a ranked result returned to the user.
type SearchResult struct {
	ChunkID     string
	SourceID    string
	Collection  string
	Content     string
	Source      string  // formatted "title (origin)"
	SourceTitle string  // raw title only
	Score       float64
}

// Searcher performs hybrid BM25 + vector search with RRF merging.
type Searcher struct {
	cfg *config.Config
	db  *store.DB
}

func NewSearcher(cfg *config.Config) (*Searcher, error) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	return &Searcher{cfg: cfg, db: db}, nil
}

func (s *Searcher) Close() error { return s.db.Close() }

func (s *Searcher) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.Limit == 0 {
		opts.Limit = 5
	}
	// Fetch more candidates for reranking
	candidateSize := opts.Limit * 4
	if opts.Rerank {
		candidateSize = opts.Limit * 8
	}

	// BM25 search (always available)
	bm25Results, err := s.db.Chunks().BM25Search(opts.Query, opts.Collection, candidateSize)
	if err != nil {
		bm25Results = nil // non-fatal
	}

	// Vector search (best-effort)
	var vecResults []store.SearchResult
	embedder, err := embed.New(s.cfg.DefaultModel, s.cfg)
	if err == nil {
		vecs, err := embedder.Embed(ctx, []string{opts.Query})
		if err == nil && len(vecs) > 0 {
			vecResults, _ = s.db.Embeddings().VectorSearch(
				vecs[0], embedder.ModelName(), opts.Collection, candidateSize)
		}
	}

	// RRF merge
	merged := rrf(bm25Results, vecResults, candidateSize)

	// Enrich with source info
	enriched := s.enrich(merged)

	// Two-stage reranking
	if opts.Rerank && len(enriched) > 1 {
		candidates := make([]rerank.Candidate, len(enriched))
		for i, r := range enriched {
			candidates[i] = rerank.Candidate{
				ID:      r.ChunkID,
				Content: r.Content,
				Source:  r.Source,
				Score:   r.Score,
			}
		}

		var ranker rerank.Reranker
		if opts.RerankMode == "llm" {
			ranker = rerank.NewLLMReranker(s.cfg)
		} else {
			ranker = rerank.NewTokenOverlap()
		}

		reranked, err := ranker.Rerank(ctx, opts.Query, candidates)
		if err == nil {
			// Map back
			byID := make(map[string]SearchResult, len(enriched))
			for _, r := range enriched {
				byID[r.ChunkID] = r
			}
			enriched = make([]SearchResult, 0, len(reranked))
			for _, c := range reranked {
				if r, ok := byID[c.ID]; ok {
					r.Score = c.Score
					enriched = append(enriched, r)
				}
			}
		}
	}

	// Trim to requested limit
	if len(enriched) > opts.Limit {
		enriched = enriched[:opts.Limit]
	}
	return enriched, nil
}

// rrf implements Reciprocal Rank Fusion.
// https://plg.uwaterloo.ca/~gvcormac/cormacksigir09-rrf.pdf
func rrf(bm25, vec []store.SearchResult, limit int) []store.SearchResult {
	const k = 60.0
	scores := make(map[string]float64)
	chunks := make(map[string]store.SearchResult)

	for rank, r := range bm25 {
		scores[r.ChunkID] += 1.0 / (k + float64(rank+1))
		chunks[r.ChunkID] = r
	}
	for rank, r := range vec {
		scores[r.ChunkID] += 1.0 / (k + float64(rank+1))
		if _, ok := chunks[r.ChunkID]; !ok {
			chunks[r.ChunkID] = r
		}
	}

	type scored struct {
		id    string
		score float64
	}
	var list []scored
	for id, score := range scores {
		list = append(list, scored{id, score})
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].score > list[j].score
	})

	var result []store.SearchResult
	for i, s := range list {
		if i >= limit {
			break
		}
		r := chunks[s.id]
		r.Score = s.score
		result = append(result, r)
	}
	return result
}

func (s *Searcher) enrich(results []store.SearchResult) []SearchResult {
	if len(results) == 0 {
		return nil
	}

	// Collect unique source IDs in insertion order.
	seen := make(map[string]struct{}, len(results))
	var srcIDs []string
	for _, r := range results {
		if _, ok := seen[r.SourceID]; !ok {
			seen[r.SourceID] = struct{}{}
			srcIDs = append(srcIDs, r.SourceID)
		}
	}

	type sourceInfo struct {
		label string
		title string
	}
	infoMap := make(map[string]sourceInfo, len(srcIDs))

	// Single batch query: WHERE id IN (...)
	sources, err := s.db.Sources().GetByIDs(srcIDs)
	if err == nil {
		for _, src := range sources {
			label := src.Origin
			if label == "" {
				label = src.ID
			}
			title := src.Title
			if title != "" {
				label = title + " (" + src.Origin + ")"
			}
			infoMap[src.ID] = sourceInfo{label: label, title: title}
		}
	}
	// Fallback: any IDs not returned get the raw ID as label.
	for _, id := range srcIDs {
		if _, ok := infoMap[id]; !ok {
			infoMap[id] = sourceInfo{label: id}
		}
	}

	// Build collection ID → name map
	colNames := map[string]string{}
	if cols, colErr := s.db.Collections().List(); colErr == nil {
		for _, c := range cols {
			colNames[c.ID] = c.Name
		}
	}

	out := make([]SearchResult, len(results))
	for i, r := range results {
		info := infoMap[r.SourceID]
		colDisplay := colNames[r.Collection]
		if colDisplay == "" {
			colDisplay = r.Collection
		}
		out[i] = SearchResult{
			ChunkID:     r.ChunkID,
			SourceID:    r.SourceID,
			Collection:  colDisplay,
			Content:     r.Content,
			Source:      info.label,
			SourceTitle: info.title,
			Score:       r.Score,
		}
	}
	return out
}
