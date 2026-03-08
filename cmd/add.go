package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/spf13/cobra"
)

var addCollection string

var addCmd = &cobra.Command{
	Use:   "add <file|url>",
	Short: "Add a document to your knowledge base",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		origin := args[0]

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		// Resolve absolute path for local files
		if !isURL(origin) {
			abs, err := filepath.Abs(origin)
			if err != nil {
				return fmt.Errorf("resolve path: %w", err)
			}
			origin = abs
		}

		svc, err := ingest.NewService(cfg)
		if err != nil {
			return err
		}
		defer svc.Close()

		opts := ingest.AddOptions{
			Origin:     origin,
			Collection: addCollection,
		}

		result, err := svc.Add(cmd.Context(), opts)
		if err != nil {
			return err
		}

		fmt.Printf("✅ Added: %s\n", result.Title)
		fmt.Printf("   Collection : %s\n", result.Collection)
		fmt.Printf("   Chunks     : %d\n", result.ChunkCount)
		if result.RelationCount > 0 {
			fmt.Printf("   Relations  : %d\n", result.RelationCount)
		}
		if len(result.TopChunks) > 0 {
			fmt.Printf("   Preview    :\n")
			for i, c := range result.TopChunks {
				fmt.Printf("     [%d] %s\n", i+1, c)
			}
		}
		return nil
	},
}

func init() {
	addCmd.Flags().StringVarP(&addCollection, "collection", "c", "", "Target collection ID or name")
}

func isURL(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || s[:8] == "https://")
}
