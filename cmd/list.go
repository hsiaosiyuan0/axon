package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var (
	listCollection string
	listVerbose    bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all sources in your knowledge base",
	Long: `List all sources that have been added to the knowledge base.

Shows title, collection, origin (file/URL), and chunk count for each source.

Examples:
  axon list
  axon list -c notes
  axon list -v`,
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

		// Load sources
		var sources []store.Source
		if listCollection != "" {
			// Resolve collection name to ID
			col, err := db.Collections().Get(listCollection)
			if err != nil {
				return fmt.Errorf("collection %q not found", listCollection)
			}
			sources, err = db.Sources().ListByCollection(col.ID)
			if err != nil {
				return err
			}
		} else {
			sources, err = db.Sources().List()
			if err != nil {
				return err
			}
		}

		if len(sources) == 0 {
			if listCollection != "" {
				fmt.Printf("📭 No sources in collection %q.\n", listCollection)
			} else {
				fmt.Println("📭 No sources found. Use `axon add <file|url>` to get started.")
			}
			return nil
		}

		// Load chunk counts in one query
		chunkCounts, err := db.Chunks().CountBySource()
		if err != nil {
			// Non-fatal: just show zeros
			chunkCounts = map[string]int{}
		}

		// Load collection names
		collections, err := db.Collections().List()
		if err != nil {
			collections = nil
		}
		colNames := make(map[string]string)
		for _, c := range collections {
			colNames[c.ID] = c.Name
		}

		// Header
		total := len(sources)
		if listCollection != "" {
			fmt.Printf("📚 %d source(s) in collection %q\n\n", total, listCollection)
		} else {
			fmt.Printf("📚 %d source(s) in knowledge base\n\n", total)
		}

		// Print sources
		for i, s := range sources {
			title := s.Title
			if title == "" {
				title = filepath.Base(s.Origin)
			}
			colName := colNames[s.Collection]
			if colName == "" {
				colName = s.Collection
			}
			chunks := chunkCounts[s.ID]

			fmt.Printf("[%d] %s\n", i+1, title)
			fmt.Printf("    Collection : %s\n", colName)
			fmt.Printf("    Chunks     : %d\n", chunks)
			fmt.Printf("    Origin     : %s\n", shortenOrigin(s.Origin))
			if listVerbose {
				fmt.Printf("    ID         : %s\n", s.ID)
				fmt.Printf("    Type       : %s\n", s.SourceType)
				fmt.Printf("    Lang       : %s\n", s.Lang)
				fmt.Printf("    Added      : %s\n", formatTime(s.CreatedAt))
			}
			if i < total-1 {
				fmt.Println()
			}
		}

		// Summary footer
		fmt.Printf("\n── Total: %d source(s) ──\n", total)
		return nil
	},
}

func init() {
	listCmd.Flags().StringVarP(&listCollection, "collection", "c", "", "Filter by collection name or ID")
	listCmd.Flags().BoolVarP(&listVerbose, "verbose", "v", false, "Show additional details (ID, type, lang, date)")
}

// shortenOrigin trims long paths for display.
func shortenOrigin(origin string) string {
	const maxLen = 70
	if len(origin) <= maxLen {
		return origin
	}
	// For URLs: keep scheme + host + "…" + end
	if strings.HasPrefix(origin, "http://") || strings.HasPrefix(origin, "https://") {
		return origin[:40] + "…" + origin[len(origin)-20:]
	}
	// For file paths: keep the last N segments
	return "…" + origin[len(origin)-maxLen+1:]
}

// formatTime formats a time value for human reading.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Format("2006-01-02 15:04")
}
