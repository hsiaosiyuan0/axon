package cmd

import (
	"fmt"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/relate"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var (
	relateAuto        bool
	relateUseLLM      bool
	relateThreshold   float64
	relateMaxPer      int
	relateDryRun      bool
	relateVerbose     bool
	relateSourceID    string
	relateMaxChunks   int
	relateNoResume    bool
)

var relateCmd = &cobra.Command{
	Use:   "relate [id-or-query]",
	Short: "Show or auto-discover relations between knowledge items",
	Long: `Show relations for a specific item, or auto-discover similar items via vector similarity or LLM extraction.

Examples:
  axon relate "golang channels"          # show relations for matched item
  axon relate --auto                     # auto-discover similar pairs (vector)
  axon relate --auto -c notes            # limit to a collection
  axon relate --auto --threshold 0.80    # lower threshold
  axon relate --auto --dry-run           # preview without saving
  axon relate --llm                      # LLM semantic triple extraction (requires AXON_LLM_API_KEY)
  axon relate --llm -c notes             # LLM extraction for one collection
  axon relate --llm --source <id>        # LLM extraction for one source
  axon relate --llm --max-chunks 5       # limit chunks per source (default 10)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		collection, _ := cmd.Flags().GetString("collection")

		// ── LLM extraction mode ──────────────────────────────────────────
		if relateUseLLM {
			fmt.Println("🤖 LLM relation extraction mode")
			if cfg.LLMAPIKey == "" {
				fmt.Println("⚠️  AXON_LLM_API_KEY not set.")
				fmt.Println("   Set it with: export AXON_LLM_API_KEY=sk-...")
				return fmt.Errorf("AXON_LLM_API_KEY required")
			}
			result, err := relate.ExtractWithLLM(cmd.Context(), cfg, relate.LLMOptions{
				Collection: collection,
				SourceID:   relateSourceID,
				MaxChunks:  relateMaxChunks,
				DryRun:     relateDryRun,
				Verbose:    relateVerbose,
				Resume:     !relateNoResume,
			})
			if err != nil {
				return err
			}
			fmt.Printf("\n📊 Sources: %d | Chunks: %d | Triples found: %d | Relations saved: %d\n",
				result.Sources, result.Chunks, result.Triples, result.Relations)
			if relateDryRun {
				fmt.Println("   (dry-run: nothing was saved)")
			}
			return nil
		}

		// ── auto-discovery mode (vector similarity) ──────────────────────
		if relateAuto {
			result, err := relate.AutoDiscover(cmd.Context(), cfg, relate.AutoOptions{
				Collection: collection,
				Threshold:  relateThreshold,
				MaxPerDoc:  relateMaxPer,
				DryRun:     relateDryRun,
			})
			if err != nil {
				return err
			}
			if relateDryRun {
				fmt.Printf("\n📊 Would create %d relation(s) (dry-run)\n", result.Created)
			} else {
				fmt.Printf("\n📊 Examined %d sources, created %d relation(s), skipped %d (already existed)\n",
					result.Examined, result.Created, result.Skipped)
			}
			return nil
		}

		// ── show relations for a specific item ───────────────────────────
		if len(args) == 0 {
			return fmt.Errorf("provide a query, --auto, or --llm flag")
		}

		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		query := strings.Join(args, " ")
		var chunkID, sourceID string

		if isUUID(query) {
			chunkID = query
		} else {
			searcher, err := hybrid.NewSearcher(cfg)
			if err != nil {
				return err
			}
			defer searcher.Close()

			results, err := searcher.Search(cmd.Context(), hybrid.SearchOptions{
				Query: query,
				Limit: 1,
			})
			if err != nil || len(results) == 0 {
				fmt.Println("❌ No matching knowledge found for:", query)
				return nil
			}
			chunkID = results[0].ChunkID
			sourceID = results[0].SourceID
			fmt.Printf("🔍 Matched: %s\n", results[0].Source)
			fmt.Printf("📄 Content: %s\n\n", truncateStr(results[0].Content, 200))
		}

		_ = sourceID

		chunk, err := db.Chunks().GetByID(chunkID)
		if err != nil {
			return fmt.Errorf("chunk not found: %w", err)
		}

		rels, err := db.Relations().ListByFrom(chunk.SourceID)
		if err != nil {
			return err
		}
		relsTo, err := db.Relations().ListByTo(chunk.SourceID)
		if err != nil {
			return err
		}
		rels = append(rels, relsTo...)

		if len(rels) == 0 {
			fmt.Println("🔗 No relations found for this item.")
			fmt.Println("   Run `axon relate --auto` to auto-discover similar items.")
			fmt.Println("   Run `axon relate --llm` to extract semantic relations via LLM.")
			return nil
		}

		fmt.Printf("🔗 Relations (%d):\n", len(rels))
		fmt.Println(strings.Repeat("─", 60))
		for _, r := range rels {
			direction := "→"
			otherID := r.ToID
			if r.ToID == chunk.SourceID {
				direction = "←"
				otherID = r.FromID
			}
			other, err := db.Sources().GetByID(otherID)
			otherLabel := otherID
			if err == nil {
				otherLabel = other.Origin
				if other.Title != "" {
					otherLabel = other.Title
				}
			}
			weight := ""
			if r.Weight > 0 && r.Weight != 1.0 {
				weight = fmt.Sprintf(" (%.3f)", r.Weight)
			}
			fmt.Printf("  [%s]%s %s %s\n", r.RelType, weight, direction, otherLabel)
			if r.Evidence != "" {
				fmt.Printf("       evidence: %s\n", truncate(r.Evidence, 100))
			}
		}
		return nil
	},
}

func init() {
	relateCmd.Flags().BoolVar(&relateNoResume, "no-resume", false, "Ignore existing checkpoint and start fresh")
	relateCmd.Flags().BoolVar(&relateAuto, "auto", false, "Auto-discover similar items via vector similarity")
	relateCmd.Flags().BoolVar(&relateUseLLM, "llm", false, "Extract semantic triples using LLM (requires AXON_LLM_API_KEY)")
	relateCmd.Flags().Float64Var(&relateThreshold, "threshold", 0.85, "Cosine similarity threshold for auto-discovery")
	relateCmd.Flags().IntVar(&relateMaxPer, "max-per-doc", 5, "Max relations to create per document")
	relateCmd.Flags().BoolVar(&relateDryRun, "dry-run", false, "Preview without saving")
	relateCmd.Flags().BoolVarP(&relateVerbose, "verbose", "v", false, "Show all extracted triples")
	relateCmd.Flags().StringVar(&relateSourceID, "source", "", "Process only this source (ID or origin path)")
	relateCmd.Flags().IntVar(&relateMaxChunks, "max-chunks", 10, "Max chunks to process per source (LLM mode)")
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	parts := strings.Split(s, "-")
	return len(parts) == 5
}

func truncateStr(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
