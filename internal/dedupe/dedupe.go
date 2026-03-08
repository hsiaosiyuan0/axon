// Package dedupe detects and optionally removes duplicate or near-duplicate
// sources in the Axon knowledge base.
//
// Duplicate detection strategies:
//  1. Exact hash match  — SHA256 of normalized plaintext (free, O(n))
//  2. Near-duplicate    — cosine similarity of mean source vector > threshold (O(n²))
//
// The command never deletes data without --confirm; by default it only reports.
package dedupe

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// DupeGroup holds a set of sources that appear to be duplicates.
type DupeGroup struct {
	Type     string         // "exact" | "near"
	Score    float64        // similarity (1.0 for exact)
	Sources  []store.Source // oldest first (keep sources[0], remove rest)
}

// Options controls dedupe behaviour.
type Options struct {
	Collection string  // "" = all
	Threshold  float64 // cosine similarity threshold for near-dupes (default 0.97)
	ExactOnly  bool    // only check exact hash matches (fast)
	DryRun     bool    // report without deleting
	Verbose    bool
}

// Result summarises what was found / removed.
type Result struct {
	Examined int
	Groups   []DupeGroup
	Removed  int
}

// Run detects (and optionally removes) duplicate sources.
func Run(ctx context.Context, cfg *config.Config, opts Options) (*Result, error) {
	if opts.Threshold == 0 {
		opts.Threshold = 0.97
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// Load sources
	var sources []store.Source
	if opts.Collection != "" {
		col, err := db.Collections().Get(opts.Collection)
		if err != nil {
			return nil, fmt.Errorf("collection %q not found: %w", opts.Collection, err)
		}
		sources, err = db.Sources().ListByCollection(col.ID)
	} else {
		sources, err = db.Sources().List()
	}
	if err != nil {
		return nil, err
	}

	res := &Result{Examined: len(sources)}

	// ── 1. Exact hash detection ───────────────────────────────────────────────
	hashGroups := exactDupes(db, sources, opts.Verbose)
	res.Groups = append(res.Groups, hashGroups...)

	// Build set of IDs already grouped (avoid double-reporting)
	grouped := make(map[string]bool)
	for _, g := range hashGroups {
		for _, s := range g.Sources {
			grouped[s.ID] = true
		}
	}

	// ── 2. Near-duplicate via vector centroid ─────────────────────────────────
	if !opts.ExactOnly {
		ungrouped := filterOut(sources, grouped)
		nearGroups, err := nearDupes(ctx, db, ungrouped, opts.Threshold, opts.Verbose)
		if err != nil {
			fmt.Printf("⚠️  Near-dupe detection skipped: %v\n", err)
		} else {
			res.Groups = append(res.Groups, nearGroups...)
		}
	}

	if len(res.Groups) == 0 {
		return res, nil
	}

	// ── 3. Remove duplicates ──────────────────────────────────────────────────
	if !opts.DryRun {
		for _, g := range res.Groups {
			// Keep first (oldest); remove the rest
			for _, s := range g.Sources[1:] {
				if err := db.Sources().Delete(s.ID); err != nil {
					fmt.Printf("  ⚠️  Failed to remove %s: %v\n", s.ID[:8], err)
				} else {
					res.Removed++
				}
			}
		}
	}

	return res, nil
}

// ── Exact duplicate detection ─────────────────────────────────────────────────

// normalizedHash computes a canonical hash of source content by loading all
// chunks, concatenating them, normalising whitespace.
func normalizedHash(db *store.DB, srcID string) string {
	chunks, err := db.Chunks().GetBySourceID(srcID)
	if err != nil || len(chunks) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(strings.Join(strings.Fields(c.Content), " "))
	}
	h := sha256.Sum256([]byte(sb.String()))
	return fmt.Sprintf("%x", h)
}

func exactDupes(db *store.DB, sources []store.Source, verbose bool) []DupeGroup {
	hashMap := make(map[string][]store.Source)
	for _, src := range sources {
		h := normalizedHash(db, src.ID)
		if h == "" {
			continue
		}
		hashMap[h] = append(hashMap[h], src)
	}

	var groups []DupeGroup
	for _, srcs := range hashMap {
		if len(srcs) < 2 {
			continue
		}
		// Sort oldest first
		sort.Slice(srcs, func(i, j int) bool {
			return srcs[i].CreatedAt.Before(srcs[j].CreatedAt)
		})
		if verbose {
			fmt.Printf("🔁 Exact dupe group (%d sources):\n", len(srcs))
			for i, s := range srcs {
				marker := "  keep"
				if i > 0 {
					marker = "  → rm"
				}
				fmt.Printf("   %s  %s  %s\n", marker, s.ID[:8], s.Origin)
			}
		}
		groups = append(groups, DupeGroup{Type: "exact", Score: 1.0, Sources: srcs})
	}
	return groups
}

// ── Near-duplicate detection ──────────────────────────────────────────────────

// sourceMeanVec computes the mean embedding vector for a source.
func sourceMeanVec(db *store.DB, srcID string) ([]float32, error) {
	chunks, err := db.Chunks().GetBySourceID(srcID)
	if err != nil || len(chunks) == 0 {
		return nil, fmt.Errorf("no chunks")
	}
	var mean []float32
	count := 0
	for _, c := range chunks {
		emb, err := db.Embeddings().GetByChunkID(c.ID)
		if err != nil || len(emb.Vector) == 0 {
			continue
		}
		if mean == nil {
			mean = make([]float32, len(emb.Vector))
		}
		for i, v := range emb.Vector {
			mean[i] += v
		}
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("no embeddings")
	}
	for i := range mean {
		mean[i] /= float32(count)
	}
	return mean, nil
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
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
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func nearDupes(ctx context.Context, db *store.DB, sources []store.Source, threshold float64, verbose bool) ([]DupeGroup, error) {
	// Load mean vectors for all sources
	type srcVec struct {
		src store.Source
		vec []float32
	}
	var vecs []srcVec
	for _, src := range sources {
		v, err := sourceMeanVec(db, src.ID)
		if err != nil {
			continue // no embeddings: skip
		}
		vecs = append(vecs, srcVec{src, v})
	}

	if len(vecs) < 2 {
		return nil, nil
	}

	// O(n²) pairwise similarity
	visited := make(map[int]bool)
	var groups []DupeGroup

	for i := 0; i < len(vecs); i++ {
		if visited[i] {
			continue
		}
		var group []store.Source
		group = append(group, vecs[i].src)
		maxSim := 0.0

		for j := i + 1; j < len(vecs); j++ {
			if visited[j] {
				continue
			}
			sim := cosine(vecs[i].vec, vecs[j].vec)
			if sim >= threshold {
				group = append(group, vecs[j].src)
				visited[j] = true
				if sim > maxSim {
					maxSim = sim
				}
			}
		}

		if len(group) > 1 {
			sort.Slice(group, func(a, b int) bool {
				return group[a].CreatedAt.Before(group[b].CreatedAt)
			})
			if verbose {
				fmt.Printf("〰️  Near-dupe group (sim=%.3f, %d sources):\n", maxSim, len(group))
				for k, s := range group {
					marker := "  keep"
					if k > 0 {
						marker = "  → rm"
					}
					fmt.Printf("   %s  %s  %s\n", marker, s.ID[:8], s.Origin)
				}
			}
			groups = append(groups, DupeGroup{Type: "near", Score: maxSim, Sources: group})
		}
	}
	return groups, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func filterOut(sources []store.Source, exclude map[string]bool) []store.Source {
	var out []store.Source
	for _, s := range sources {
		if !exclude[s.ID] {
			out = append(out, s)
		}
	}
	return out
}
