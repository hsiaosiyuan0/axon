package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/obsidian"
	"github.com/spf13/cobra"
)

var (
	vaultCollection string
	vaultDryRun     bool
	vaultVerbose    bool
)

var vaultCmd = &cobra.Command{
	Use:   "vault <path>",
	Short: "Import an Obsidian vault into Axon",
	Long: `Recursively import all Markdown files from an Obsidian vault directory.

Wikilinks ([[Note Name]]) are parsed and stored as relations. If the linked
note has already been imported, the relation is resolved immediately. If not,
it is stored as a pending relation and resolved when the linked note is later
imported.

Example:
  axon vault ~/Documents/MyVault
  axon vault ~/Documents/MyVault -c obsidian
  axon vault ~/Documents/MyVault --dry-run`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		root := args[0]

		// Expand ~
		if strings.HasPrefix(root, "~/") {
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, root[2:])
		}

		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("vault path not found: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%q is not a directory", root)
		}

		// Scan vault first to show summary
		fmt.Printf("🔍 Scanning vault: %s\n", root)
		vault, err := obsidian.ScanVault(root)
		if err != nil {
			return fmt.Errorf("scan vault: %w", err)
		}

		fmt.Printf("📔 Found %d notes\n", len(vault.Notes))

		// Count total wikilinks
		totalLinks := 0
		for _, note := range vault.Notes {
			totalLinks += len(note.Links)
		}
		fmt.Printf("🔗 Total wikilinks: %d\n", totalLinks)

		if vaultDryRun {
			fmt.Println("\n[dry-run] Files that would be imported:")
			for _, note := range vault.Notes {
				rel, _ := filepath.Rel(root, note.Path)
				linkCount := len(note.Links)
				fmt.Printf("  📄 %s (%d links)\n", rel, linkCount)
			}
			return nil
		}

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		svc, err := ingest.NewService(cfg)
		if err != nil {
			return err
		}
		defer svc.Close()

		col := vaultCollection
		if col == "" {
			// Default: use vault directory name as collection
			col = filepath.Base(root)
		}

		fmt.Printf("📁 Target collection: %s\n\n", col)

		success, skipped, failed := 0, 0, 0
		for _, note := range vault.Notes {
			rel, _ := filepath.Rel(root, note.Path)
			result, err := svc.Add(cmd.Context(), ingest.AddOptions{
				Origin:     note.Path,
				Collection: col,
			})
			if err != nil {
				fmt.Printf("  ❌ %s: %v\n", rel, err)
				failed++
				continue
			}
			if result.ChunkCount == 0 {
				skipped++
				if vaultVerbose {
					fmt.Printf("  ⏭  %s (no changes)\n", rel)
				}
				continue
			}
			success++
			if vaultVerbose {
				fmt.Printf("  ✅ %s → %d chunks, %d links\n", rel, result.ChunkCount, result.RelationCount)
			} else {
				fmt.Printf("  ✅ %s\n", rel)
			}
		}

		fmt.Printf("\n📊 Results: %d imported, %d skipped, %d failed\n", success, skipped, failed)
		fmt.Printf("   Collection: %s\n", col)
		fmt.Printf("   Vault: %s\n", root)
		return nil
	},
}

func init() {
	vaultCmd.Flags().StringVarP(&vaultCollection, "collection", "c", "", "Target collection name (default: vault directory name)")
	vaultCmd.Flags().BoolVar(&vaultDryRun, "dry-run", false, "Show what would be imported without actually importing")
	vaultCmd.Flags().BoolVarP(&vaultVerbose, "verbose", "v", false, "Show detailed output for each file")
}
