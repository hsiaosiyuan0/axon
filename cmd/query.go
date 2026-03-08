package cmd

import (
	"fmt"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var (
	queryCollection string
	queryLimit      int
	queryRerank     bool
	queryRerankMode string
)

var queryCmd = &cobra.Command{
	Use:   "query <text>",
	Short: "Search your knowledge base",
	Long: `Search the knowledge base using hybrid BM25 + vector retrieval.

Optionally enable two-stage reranking for higher precision:
  --rerank              fast token-overlap BM25 reranker (default mode)
  --rerank --rerank-mode llm   LLM-based cross-encoder reranking (requires AXON_LLM_API_KEY)

Examples:
  axon query "golang concurrency patterns"
  axon query "golang concurrency" --rerank
  axon query "golang concurrency" --rerank --rerank-mode llm
  axon query "channels" -c notes -n 10`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		q := strings.Join(args, " ")

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		searcher, err := hybrid.NewSearcher(cfg)
		if err != nil {
			return err
		}
		defer searcher.Close()

		// Pre-load collection name map for display
		colNames := map[string]string{}
		if db, dbErr := store.Open(cfg.DBPath); dbErr == nil {
			if cols, colErr := db.Collections().List(); colErr == nil {
				for _, c := range cols {
					colNames[c.ID] = c.Name
				}
			}
			db.Close()
		}

		opts := hybrid.SearchOptions{
			Query:      q,
			Collection: queryCollection,
			Limit:      queryLimit,
			Rerank:     queryRerank,
			RerankMode: queryRerankMode,
		}

		results, err := searcher.Search(cmd.Context(), opts)
		if err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		rerankLabel := ""
		if queryRerank {
			rerankLabel = fmt.Sprintf(" [reranked: %s]", queryRerankMode)
			if queryRerankMode == "" {
				rerankLabel = " [reranked: token-bm25]"
			}
		}
		fmt.Printf("🔍 Results for: %q%s\n\n", q, rerankLabel)
		for i, r := range results {
			// Source label: shorten long paths
			source := r.Source
			if len(source) > 60 {
				source = "…" + source[len(source)-57:]
			}
			fmt.Printf("[%d] %s\n", i+1, source)
			colDisplay := colNames[r.Collection]
			if colDisplay == "" {
				colDisplay = r.Collection
			}
			fmt.Printf("    Collection : %s  Score : %.4f\n", colDisplay, r.Score)
			// Wrap snippet at ~80 chars for readability
			snippet := strings.TrimSpace(r.Content)
			if len([]rune(snippet)) > 220 {
				snippet = string([]rune(snippet)[:220]) + "…"
			}
			// Indent each line of snippet
			lines := strings.Split(snippet, "\n")
			fmt.Printf("    ┄\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				fmt.Printf("    %s\n", line)
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	queryCmd.Flags().StringVarP(&queryCollection, "collection", "c", "", "Limit search to collection")
	queryCmd.Flags().IntVarP(&queryLimit, "limit", "n", 5, "Number of results")
	queryCmd.Flags().BoolVar(&queryRerank, "rerank", false, "Enable two-stage reranking for higher precision")
	queryCmd.Flags().StringVar(&queryRerankMode, "rerank-mode", "token", "Reranker mode: 'token' (fast) or 'llm' (slow, high quality)")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
