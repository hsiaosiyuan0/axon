package cmd

import (
	"fmt"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/dedupe"
	"github.com/spf13/cobra"
)

var (
	dedupeCollection string
	dedupeThreshold  float64
	dedupeExactOnly  bool
	dedupeDryRun     bool
	dedupeVerbose    bool
	dedupeConfirm    bool
)

var dedupeCmd = &cobra.Command{
	Use:   "dedupe",
	Short: "Detect and remove duplicate sources in the knowledge base",
	Long: `Scan the knowledge base for duplicate or near-duplicate sources.

Detection strategies:
  1. Exact match  — SHA256 hash of normalised content (always on, O(n))
  2. Near-dupe    — cosine similarity of mean source embedding > threshold
                    (disabled with --exact-only, requires embeddings)

By default runs in dry-run mode (safe): prints duplicates without removing.
Use --confirm to actually delete the duplicates (keeps the oldest copy).

Examples:
  axon dedupe                          # dry-run: report all duplicates
  axon dedupe -v                       # verbose: show each dupe group
  axon dedupe -c notes                 # limit to a collection
  axon dedupe --exact-only             # fast: only exact hash matches
  axon dedupe --threshold 0.95         # lower similarity threshold
  axon dedupe --confirm                # actually remove duplicates
  axon dedupe --confirm --dry-run      # (--confirm is ignored in dry-run)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		fmt.Println("🔍 Scanning for duplicates…")
		if dedupeExactOnly {
			fmt.Println("   Mode: exact hash match only")
		} else {
			fmt.Printf("   Mode: exact + near-duplicate (similarity ≥ %.2f)\n", dedupeThreshold)
		}

		result, err := dedupe.Run(cmd.Context(), cfg, dedupe.Options{
			Collection: dedupeCollection,
			Threshold:  dedupeThreshold,
			ExactOnly:  dedupeExactOnly,
			DryRun:     dedupeDryRun || !dedupeConfirm,
			Verbose:    dedupeVerbose,
		})
		if err != nil {
			return err
		}

		fmt.Printf("\n📊 Examined: %d sources\n", result.Examined)

		if len(result.Groups) == 0 {
			fmt.Println("✅ No duplicates found!")
			return nil
		}

		// Print summary table
		exactCount, nearCount := 0, 0
		totalDupeInstances := 0
		for _, g := range result.Groups {
			if g.Type == "exact" {
				exactCount++
			} else {
				nearCount++
			}
			totalDupeInstances += len(g.Sources) - 1 // -1 for the kept copy
		}

		fmt.Printf("⚠️  Found %d dupe group(s):\n", len(result.Groups))
		if exactCount > 0 {
			fmt.Printf("   🔁 Exact:      %d group(s)\n", exactCount)
		}
		if nearCount > 0 {
			fmt.Printf("   〰️  Near-dupe: %d group(s)\n", nearCount)
		}
		fmt.Printf("   Total duplicate instances: %d\n\n", totalDupeInstances)

		// Print details (condensed if not verbose)
		if !dedupeVerbose {
			for i, g := range result.Groups {
				typeLabel := "exact"
				if g.Type == "near" {
					typeLabel = fmt.Sprintf("near %.3f", g.Score)
				}
				fmt.Printf("  [%d] %-12s keep: %s  (+ %d dupe(s))\n",
					i+1, typeLabel,
					truncateOrigin(result.Groups[i].Sources[0].Origin),
					len(g.Sources)-1,
				)
				for _, s := range g.Sources[1:] {
					fmt.Printf("         rm:   %s\n", truncateOrigin(s.Origin))
				}
			}
		}

		// Action summary
		if dedupeDryRun || !dedupeConfirm {
			fmt.Println()
			fmt.Printf("💡 Dry-run: %d source(s) would be removed.\n", totalDupeInstances)
			fmt.Println("   Run with --confirm to permanently delete duplicates.")
		} else {
			fmt.Printf("\n🗑️  Removed: %d duplicate source(s)\n", result.Removed)
			fmt.Println("✅ Deduplication complete.")
		}

		return nil
	},
}

func init() {
	dedupeCmd.Flags().StringVarP(&dedupeCollection, "collection", "c", "", "Limit to a collection")
	dedupeCmd.Flags().Float64Var(&dedupeThreshold, "threshold", 0.97, "Cosine similarity threshold for near-duplicate detection")
	dedupeCmd.Flags().BoolVar(&dedupeExactOnly, "exact-only", false, "Only detect exact hash duplicates (fast, no embeddings needed)")
	dedupeCmd.Flags().BoolVar(&dedupeDryRun, "dry-run", false, "Report without removing (default when --confirm not set)")
	dedupeCmd.Flags().BoolVarP(&dedupeVerbose, "verbose", "v", false, "Print detailed group info")
	dedupeCmd.Flags().BoolVar(&dedupeConfirm, "confirm", false, "Actually remove duplicate sources (keeps oldest copy)")
}

func truncateOrigin(s string) string {
	if strings.HasPrefix(s, "http") {
		if len(s) > 60 {
			return s[:57] + "…"
		}
		return s
	}
	parts := strings.Split(s, "/")
	if len(parts) > 0 {
		name := parts[len(parts)-1]
		if len(name) > 50 {
			return "…" + name[len(name)-47:]
		}
		return name
	}
	return s
}
