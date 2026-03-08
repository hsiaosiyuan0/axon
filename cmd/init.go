package cmd

import (
	"fmt"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/modelreg"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize your Axon knowledge base",
	Long: `Initialize Axon and set up the local database.

The built-in embedding model (bge-small-zh-v1.5, ~24 MB) is embedded in the
binary and will be extracted automatically on first use — no downloads needed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer db.Close()

		if err := db.Migrate(); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}

		fmt.Printf("✅ Axon initialized at %s\n\n", cfg.DBPath)

		// Print built-in model info
		builtin := modelreg.BuiltInModel()
		if builtin != nil {
			fmt.Printf("📦 Built-in model: %s (~%d MB, embedded in binary)\n",
				builtin.Name, builtin.SizeMB)
			fmt.Printf("   %s\n\n", builtin.Description)
		}

		fmt.Println("Next steps:")
		fmt.Println("  axon collection new               — create a collection")
		fmt.Println("  axon add <file>                   — add a document")
		fmt.Println("  axon query \"your question\"        — search your knowledge base")
		fmt.Println("  axon model list                   — see all available models")
		fmt.Println("  axon model download bge-m3        — download a larger model")
		return nil
	},
}

func init() {
}
