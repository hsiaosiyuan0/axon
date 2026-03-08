package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/anki"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var (
	exportCollection string
	exportFormat     string
	exportOutput     string
	exportFull       bool
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export knowledge base to Markdown, JSON, or Anki",
	Long: `Export your knowledge base to portable formats.

Formats:
  markdown  One .md file per source, organized in collection folders
  json      Single JSON file with all sources, chunks, and relations
  jsonl     JSON Lines (one source per line) for streaming pipelines
  anki      Anki flashcard deck (.apkg) — one card per chunk

Examples:
  axon export                          # Markdown to ./axon-export/
  axon export -f json -o kb.json       # JSON bundle
  axon export -f jsonl -o kb.jsonl     # JSONL stream
  axon export -f anki -o axon.apkg     # Anki flashcard deck
  axon export -c notes -o notes/       # Only "notes" collection`,
	RunE: runExport,
}

func init() {
	exportCmd.Flags().StringVarP(&exportCollection, "collection", "c", "", "Limit to a collection")
	exportCmd.Flags().StringVarP(&exportFormat, "format", "f", "markdown", "Output format: markdown, json, jsonl")
	exportCmd.Flags().StringVarP(&exportOutput, "output", "o", "", "Output path (default: ./axon-export/ or ./axon-export.json)")
	exportCmd.Flags().BoolVar(&exportFull, "full", false, "Include raw content and all chunks (default: summary only)")
}

// ── JSON export types ─────────────────────────────────────────────────────────

type exportSource struct {
	ID         string         `json:"id"`
	Collection string         `json:"collection"`
	SourceType string         `json:"source_type"`
	Origin     string         `json:"origin"`
	Title      string         `json:"title"`
	PlainText  string         `json:"plain_text,omitempty"`
	Lang       string         `json:"lang,omitempty"`
	Meta       map[string]any `json:"meta,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	Chunks     []exportChunk  `json:"chunks,omitempty"`
}

type exportChunk struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Section  string `json:"section,omitempty"`
	Content  string `json:"content"`
}

type exportRelation struct {
	FromID  string  `json:"from_id"`
	ToID    string  `json:"to_id"`
	RelType string  `json:"rel_type"`
	Weight  float64 `json:"weight"`
	By      string  `json:"established_by,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

type exportBundle struct {
	ExportedAt  time.Time         `json:"exported_at"`
	Collections []store.Collection `json:"collections"`
	Sources     []exportSource    `json:"sources"`
	Relations   []exportRelation  `json:"relations,omitempty"`
}

// ── Main runner ───────────────────────────────────────────────────────────────

func runExport(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(globalDB)
	if err != nil {
		return err
	}

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	// Collect collections
	var cols []store.Collection
	if exportCollection != "" {
		col, err := db.Collections().Get(exportCollection)
		if err != nil {
			return fmt.Errorf("collection %q not found", exportCollection)
		}
		cols = []store.Collection{*col}
	} else {
		cols, err = db.Collections().List()
		if err != nil {
			return err
		}
	}

	// Collect all sources grouped by collection
	var allSources []exportSource
	for _, col := range cols {
		sources, err := db.Sources().ListByCollection(col.ID)
		if err != nil {
			return err
		}
		for _, src := range sources {
			es := exportSource{
				ID:         src.ID,
				Collection: col.Name,
				SourceType: src.SourceType,
				Origin:     src.Origin,
				Title:      src.Title,
				Lang:       src.Lang,
				Meta:       src.Meta,
				CreatedAt:  src.CreatedAt,
			}
			if exportFull {
				es.PlainText = src.PlainText
				chunks, _ := db.Chunks().GetBySourceID(src.ID)
				for _, c := range chunks {
					es.Chunks = append(es.Chunks, exportChunk{
						ID:       c.ID,
						Position: c.Position,
						Section:  c.Section,
						Content:  c.Content,
					})
				}
			}
			allSources = append(allSources, es)
		}
	}

	// Collect relations
	var allRelations []exportRelation
	rels, _ := db.Relations().ListAll()
	for _, r := range rels {
		allRelations = append(allRelations, exportRelation{
			FromID:   r.FromID,
			ToID:     r.ToID,
			RelType:  r.RelType,
			Weight:   r.Weight,
			By:       r.EstablishedBy,
			Evidence: r.Evidence,
		})
	}

	switch strings.ToLower(exportFormat) {
	case "markdown", "md":
		return exportMarkdown(cols, allSources, db)
	case "json":
		return exportJSON(cols, allSources, allRelations)
	case "jsonl":
		return exportJSONL(allSources)
	case "anki", "apkg":
		return exportAnki(cols, allSources, db)
	default:
		return fmt.Errorf("unknown format %q (use: markdown, json, jsonl, anki)", exportFormat)
	}
}

// ── Markdown export ───────────────────────────────────────────────────────────

func exportMarkdown(cols []store.Collection, sources []exportSource, db *store.DB) error {
	outDir := exportOutput
	if outDir == "" {
		outDir = "axon-export"
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Group by collection
	byColl := map[string][]exportSource{}
	for _, s := range sources {
		byColl[s.Collection] = append(byColl[s.Collection], s)
	}

	totalFiles := 0
	for collName, srcs := range byColl {
		collDir := filepath.Join(outDir, sanitizeFilename(collName))
		if err := os.MkdirAll(collDir, 0o755); err != nil {
			return err
		}

		for _, src := range srcs {
			filename := sanitizeFilename(src.Title)
			if filename == "" {
				filename = src.ID[:8]
			}
			filename += ".md"
			path := filepath.Join(collDir, filename)

			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("# %s\n\n", src.Title))
			sb.WriteString(fmt.Sprintf("- **Origin**: %s\n", src.Origin))
			sb.WriteString(fmt.Sprintf("- **Collection**: %s\n", src.Collection))
			sb.WriteString(fmt.Sprintf("- **Source Type**: %s\n", src.SourceType))
			sb.WriteString(fmt.Sprintf("- **Added**: %s\n", src.CreatedAt.Format("2006-01-02")))
			if src.Lang != "" {
				sb.WriteString(fmt.Sprintf("- **Language**: %s\n", src.Lang))
			}
			sb.WriteString("\n---\n\n")

			// Full text or chunks
			if exportFull && len(src.Chunks) > 0 {
				for _, c := range src.Chunks {
					if c.Section != "" {
						sb.WriteString(fmt.Sprintf("## %s\n\n", c.Section))
					}
					sb.WriteString(c.Content)
					sb.WriteString("\n\n")
				}
			} else {
				// Fetch plain text for summary
				fullSrc, err := db.Sources().GetByID(src.ID)
				if err == nil && fullSrc.PlainText != "" {
					plain := fullSrc.PlainText
					if !exportFull && len(plain) > 2000 {
						plain = plain[:2000] + "\n\n*(truncated — use --full for complete text)*"
					}
					sb.WriteString(plain)
					sb.WriteString("\n")
				}
			}

			if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
				return err
			}
			totalFiles++
		}
	}

	// Write index
	indexPath := filepath.Join(outDir, "INDEX.md")
	var idx strings.Builder
	idx.WriteString(fmt.Sprintf("# Axon Export Index\n\nExported: %s\n\n",
		time.Now().Format("2006-01-02 15:04:05")))
	for _, col := range cols {
		idx.WriteString(fmt.Sprintf("## %s\n\n", col.Name))
		if col.Description != "" {
			idx.WriteString(fmt.Sprintf("> %s\n\n", col.Description))
		}
		for _, src := range byColl[col.Name] {
			fn := sanitizeFilename(src.Title)
			if fn == "" {
				fn = src.ID[:8]
			}
			idx.WriteString(fmt.Sprintf("- [%s](%s/%s.md)\n",
				src.Title, sanitizeFilename(col.Name), fn))
		}
		idx.WriteString("\n")
	}
	_ = os.WriteFile(indexPath, []byte(idx.String()), 0o644)

	fmt.Printf("✅ Exported %d sources to %s/\n", totalFiles, outDir)
	fmt.Printf("   📄 Index: %s\n", indexPath)
	return nil
}

// ── JSON export ───────────────────────────────────────────────────────────────

func exportJSON(cols []store.Collection, sources []exportSource, relations []exportRelation) error {
	outPath := exportOutput
	if outPath == "" {
		outPath = "axon-export.json"
	}

	bundle := exportBundle{
		ExportedAt:  time.Now(),
		Collections: cols,
		Sources:     sources,
		Relations:   relations,
	}

	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return err
	}

	fmt.Printf("✅ Exported %d sources, %d relations → %s (%.1f KB)\n",
		len(sources), len(relations), outPath, float64(len(data))/1024)
	return nil
}

// ── JSONL export ──────────────────────────────────────────────────────────────

func exportJSONL(sources []exportSource) error {
	outPath := exportOutput
	if outPath == "" {
		outPath = "axon-export.jsonl"
	}

	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, src := range sources {
		if err := enc.Encode(src); err != nil {
			return err
		}
	}

	fmt.Printf("✅ Exported %d sources → %s\n", len(sources), outPath)
	return nil
}

// ── Anki export ───────────────────────────────────────────────────────────────

func exportAnki(cols []store.Collection, sources []exportSource, db *store.DB) error {
	outPath := exportOutput
	if outPath == "" {
		outPath = "axon-export.apkg"
	}

	// Build collection name map: id → name
	collMap := map[string]string{}
	for _, c := range cols {
		collMap[c.ID] = c.Name
	}

	var cards []anki.Card
	totalChunks := 0

	for _, src := range sources {
		collName := src.Collection
		if collName == "" {
			collName = "axon"
		}

		chunks, err := db.Chunks().GetBySourceID(src.ID)
		if err != nil {
			continue
		}

		for _, c := range chunks {
			if strings.TrimSpace(c.Content) == "" {
				continue
			}
			card := anki.ChunkToCard(c.Section, c.Content, src.Title, collName)
			cards = append(cards, card)
			totalChunks++
		}
	}

	if len(cards) == 0 {
		fmt.Println("⚠️  No chunks found to export as Anki cards.")
		return nil
	}

	if err := anki.ExportAPKG(cards, outPath); err != nil {
		return fmt.Errorf("write apkg: %w", err)
	}

	stat, _ := os.Stat(outPath)
	size := int64(0)
	if stat != nil {
		size = stat.Size()
	}

	fmt.Printf("✅ Exported %d flashcards (%d sources) → %s (%.1f KB)\n",
		totalChunks, len(sources), outPath, float64(size)/1024)
	fmt.Println()
	fmt.Println("   📱 To import in Anki:")
	fmt.Println("      1. Open Anki desktop")
	fmt.Printf("      2. File → Import → select %s\n", outPath)
	fmt.Println("      3. Cards will appear in deck \"Axon\"")
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func sanitizeFilename(name string) string {
	r := strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-",
		"?", "", "\"", "", "<", "", ">", "", "|", "-",
		" ", "_",
	)
	result := r.Replace(name)
	if len(result) > 80 {
		result = result[:80]
	}
	return result
}
