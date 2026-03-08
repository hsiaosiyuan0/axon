package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/ingest"
	"github.com/hsiaosiyuan0/axon/internal/plugin"
	"github.com/spf13/cobra"
)

var (
	importCollection string
	importDryRun     bool
	importGlob       string
	importSkipEmpty  bool
	importNotion     bool
)

var importCmd = &cobra.Command{
	Use:   "import <path>",
	Short: "Import a local directory, Obsidian vault, or Notion export into the knowledge base",
	Long: `Recursively import all Markdown (and text) files from a directory.

Supports Obsidian vaults — [[wiki links]] are parsed into relations automatically.
Supports Notion HTML and Markdown exports — auto-detected or use --notion flag.

Examples:
  axon import ~/notes                          # import entire notes folder
  axon import ~/obsidian/vault -c obsidian     # import into named collection
  axon import ~/notion-export --notion         # import Notion export explicitly
  axon import ~/notes --glob "*.md"            # only markdown files
  axon import ~/notes --dry-run               # preview without ingesting`,
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
			return fmt.Errorf("path not found: %w", err)
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

		// Auto-detect Notion export
		isNotion := importNotion
		if !isNotion && info.IsDir() {
			isNotion = plugin.IsNotionExport(root)
			if isNotion {
				fmt.Printf("🔖 Detected Notion export format\n")
			}
		}

		// Collect files
		pattern := importGlob
		if pattern == "" {
			if isNotion {
				// Also pick up HTML files for Notion exports
				pattern = "*.{md,markdown,txt,text,html,htm}"
			} else {
				pattern = "*.{md,markdown,txt,text}"
			}
		}

		var files []string
		if info.IsDir() {
			files, err = walkDir(root, pattern)
			if err != nil {
				return fmt.Errorf("walk directory: %w", err)
			}
		} else {
			files = []string{root}
		}

		if len(files) == 0 {
			fmt.Printf("📂 No matching files found in %s\n", root)
			return nil
		}

		fmt.Printf("📂 Found %d file(s) to import from %s\n", len(files), root)

		if importDryRun {
			for _, f := range files {
				rel, _ := filepath.Rel(root, f)
				fmt.Printf("  [dry-run] %s\n", rel)
			}
			fmt.Printf("\n🔍 Would import %d file(s)\n", len(files))
			return nil
		}

		// Import each file
		var (
			success int
			skipped int
			failed  int
		)

		for i, f := range files {
			rel, _ := filepath.Rel(root, f)

			// Skip empty files if flag set
			if importSkipEmpty {
				fi, _ := os.Stat(f)
				if fi != nil && fi.Size() < 10 {
					fmt.Printf("  [%d/%d] ⏭  %s (empty)\n", i+1, len(files), rel)
					skipped++
					continue
				}
			}

			fmt.Printf("  [%d/%d] 📄 %s\n", i+1, len(files), rel)

			lowerF := strings.ToLower(f)
			isHTML := strings.HasSuffix(lowerF, ".html") || strings.HasSuffix(lowerF, ".htm")
			isMD := strings.HasSuffix(lowerF, ".md") || strings.HasSuffix(lowerF, ".markdown")

			var result *ingest.AddResult
			var ingestErr error

			switch {
			case isNotion && isHTML:
				// Parse Notion HTML export
				notionData, pErr := plugin.ParseNotionHTML(f)
				if pErr != nil {
					fmt.Printf("        ⏭  %v\n", pErr)
					skipped++
					continue
				}
				result, ingestErr = svc.AddWithData(context.Background(), ingest.AddOptions{
					Origin:     f,
					Collection: importCollection,
				}, notionData)

			case isNotion && isMD:
				// Parse Notion Markdown export (clean title + frontmatter)
				notionData, pErr := plugin.ParseNotionMarkdown(f)
				if pErr == nil {
					result, ingestErr = svc.AddWithData(context.Background(), ingest.AddOptions{
						Origin:     f,
						Collection: importCollection,
					}, notionData)
				} else {
					// Fall back to standard file ingestion
					result, ingestErr = svc.Add(context.Background(), ingest.AddOptions{
						Origin:     f,
						Collection: importCollection,
					})
				}

			default:
				result, ingestErr = svc.Add(context.Background(), ingest.AddOptions{
					Origin:     f,
					Collection: importCollection,
				})
			}

			if ingestErr != nil {
				fmt.Printf("        ❌ %v\n", ingestErr)
				failed++
				continue
			}

			if result.ChunkCount == 0 {
				fmt.Printf("        ⏭  no content (skipped)\n")
				skipped++
				continue
			}

			fmt.Printf("        ✅ %s → %s (%d chunks, %d relations)\n",
				result.Title, result.Collection, result.ChunkCount, result.RelationCount)
			success++
		}

		fmt.Printf("\n📊 Import complete: %d imported, %d skipped, %d failed (total %d)\n",
			success, skipped, failed, len(files))
		return nil
	},
}

func init() {
	importCmd.Flags().StringVarP(&importCollection, "collection", "c", "", "Target collection name or ID")
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "Preview files without importing")
	importCmd.Flags().StringVar(&importGlob, "glob", "", "File glob pattern (default: *.md,*.txt)")
	importCmd.Flags().BoolVar(&importSkipEmpty, "skip-empty", true, "Skip files with less than 10 bytes")
	importCmd.Flags().BoolVar(&importNotion, "notion", false, "Parse as Notion export (auto-detected if not specified)")
}

// walkDir recursively walks a directory and returns matching files.
// pattern supports comma-separated globs like "*.md,*.txt"
func walkDir(root, pattern string) ([]string, error) {
	patterns := strings.Split(pattern, ",")
	for i, p := range patterns {
		patterns[i] = strings.TrimSpace(p)
	}
	// Flatten brace expansion: "*.{md,txt}" → ["*.md","*.txt"]
	var expanded []string
	for _, p := range patterns {
		expanded = append(expanded, expandGlob(p)...)
	}

	var files []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable
		}
		if info.IsDir() {
			// Skip hidden dirs (e.g., .obsidian, .git)
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		name := strings.ToLower(info.Name())
		for _, p := range expanded {
			matched, _ := filepath.Match(p, name)
			if matched {
				files = append(files, path)
				break
			}
		}
		return nil
	})
	return files, err
}

// expandGlob handles simple brace expansion: "*.{md,txt}" → ["*.md","*.txt"]
func expandGlob(pattern string) []string {
	start := strings.Index(pattern, "{")
	end := strings.Index(pattern, "}")
	if start < 0 || end < 0 || end < start {
		return []string{pattern}
	}
	prefix := pattern[:start]
	suffix := pattern[end+1:]
	parts := strings.Split(pattern[start+1:end], ",")
	var result []string
	for _, p := range parts {
		result = append(result, prefix+strings.TrimSpace(p)+suffix)
	}
	return result
}
