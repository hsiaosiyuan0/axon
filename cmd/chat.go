package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/chat"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/hybrid"
	"github.com/spf13/cobra"
)

var (
	chatCollection string
	chatLimit      int
	chatNoStream   bool
	chatMaxTurns   int
	chatSystem     string
	chatNoContext  bool
	chatOneShot    string
)

var chatCmd = &cobra.Command{
	Use:   "chat",
	Short: "RAG-powered conversational QA over your knowledge base",
	Long: `Start an interactive chat session backed by your knowledge base.

Each message is automatically enriched with the most relevant chunks (RAG),
then sent to the configured LLM for a grounded, cited answer.

Requires AXON_LLM_API_KEY to be set.

Examples:
  axon chat
  axon chat -c research
  axon chat --one-shot "Summarize what I know about Go channels"
  axon chat --no-context   # pure LLM, no retrieval`,
	RunE: runChat,
}

func init() {
	chatCmd.Flags().StringVarP(&chatCollection, "collection", "c", "", "Limit retrieval to this collection")
	chatCmd.Flags().IntVarP(&chatLimit, "context-docs", "n", 4, "Number of chunks to retrieve per turn")
	chatCmd.Flags().BoolVar(&chatNoStream, "no-stream", false, "Disable streaming (print full response at once)")
	chatCmd.Flags().IntVar(&chatMaxTurns, "max-turns", 20, "Max conversation turns to keep in context (0 = unlimited)")
	chatCmd.Flags().StringVar(&chatSystem, "system", "", "Extra system prompt text to inject")
	chatCmd.Flags().BoolVar(&chatNoContext, "no-context", false, "Skip retrieval; pure LLM chat mode")
	chatCmd.Flags().StringVar(&chatOneShot, "one-shot", "", "Ask a single question and exit (non-interactive)")
}

func runChat(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(globalDB)
	if err != nil {
		return err
	}

	if cfg.LLMAPIKey == "" {
		return fmt.Errorf("AXON_LLM_API_KEY is not set — required for chat mode")
	}

	llm := chat.NewClient(cfg.LLMEndpoint, cfg.LLMAPIKey, cfg.LLMModel)
	session := chat.NewSession()

	var searcher *hybrid.Searcher
	if !chatNoContext {
		searcher, err = hybrid.NewSearcher(cfg)
		if err != nil {
			return fmt.Errorf("opening knowledge base: %w", err)
		}
		defer searcher.Close()
	}

	// Banner
	if chatOneShot == "" {
		printChatBanner(cfg)
	}

	// One-shot mode
	if chatOneShot != "" {
		return chatTurn(cmd.Context(), llm, session, searcher, chatOneShot, cfg)
	}

	// Interactive loop
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("\n")

	for {
		fmt.Print("\033[1;36m you \033[0m▶ ")
		line, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println()
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" || line == "exit" || line == "quit" {
			fmt.Println("\n👋 Goodbye!")
			break
		}
		if line == "/clear" {
			session = chat.NewSession()
			fmt.Println("🔄 Session cleared.")
			continue
		}
		if line == "/help" {
			printChatHelp()
			continue
		}

		fmt.Println()
		if err := chatTurn(cmd.Context(), llm, session, searcher, line, cfg); err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  %v\n\n", err)
		}
		fmt.Println()

		// Trim history to max turns
		if chatMaxTurns > 0 {
			session.Truncate(chatMaxTurns)
		}
	}
	return nil
}

func chatTurn(
	ctx context.Context,
	llm *chat.Client,
	session *chat.Session,
	searcher *hybrid.Searcher,
	query string,
	cfg *config.Config,
) error {
	// Retrieve context chunks
	var chunks []chat.ContextChunk
	if searcher != nil {
		results, err := searcher.Search(ctx, hybrid.SearchOptions{
			Query:      query,
			Collection: chatCollection,
			Limit:      chatLimit,
			Rerank:     true,
			RerankMode: "token",
		})
		if err == nil {
			for _, r := range results {
				chunks = append(chunks, chat.ContextChunk{
					Source:  r.Source,
					Content: r.Content,
					Score:   r.Score,
				})
			}
		}
	}

	// Build / refresh system prompt with new context on each turn
	systemPrompt := chat.BuildSystemPrompt(chunks, chatSystem)

	// Build messages for this turn
	var msgs []chat.Message
	msgs = append(msgs, chat.Message{Role: "system", Content: systemPrompt})
	// Append history (non-system)
	for _, m := range session.Messages {
		if m.Role != "system" {
			msgs = append(msgs, m)
		}
	}
	msgs = append(msgs, chat.Message{Role: "user", Content: query})

	// Show source citations
	if len(chunks) > 0 && !chatNoContext {
		fmt.Printf("\033[2m📚 Context: ")
		seen := make(map[string]bool)
		for _, c := range chunks {
			title := c.Source
			if idx := strings.Index(title, " ("); idx != -1 {
				title = title[:idx]
			}
			if !seen[title] {
				fmt.Printf("[%s] ", title)
				seen[title] = true
			}
		}
		fmt.Printf("\033[0m\n\n")
	}

	fmt.Print("\033[1;32m axon \033[0m▶ ")

	var reply string
	var replyErr error

	if chatNoStream {
		reply, replyErr = llm.Complete(ctx, msgs)
		if replyErr == nil {
			fmt.Println(reply)
		}
	} else {
		reply, replyErr = llm.CompleteStream(ctx, msgs, os.Stdout)
		fmt.Println()
	}

	if replyErr != nil {
		return replyErr
	}

	// Save to session history
	session.Add("user", query)
	session.Add("assistant", reply)
	return nil
}

func printChatBanner(cfg *config.Config) {
	fmt.Println(`
╔══════════════════════════════════════╗
║  🧠 Axon Chat — RAG Mode            ║
╚══════════════════════════════════════╝`)
	fmt.Printf("  Model  : %s\n", cfg.LLMModel)
	fmt.Printf("  DB     : %s\n", cfg.DBPath)
	if chatCollection != "" {
		fmt.Printf("  Filter : collection=%s\n", chatCollection)
	}
	if chatNoContext {
		fmt.Println("  Context: disabled (pure LLM)")
	} else {
		fmt.Printf("  Context: top-%d chunks per turn\n", chatLimit)
	}
	fmt.Println()
	fmt.Println("  /clear — clear session   /help — show commands   /quit — exit")
}

func printChatHelp() {
	fmt.Println(`
  Commands:
    /clear     Clear conversation history
    /help      Show this help
    /quit      Exit chat
    /exit      Exit chat

  Tips:
    • Ask about anything in your knowledge base
    • Axon retrieves the most relevant context each turn
    • Use --collection to narrow search to a specific collection
    • Use --one-shot "question" for non-interactive use`)
}
