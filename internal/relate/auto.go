// Package relate provides automatic relation discovery via vector similarity.
package relate

import (
	"context"
	"fmt"
	"math"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// AutoOptions controls how automatic relation discovery works.
type AutoOptions struct {
	Collection string  // optional: limit to a collection
	Threshold  float64 // cosine similarity threshold (default 0.85)
	MaxPerDoc  int     // max relations per source document (default 5)
	DryRun     bool    // if true, print but don't save
}

// AutoResult summarizes what was found/created.
type AutoResult struct {
	Examined int
	Created  int
	Skipped  int // already existed
}

// AutoDiscover scans all sources in the DB, computes pairwise cosine similarity
// between their representative embeddings, and creates "similar" relations for
// pairs that exceed the threshold.
func AutoDiscover(ctx context.Context, cfg *config.Config, opts AutoOptions) (*AutoResult, error) {
	if opts.Threshold == 0 {
		opts.Threshold = 0.85
	}
	if opts.MaxPerDoc == 0 {
		opts.MaxPerDoc = 5
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// 1. Determine which model to use
	modelName := cfg.DefaultModel

	// 2. Load all sources (optionally filtered by collection)
	sources, err := loadSources(db, opts.Collection)
	if err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}
	if len(sources) < 2 {
		return &AutoResult{Examined: len(sources)}, nil
	}

	fmt.Printf("🔍 Examining %d sources for similar relations (threshold=%.2f)…\n",
		len(sources), opts.Threshold)

	// 3. Build representative vector per source (average of first chunk's embedding)
	type srcVec struct {
		src    store.Source
		vector []float32
	}

	embedder, err := embed.New(modelName, cfg)
	if err != nil {
		return nil, fmt.Errorf("embedder: %w", err)
	}

	var items []srcVec
	for _, src := range sources {
		// Get first chunk
		chunks, err := db.Chunks().GetBySourceID(src.ID)
		if err != nil || len(chunks) == 0 {
			continue
		}

		// Average embeddings of first up to 3 chunks as representative vector
		limit := 3
		if len(chunks) < limit {
			limit = len(chunks)
		}
		texts := make([]string, limit)
		for i := 0; i < limit; i++ {
			texts[i] = chunks[i].Content
		}
		vecs, err := embedder.Embed(ctx, texts)
		if err != nil {
			fmt.Printf("  ⚠️  Skip %s: embed error: %v\n", src.Title, err)
			continue
		}
		avg := averageVectors(vecs)
		items = append(items, srcVec{src: src, vector: avg})
	}

	result := &AutoResult{Examined: len(items)}

	// 4. Pairwise similarity — O(n²) but fine for personal KB (< 10k docs)
	for i := 0; i < len(items); i++ {
		count := 0
		for j := i + 1; j < len(items); j++ {
			sim := cosineSim(items[i].vector, items[j].vector)
			if sim < opts.Threshold {
				continue
			}
			if count >= opts.MaxPerDoc {
				break
			}

			srcA := items[i].src
			srcB := items[j].src

			labelA := labelFor(srcA)
			labelB := labelFor(srcB)

			if opts.DryRun {
				fmt.Printf("  [dry-run] %.3f  %s  ↔  %s\n", sim, labelA, labelB)
				result.Created++
				count++
				continue
			}

			// Check if relation already exists
			existing, _ := db.Relations().ListByFrom(srcA.ID)
			alreadyExists := false
			for _, r := range existing {
				if r.ToID == srcB.ID && r.RelType == "similar" {
					alreadyExists = true
					break
				}
			}
			if alreadyExists {
				result.Skipped++
				continue
			}

			_, err := db.Relations().Create(store.CreateRelationParams{
				FromType:       "source",
				FromID:         srcA.ID,
				FromCollection: srcA.Collection,
				ToType:         "source",
				ToID:           srcB.ID,
				ToCollection:   srcB.Collection,
				RelType:        "similar",
				Weight:         sim,
				Bidirectional:  true,
				EstablishedBy:  "vector-similarity",
				Evidence:       fmt.Sprintf("cosine=%.4f", sim),
			})
			if err != nil {
				fmt.Printf("  ⚠️  Failed to create relation: %v\n", err)
				continue
			}
			fmt.Printf("  ✅ %.3f  %s  ↔  %s\n", sim, labelA, labelB)
			result.Created++
			count++
		}
	}

	return result, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func loadSources(db *store.DB, collection string) ([]store.Source, error) {
	if collection != "" {
		col, err := db.Collections().Get(collection)
		if err != nil {
			return nil, fmt.Errorf("collection %q not found", collection)
		}
		return db.Sources().ListByCollection(col.ID)
	}
	return db.Sources().List()
}

func labelFor(src store.Source) string {
	if src.Title != "" {
		return src.Title
	}
	if len(src.Origin) > 60 {
		return "…" + src.Origin[len(src.Origin)-57:]
	}
	return src.Origin
}

func averageVectors(vecs [][]float32) []float32 {
	if len(vecs) == 0 {
		return nil
	}
	dim := len(vecs[0])
	avg := make([]float32, dim)
	for _, v := range vecs {
		for i, x := range v {
			avg[i] += x
		}
	}
	n := float32(len(vecs))
	for i := range avg {
		avg[i] /= n
	}
	return avg
}

func cosineSim(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}
