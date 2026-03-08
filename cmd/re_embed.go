package cmd

import (
	"context"
	"fmt"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var reEmbedCmd = &cobra.Command{
	Use:   "re-embed",
	Short: "Re-embed a collection with a new model",
	Long: `Re-compute embeddings for all chunks in a collection using a new model.
Useful after switching embedding models to keep vector search up-to-date.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		colFlag, _ := cmd.Flags().GetString("collection")
		modelFlag, _ := cmd.Flags().GetString("model")
		dryRun, _ := cmd.Flags().GetBool("dry-run")

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.Migrate(); err != nil {
			return err
		}

		// Resolve target collections
		var collections []store.Collection
		if colFlag != "" {
			col, err := db.Collections().Get(colFlag)
			if err != nil {
				return fmt.Errorf("collection %q not found: %w", colFlag, err)
			}
			collections = []store.Collection{*col}
		} else {
			collections, err = db.Collections().List()
			if err != nil {
				return err
			}
		}

		if len(collections) == 0 {
			fmt.Println("No collections found.")
			return nil
		}

		// Build embedder
		modelName := modelFlag
		if modelName == "" {
			modelName = cfg.DefaultModel
		}

		embedder, err := embed.New(modelName, cfg)
		if err != nil {
			return fmt.Errorf("init embedder: %w", err)
		}
		fmt.Printf("🔧 Model    : %s (%s)\n", embedder.ModelName(), embedder.Provider())
		fmt.Printf("📐 Dim      : %d\n", embedder.Dim())

		if dryRun {
			for _, col := range collections {
				chunks, err := db.Chunks().GetByCollectionID(col.ID)
				if err != nil {
					return err
				}
				fmt.Printf("📁 [dry-run] %s → %d chunks would be re-embedded\n", col.Name, len(chunks))
			}
			return nil
		}

		ctx := context.Background()
		totalChunks := 0
		totalCols := 0

		for _, col := range collections {
			chunks, err := db.Chunks().GetByCollectionID(col.ID)
			if err != nil {
				fmt.Printf("⚠️  Skip %s: %v\n", col.Name, err)
				continue
			}
			if len(chunks) == 0 {
				fmt.Printf("📁 %s: no chunks, skipping\n", col.Name)
				continue
			}

			fmt.Printf("📁 %s: embedding %d chunks...\n", col.Name, len(chunks))

			batchSize := 32
			done := 0
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
					return fmt.Errorf("embed batch: %w", err)
				}

				for j, vec := range vectors {
					if err := db.Embeddings().Upsert(
						batch[j].ID,
						embedder.ModelName(),
						embedder.Provider(),
						vec,
					); err != nil {
						return fmt.Errorf("upsert embedding: %w", err)
					}
				}
				done += len(batch)
				fmt.Printf("   %d/%d\r", done, len(chunks))
			}
			fmt.Printf("   ✅ %d chunks done\n", len(chunks))
			totalChunks += len(chunks)
			totalCols++
		}

		fmt.Printf("\n✅ Re-embedded %d chunks across %d collection(s) with model [%s]\n",
			totalChunks, totalCols, embedder.ModelName())
		return nil
	},
}

func init() {
	reEmbedCmd.Flags().StringP("collection", "c", "", "Collection name or ID (omit for all)")
	reEmbedCmd.Flags().StringP("model", "m", "", "Embedding model (default: config default)")
	reEmbedCmd.Flags().Bool("dry-run", false, "Show what would be done without actually embedding")
	_ = reEmbedCmd.MarkFlagRequired("model")
}
