package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hsiaosiyuan0/axon/internal/api"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/spf13/cobra"
)

var (
	serveAddr string
	serveKey  string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Axon HTTP API server",
	Long: `Start a local HTTP REST API server for Axon.

This allows external tools (Claude, Cursor, scripts, other apps) to query
and update your knowledge base over HTTP.

Authentication:
  If --key (or AXON_API_KEY env var) is set, all requests except GET /health
  must include either:
    X-API-Key: <key>
  or:
    Authorization: Bearer <key>

Endpoints:
  GET  /health
  GET  /v1/status
  GET  /v1/collections
  GET  /v1/sources?collection=<name>&limit=<n>
  GET  /v1/query?q=<text>&collection=<name>&limit=<n>
  POST /v1/query   {"q":"...","collection":"...","limit":5}
  POST /v1/add     {"origin":"<url or path>","collection":"..."}
  POST /v1/add     {"text":"...","title":"...","collection":"..."}

Examples:
  axon serve                              # no auth, localhost:7474
  axon serve --key mysecret              # require API key
  axon serve --addr 0.0.0.0:7474        # listen on all interfaces
  axon serve --addr localhost:8080       # custom port

  # With auth:
  curl -H "X-API-Key: mysecret" http://localhost:7474/v1/status
  curl -H "Authorization: Bearer mysecret" http://localhost:7474/v1/status`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		// --key flag overrides AXON_API_KEY env var
		if serveKey != "" {
			cfg.APIKey = serveKey
		}

		srv := api.New(cfg, serveAddr)

		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		fmt.Printf("🦞 Axon API server listening on http://%s\n", serveAddr)
		fmt.Printf("   DB: %s\n", cfg.DBPath)
		fmt.Printf("   UI: http://%s/ui\n", serveAddr)
		if cfg.APIKey != "" {
			fmt.Printf("   Auth: X-API-Key required (%d chars)\n", len(cfg.APIKey))
		} else {
			fmt.Println("   Auth: disabled (set --key or AXON_API_KEY to enable)")
		}
		fmt.Println("   Press Ctrl+C to stop")
		fmt.Println()

		if err := srv.ListenAndServe(ctx); err != nil {
			if err.Error() == "http: Server closed" {
				fmt.Println("\n✅ Server stopped.")
				return nil
			}
			return err
		}
		return nil
	},
}

func init() {
	serveCmd.Flags().StringVar(&serveAddr, "addr", "localhost:7474", "Address to listen on")
	serveCmd.Flags().StringVar(&serveKey, "key", "", "API key for authentication (or set AXON_API_KEY)")
}
