package cmd

import (
	"fmt"
	"os"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/extplugin"
	"github.com/spf13/cobra"
)

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage external plugins",
	Long: `List, inspect, and test external Axon plugins.

External plugins are standalone executables placed in the plugins directory
(default: ~/.axon/plugins/). They must:

  1. Be named with prefix "axon-plugin-" or "axon_plugin_"
  2. Be executable (chmod +x)
  3. Accept a JSON-RPC request on stdin
  4. Write a JSON-RPC response to stdout

Protocol (single line JSON per request/response):
  Request:  {"method":"describe","params":{}}
  Response: {"result":{"source_type":"notion","description":"Notion importer"}}

Supported methods: describe, fetch, has_changed, relations

Example plugin directory: ~/.axon/plugins/
  axon-plugin-notion    ← Notion page importer
  axon-plugin-confluence ← Confluence importer

SDK:
  Use extplugin.Run(extplugin.Handler{...}) in Go plugins.
  Any language that can read stdin/write stdout works.`,
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed external plugins",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		mgr, err := extplugin.NewManager(cfg.PluginsDir)
		if err != nil {
			return err
		}

		plugins := mgr.List()
		if len(plugins) == 0 {
			fmt.Printf("No external plugins found in %s\n", cfg.PluginsDir)
			fmt.Println("\nTo install a plugin:")
			fmt.Printf("  cp my-plugin %s/axon-plugin-myname\n", cfg.PluginsDir)
			fmt.Printf("  chmod +x %s/axon-plugin-myname\n", cfg.PluginsDir)
			return nil
		}

		fmt.Printf("%-20s  %s\n", "SOURCE TYPE", "DESCRIPTION")
		fmt.Printf("%-20s  %s\n", "───────────", "───────────")
		for _, p := range plugins {
			d := p.Describe()
			fmt.Printf("%-20s  %s\n", d.SourceType, d.Description)
		}
		return nil
	},
}

var pluginTestCmd = &cobra.Command{
	Use:   "test <plugin-path> <origin>",
	Short: "Test a plugin by fetching an origin",
	Long: `Run a plugin's fetch method and print the result.

Examples:
  axon plugin test ~/.axon/plugins/axon-plugin-notion "notion://page/abc123"
  axon plugin test ./my-plugin /path/to/file`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		pluginPath := args[0]
		origin := args[1]

		// Expand ~
		if len(pluginPath) >= 2 && pluginPath[:2] == "~/" {
			home, _ := os.UserHomeDir()
			pluginPath = home + "/" + pluginPath[2:]
		}

		ep, err := extplugin.NewExternalPlugin(pluginPath)
		if err != nil {
			return fmt.Errorf("load plugin: %w", err)
		}

		desc := ep.Describe()
		fmt.Printf("🔌 Plugin: %s (%s)\n\n", desc.Description, desc.SourceType)

		fmt.Printf("📥 Fetching: %s\n", origin)
		data, err := ep.Fetch(cmd.Context(), origin, nil)
		if err != nil {
			return fmt.Errorf("fetch: %w", err)
		}

		fmt.Printf("✅ Success!\n")
		fmt.Printf("   Title   : %s\n", data.Title)
		fmt.Printf("   MIME    : %s\n", data.RawMime)
		fmt.Printf("   Lang    : %s\n", data.Lang)
		fmt.Printf("   Bytes   : %d raw, %d text\n", len(data.RawContent), len(data.PlainText))
		if len(data.PlainText) > 0 {
			preview := data.PlainText
			if len(preview) > 300 {
				preview = preview[:300] + "..."
			}
			fmt.Printf("\n   Preview:\n%s\n", preview)
		}
		return nil
	},
}

var pluginDirCmd = &cobra.Command{
	Use:   "dir",
	Short: "Print the plugins directory path",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}
		fmt.Println(cfg.PluginsDir)
		return nil
	},
}

func init() {
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginTestCmd)
	pluginCmd.AddCommand(pluginDirCmd)
}
