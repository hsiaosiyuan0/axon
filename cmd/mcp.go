package cmd

import (
	"fmt"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start MCP server (stdio, for Claude/Cursor integration)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		fmt.Fprintln(cmd.ErrOrStderr(), "🔧 Axon MCP server started (stdio)")
		return mcp.Serve(cmd.Context(), cfg)
	},
}
