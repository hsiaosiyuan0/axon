package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var collectionCmd = &cobra.Command{
	Use:   "collection",
	Short: "Manage collections",
}

var collectionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all collections",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		cols, err := db.Collections().List()
		if err != nil {
			return err
		}

		if len(cols) == 0 {
			fmt.Println("No collections yet. Run 'axon collection new' to create one.")
			return nil
		}

		fmt.Printf("%-20s %-10s %-20s %s\n", "NAME", "TYPE", "MODEL", "DESCRIPTION")
		fmt.Println("─────────────────────────────────────────────────────────────")
		for _, c := range cols {
			fmt.Printf("%-20s %-10s %-20s %s\n", c.Name, c.Type, c.ModelName, c.Description)
		}
		return nil
	},
}

var collectionNewCmd = &cobra.Command{
	Use:   "new",
	Short: "Create a new collection (interactive)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// TODO: interactive TUI prompt
		// For now, use flags
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		name, _ := cmd.Flags().GetString("name")
		colType, _ := cmd.Flags().GetString("type")
		desc, _ := cmd.Flags().GetString("description")
		model, _ := cmd.Flags().GetString("model")

		col, err := db.Collections().Create(store.CreateCollectionParams{
			Name:        name,
			Type:        colType,
			Description: desc,
			ModelName:   model,
		})
		if err != nil {
			return err
		}

		fmt.Printf("✅ Collection created: %s (id: %s)\n", col.Name, col.ID)
		return nil
	},
}

var collectionRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Delete a collection",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("⚠️  Delete collection %q and all its data? [y/N] ", args[0])
			reader := bufio.NewReader(os.Stdin)
			answer, _ := reader.ReadString('\n')
			answer = strings.TrimSpace(strings.ToLower(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}

		if err := db.Collections().Delete(args[0]); err != nil {
			return err
		}
		fmt.Printf("🗑️  Collection %s deleted.\n", args[0])
		return nil
	},
}

func init() {
	collectionNewCmd.Flags().String("name", "", "Collection name (required)")
	collectionNewCmd.Flags().String("type", "custom", "Collection type: diary|work|code|notes|custom")
	collectionNewCmd.Flags().String("description", "", "Description of this collection")
	collectionNewCmd.Flags().String("model", "", "Embedding model name (default: config default)")
	_ = collectionNewCmd.MarkFlagRequired("name")

	collectionRmCmd.Flags().Bool("force", false, "Skip confirmation prompt")

	collectionCmd.AddCommand(collectionListCmd)
	collectionCmd.AddCommand(collectionNewCmd)
	collectionCmd.AddCommand(collectionRmCmd)
}
