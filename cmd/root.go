package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// globalDB overrides the default DB path when --db flag is set.
var globalDB string

var rootCmd = &cobra.Command{
	Use:     "axon",
	Short:   "Axon — your personal knowledge base and memory engine",
	Version: Version,
	Long: `
 █████╗ ██╗  ██╗ ██████╗ ███╗   ██╗
██╔══██╗╚██╗██╔╝██╔═══██╗████╗  ██║
███████║ ╚███╔╝ ██║   ██║██╔██╗ ██║
██╔══██║ ██╔██╗ ██║   ██║██║╚██╗██║
██║  ██║██╔╝ ██╗╚██████╔╝██║ ╚████║
╚═╝  ╚═╝╚═╝  ╚═╝ ╚═════╝ ╚═╝  ╚═══╝

Your personal knowledge base and memory engine.
Local-first, single-binary, relationship-aware.

Use --db to switch between multiple knowledge bases:
  axon --db ~/work.db query "project plan"
  axon --db ~/research.db add paper.pdf
`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Global --db flag available to all subcommands
	rootCmd.PersistentFlags().StringVar(&globalDB, "db", "", "Path to knowledge base DB file (overrides AXON_DB env)")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(queryCmd)
	rootCmd.AddCommand(collectionCmd)
	rootCmd.AddCommand(modelCmd)
	rootCmd.AddCommand(reEmbedCmd)
	rootCmd.AddCommand(relateCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(mcpCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(vaultCmd)
	rootCmd.AddCommand(dedupeCmd)
	rootCmd.AddCommand(upgradeCmd)
	rootCmd.AddCommand(chatCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(pluginCmd)
	rootCmd.AddCommand(configCmd)
}
