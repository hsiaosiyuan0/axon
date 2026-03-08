package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/spf13/cobra"
)

// ── axon config ───────────────────────────────────────────────────────────────

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage Axon configuration",
	Long: `Manage the Axon configuration file (~/.axon/config.toml).

Subcommands:
  axon config show          Print current effective configuration
  axon config path          Print path to config file
  axon config init          Create config file with defaults (if not exists)
  axon config set KEY VALUE Set a config value (e.g. axon config set llm.key sk-...)`,
}

// ── axon config show ──────────────────────────────────────────────────────────

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print current effective configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		maskKey := func(k string) string {
			if k == "" {
				return "(not set)"
			}
			if len(k) <= 8 {
				return "***"
			}
			return k[:4] + "..." + k[len(k)-4:]
		}

		fmt.Println()
		fmt.Println("╔══════════════════════════════════════════════════════════╗")
		fmt.Println("║              Axon Configuration (effective)              ║")
		fmt.Println("╠══════════════════════════════════════════════════════════╣")
		fmt.Printf("║  Config file : %-42s║\n", truncate(cfg.ConfigPath, 42))
		fmt.Printf("║  DB path     : %-42s║\n", truncate(cfg.DBPath, 42))
		fmt.Printf("║  Models dir  : %-42s║\n", truncate(cfg.ModelsDir, 42))
		fmt.Println("╠══════════════════════════════════════════════╣══════════╣")
		fmt.Println("║  [classify]                                              ║")
		fmt.Printf("║    provider  : %-42s║\n", cfg.ClassifyProvider)
		fmt.Println("╠══════════════════════════════════════════════╣══════════╣")
		fmt.Println("║  [embed]                                                 ║")
		fmt.Printf("║    provider  : %-42s║\n", cfg.EmbedProvider)
		fmt.Printf("║    model     : %-42s║\n", cfg.DefaultModel)
		fmt.Println("║  [embed.api]                                             ║")
		fmt.Printf("║    endpoint  : %-42s║\n", truncate(cfg.EmbedAPIEndpoint, 42))
		fmt.Printf("║    key       : %-42s║\n", maskKey(cfg.EmbedAPIKey))
		fmt.Printf("║    model     : %-42s║\n", cfg.EmbedAPIModel)
		fmt.Println("╠══════════════════════════════════════════════╣══════════╣")
		fmt.Println("║  [llm]                                                   ║")
		fmt.Printf("║    endpoint  : %-42s║\n", truncate(cfg.LLMEndpoint, 42))
		fmt.Printf("║    key       : %-42s║\n", maskKey(cfg.LLMAPIKey))
		fmt.Printf("║    model     : %-42s║\n", cfg.LLMModel)
		fmt.Println("╠══════════════════════════════════════════════╣══════════╣")
		fmt.Println("║  [server]                                                ║")
		fmt.Printf("║    api_key   : %-42s║\n", maskKey(cfg.APIKey))
		fmt.Println("╚══════════════════════════════════════════════════════════╝")
		fmt.Println()
		fmt.Printf("  Edit: %s\n\n", cfg.ConfigPath)
		return nil
	},
}

// ── axon config path ──────────────────────────────────────────────────────────

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print path to config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}
		fmt.Println(cfg.ConfigPath)
		return nil
	},
}

// ── axon config init ──────────────────────────────────────────────────────────

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create config file with defaults (if not exists)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		if _, err := os.Stat(cfg.ConfigPath); err == nil {
			fmt.Printf("Config file already exists: %s\n", cfg.ConfigPath)
			fmt.Println("Use `axon config set KEY VALUE` to update individual values.")
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(cfg.ConfigPath), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(cfg.ConfigPath, []byte(defaultConfigTOML), 0o600); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
		fmt.Printf("✅ Created: %s\n", cfg.ConfigPath)
		fmt.Println()
		fmt.Println("Edit the file to configure your API keys and preferred embedding provider.")
		return nil
	},
}

// ── axon config set ───────────────────────────────────────────────────────────

var configSetCmd = &cobra.Command{
	Use:   "set KEY VALUE",
	Short: "Set a config value",
	Long: `Set a config value in ~/.axon/config.toml.

Examples:
  axon config set llm.key         sk-your-openai-key
  axon config set llm.model       gpt-4o
  axon config set llm.endpoint    https://api.openai.com/v1
  axon config set embed.provider  api
  axon config set embed.api.key   sk-your-embed-key
  axon config set embed.api.model text-embedding-3-small`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key := args[0]
		val := args[1]

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		// Validate key
		validKeys := map[string]bool{
			"db.path": true, "db.models_dir": true, "db.plugins_dir": true,
			"classify.provider": true,
			"embed.provider":    true, "embed.model": true,
			"embed.api.endpoint": true, "embed.api.key": true, "embed.api.model": true,
			"llm.endpoint": true, "llm.key": true, "llm.model": true,
			"server.api_key": true,
		}
		if !validKeys[key] {
			return fmt.Errorf("unknown config key %q\nValid keys: %s",
				key, validKeyList(validKeys))
		}

		cfgPath := cfg.ConfigPath

		// Ensure file exists
		if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(cfgPath, []byte(defaultConfigTOML), 0o600); err != nil {
				return fmt.Errorf("create config: %w", err)
			}
		}

		if err := config.SetValue(cfgPath, key, val); err != nil {
			return err
		}

		// Mask keys for display
		display := val
		if strings.HasSuffix(key, ".key") || strings.HasSuffix(key, "_key") {
			if len(val) > 8 {
				display = val[:4] + "..." + val[len(val)-4:]
			} else {
				display = "***"
			}
		}
		fmt.Printf("✅ Set %s = %s  (%s)\n", key, display, cfgPath)
		return nil
	},
}

// ── helpers ───────────────────────────────────────────────────────────────────

func validKeyList(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}

// ── default config template ───────────────────────────────────────────────────

const defaultConfigTOML = `# Axon configuration file
# Generated by: axon config init
# Reference   : https://github.com/hsiaosiyuan0/axon

# ── Database ──────────────────────────────────────────────────────────────────
[db]
# path = "~/.axon/axon.db"          # default
# models_dir = "~/.axon/models"     # default
# plugins_dir = "~/.axon/plugins"   # default

# ── Collection Classification ─────────────────────────────────────────────────
[classify]
# provider = "llm"          # llm (default, ~85-90% accuracy, requires API key)
#                           # nli (local, ~80% accuracy, downloads ~44 MB model)
#                           # bge-cosine (local, ~65% accuracy, uses built-in model)

# ── Embedding ─────────────────────────────────────────────────────────────────
[embed]
# Embedding backend: onnx (default, offline) | api | purego (zero-dep)
provider = "onnx"

# Local ONNX model name (used when provider = "onnx")
# model = "bge-small-zh-v1.5"       # default

[embed.api]
# Settings used when provider = "api"
# endpoint = "https://api.openai.com/v1"
# model    = "text-embedding-3-small"
# key      = ""                      # required for api provider

# ── LLM ───────────────────────────────────────────────────────────────────────
[llm]
# endpoint = "https://api.openai.com/v1"
# model    = "gpt-4o-mini"
# key      = ""                      # set your API key here (also used for classify.provider = llm)

# ── HTTP API Server ───────────────────────────────────────────────────────────
[server]
# api_key = ""                       # leave empty to disable auth
`

// ── register ──────────────────────────────────────────────────────────────────

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configPathCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configSetCmd)
}
