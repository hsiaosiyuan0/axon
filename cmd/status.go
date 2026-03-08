package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show knowledge base statistics and health",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return fmt.Errorf("open db: %w", err)
		}
		defer db.Close()

		type colStat struct {
			name    string
			sources int
			chunks  int
			embeds  int
		}

		cols, err := db.Collections().List()
		if err != nil {
			return err
		}

		var totalSources, totalChunks, totalEmbeds, totalRelations int
		var colStats []colStat

		for _, col := range cols {
			sources, _ := db.Sources().ListByCollection(col.ID)
			nSrc := len(sources)
			totalSources += nSrc

			var nChunk, nEmbed int
			for _, src := range sources {
				chunks, _ := db.Chunks().GetBySourceID(src.ID)
				nChunk += len(chunks)
				for _, c := range chunks {
					if e, _ := db.Embeddings().GetByChunkID(c.ID); e != nil {
						nEmbed++
					}
				}
			}
			totalChunks += nChunk
			totalEmbeds += nEmbed

			colStats = append(colStats, colStat{
				name:    col.Name,
				sources: nSrc,
				chunks:  nChunk,
				embeds:  nEmbed,
			})
		}

		// Total relations
		totalRelations, _ = db.Relations().Count()

		// Embed coverage
		coverage := 0.0
		if totalChunks > 0 {
			coverage = float64(totalEmbeds) / float64(totalChunks) * 100
		}

		// DB file size
		dbSizeStr := "?"
		if fi, err := os.Stat(cfg.DBPath); err == nil {
			sz := fi.Size()
			switch {
			case sz < 1024:
				dbSizeStr = fmt.Sprintf("%d B", sz)
			case sz < 1024*1024:
				dbSizeStr = fmt.Sprintf("%.1f KB", float64(sz)/1024)
			default:
				dbSizeStr = fmt.Sprintf("%.1f MB", float64(sz)/1024/1024)
			}
		}

		// ── Output ──────────────────────────────────────────────────────
		fmt.Println()
		fmt.Println("╔══════════════════════════════════════════════╗")
		fmt.Println("║        Axon Knowledge Base  Status           ║")
		fmt.Println("╠══════════════════════════════════════════════╣")
		fmt.Printf("║  DB       : %-33s║\n", truncate(cfg.DBPath, 33))
		fmt.Printf("║  Size     : %-33s║\n", dbSizeStr)
		fmt.Printf("║  Time     : %-33s║\n", time.Now().Format("2006-01-02 15:04:05"))
		fmt.Println("╠══════════════════════════════════════════════╣")
		fmt.Printf("║  Collections : %-5d                          ║\n", len(cols))
		fmt.Printf("║  Sources     : %-5d                          ║\n", totalSources)
		fmt.Printf("║  Chunks      : %-5d                          ║\n", totalChunks)
		fmt.Printf("║  Embeddings  : %-5d  (%.0f%% coverage)         ║\n", totalEmbeds, coverage)
		fmt.Printf("║  Relations   : %-5d                          ║\n", totalRelations)
		fmt.Println("╠══════════════════════════════════════════════╣")
		fmt.Println("║  Collections:                                ║")
		for _, cs := range colStats {
			cov := 0.0
			if cs.chunks > 0 {
				cov = float64(cs.embeds) / float64(cs.chunks) * 100
			}
			line := fmt.Sprintf("  %-12s  %3d src  %4d chunks  %3.0f%%",
				truncate(cs.name, 12), cs.sources, cs.chunks, cov)
			fmt.Printf("║  %-44s║\n", line)
		}
		if len(colStats) == 0 {
			fmt.Println("║    (no collections yet)                      ║")
		}
		fmt.Println("╠══════════════════════════════════════════════╣")
		fmt.Println("║  Embedding Backend:                          ║")

		// Determine embedding backend display
		provider := cfg.EmbedProvider
		if provider == "" {
			// Infer from model name
			switch {
			case strings.HasPrefix(cfg.DefaultModel, "api:"):
				provider = "api"
			case cfg.DefaultModel == "purego" || cfg.DefaultModel == "purego:tfidf-512":
				provider = "purego"
			default:
				provider = "onnx"
			}
		}
		switch provider {
		case "api":
			apiModel := cfg.EmbedAPIModel
			if apiModel == "" {
				apiModel = "text-embedding-3-small"
			}
			apiKey := cfg.EmbedAPIKey
			if apiKey == "" {
				apiKey = cfg.LLMAPIKey
			}
			keyStatus := "❌ not set"
			if apiKey != "" {
				keyStatus = "✅ set"
			}
			fmt.Printf("║  %-44s║\n", fmt.Sprintf("  Provider : API (OpenAI-compatible)"))
			fmt.Printf("║  %-44s║\n", fmt.Sprintf("  Model    : %s", truncate(apiModel, 35)))
			fmt.Printf("║  %-44s║\n", fmt.Sprintf("  Endpoint : %s", truncate(cfg.EmbedAPIEndpoint, 35)))
			fmt.Printf("║  %-44s║\n", fmt.Sprintf("  API Key  : %s", keyStatus))
		case "purego":
			fmt.Printf("║  %-44s║\n", "  Provider : PureGo TF-IDF (zero deps)")
			fmt.Printf("║  %-44s║\n", "  Model    : purego:tfidf-512")
			fmt.Printf("║  %-44s║\n", "  Quality  : ⚠️  low (BM25 only recommended)")
		default:
			modelDisplay := cfg.DefaultModel
			if modelDisplay == "" {
				modelDisplay = "bge-small-zh-v1.5 (default)"
			}
			fmt.Printf("║  %-44s║\n", "  Provider : Local ONNX (offline)")
			fmt.Printf("║  %-44s║\n", fmt.Sprintf("  Model    : %s", truncate(modelDisplay, 35)))
		}
		fmt.Println("╠══════════════════════════════════════════════╣")
		fmt.Println("║  Health:                                     ║")
		if totalSources == 0 {
			fmt.Println("║  ⚠️  No sources — run: axon add <file/url>   ║")
		} else {
			fmt.Println("║  ✅ Sources indexed                          ║")
		}
		if totalChunks > 0 && coverage < 50 {
			fmt.Println("║  ⚠️  Low embed coverage — axon re-embed -m X ║")
		} else if totalChunks > 0 {
			fmt.Println("║  ✅ Embedding coverage good                  ║")
		}
		if cfg.LLMAPIKey == "" {
			fmt.Println("║  ℹ️  No LLM key — axon config set llm.key    ║")
		} else {
			fmt.Println("║  ✅ LLM API key configured                   ║")
		}
		fmt.Println("╚══════════════════════════════════════════════╝")
		fmt.Println()
		return nil
	},
}
