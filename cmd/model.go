package cmd

import (
	"fmt"
	"os"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/modelreg"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var modelCmd = &cobra.Command{
	Use:   "model",
	Short: "Manage embedding models",
}

// ── model list ───────────────────────────────────────────────────────────────

var modelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available embedding models",
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

		downloaded, err := db.Models().List()
		if err != nil {
			return err
		}

		// Build a set of installed model names
		installed := make(map[string]store.Model)
		for _, m := range downloaded {
			installed[m.Name] = m
		}

		builtIn := modelreg.BuiltInModel()

		fmt.Printf("\n%-28s %-7s %-6s %-15s %-10s %s\n",
			"NAME", "SIZE", "DIM", "LANG", "STATUS", "DESCRIPTION")
		fmt.Println("─────────────────────────────────────────────────────────────────────────────────────")

		for _, spec := range modelreg.Registry {
			status := "available"
			if spec.BuiltIn {
				if m, ok := installed[spec.Name]; ok && m.IsAvailable {
					status = "✅ ready"
				} else {
					status = "📦 embedded"
				}
			} else {
				if m, ok := installed[spec.Name]; ok && m.IsAvailable {
					status = "✅ ready"
				} else {
					status = "not downloaded"
				}
			}

			tag := ""
			if builtIn != nil && spec.Name == builtIn.Name {
				tag = " (default)"
			}

			fmt.Printf("%-28s %-7s %-6d %-15s %-10s %s%s\n",
				spec.Name,
				fmt.Sprintf("%dMB", spec.SizeMB),
				spec.Dim,
				spec.Lang,
				status,
				spec.Description,
				tag,
			)
		}

		fmt.Println()
		fmt.Println("  ── API Embedding Models (OpenAI-compatible) ──────────────────────────")
		fmt.Println("  Requires embed.provider = api in ~/.axon/config.toml")
		fmt.Println()
		fmt.Printf("  %-40s %-6d %-15s %s\n", "api:text-embedding-3-small", 1536, "multilingual", "OpenAI / compatible (recommended)")
		fmt.Printf("  %-40s %-6d %-15s %s\n", "api:text-embedding-3-large", 3072, "multilingual", "OpenAI / compatible (high quality)")
		fmt.Printf("  %-40s %-6d %-15s %s\n", "api:text-embedding-ada-002", 1536, "multilingual", "OpenAI / compatible (legacy)")
		fmt.Println()
		fmt.Println("  ── Configuration ─────────────────────────────────────────────────────")
		fmt.Println("  axon config init                            # create config file")
		fmt.Println("  axon config set embed.provider  api         # use API embedding")
		fmt.Println("  axon config set embed.api.key   sk-...      # set API key")
		fmt.Println("  axon config set embed.api.model text-embedding-3-small")
		fmt.Println()
		fmt.Println("  ── Examples ──────────────────────────────────────────────────────────")
		fmt.Println("  # Use local ONNX (default, no API key needed):")
		fmt.Println("    axon add myfile.md")
		fmt.Println()
		fmt.Println("  # Use OpenAI API embedding:")
		fmt.Println("    axon config set embed.provider api")
		fmt.Println("    axon config set embed.api.key sk-...")
		fmt.Println("    axon add myfile.md")
		fmt.Println()
		fmt.Println("  # Use a different local model:")
		fmt.Println("    axon model download bge-m3")
		fmt.Println("    axon config set embed.model bge-m3")
		fmt.Println()
		fmt.Println("  Download a model:  axon model download bge-m3")
		fmt.Println("  Use a mirror:      axon model download bge-m3 --mirror hf-mirror")
		fmt.Println("  List mirrors:      axon model mirrors")
		return nil
	},
}

// ── model mirrors ─────────────────────────────────────────────────────────────

var modelMirrorsCmd = &cobra.Command{
	Use:   "mirrors",
	Short: "List available download mirrors",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("\nAvailable download mirrors:")
		fmt.Println()
		for _, m := range modelreg.Mirrors {
			fmt.Printf("  %-15s %s\n", m.Name, m.Description)
			fmt.Printf("                  %s\n", m.BaseURL)
			fmt.Println()
		}
		fmt.Println("Usage:")
		fmt.Println("  axon model download bge-m3 --mirror hf-mirror")
		fmt.Println("  axon model download bge-m3 --mirror https://my-cdn.example.com")
	},
}

// ── model download ───────────────────────────────────────────────────────────

var (
	downloadMirror string
	downloadForce  bool
)

var modelDownloadCmd = &cobra.Command{
	Use:   "download <name>",
	Short: "Download a local ONNX embedding model",
	Long: `Download a local ONNX embedding model for offline use.

Run "axon model list" to see all available models.
Run "axon model mirrors" to see available download mirrors.

Examples:
  axon model download bge-small-zh-v1.5
  axon model download bge-m3
  axon model download bge-m3 --mirror hf-mirror
  axon model download bge-m3 --mirror https://my-cdn.example.com
  axon model download bge-m3 --force

After download, set as default:
  export AXON_DEFAULT_MODEL=bge-m3`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		spec := modelreg.Find(name)
		if spec == nil {
			fmt.Fprintf(os.Stderr, "❌ Unknown model %q\n\n", name)
			fmt.Fprintln(os.Stderr, "Available models:")
			for _, m := range modelreg.Registry {
				fmt.Fprintf(os.Stderr, "  %-28s %s\n", m.Name, m.Description)
			}
			return fmt.Errorf("model not found: %s", name)
		}

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		mirrorDisplay := downloadMirror
		if mirrorDisplay == "" {
			mirrorDisplay = "huggingface (default)"
		}
		fmt.Printf("\n📦 Downloading %s\n", spec.Name)
		fmt.Printf("   Size:    ~%d MB\n", spec.SizeMB)
		fmt.Printf("   Mirror:  %s\n", mirrorDisplay)
		fmt.Printf("   Dest:    %s/%s\n\n", cfg.ModelsDir, spec.Name)

		opts := modelreg.DownloadOptions{
			Mirror: downloadMirror,
			Force:  downloadForce,
		}

		modelDir, err := modelreg.DownloadModel(spec, cfg.ModelsDir, opts)
		if err != nil {
			return fmt.Errorf("download failed: %w", err)
		}

		// Register in DB
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.Models().Upsert(store.Model{
			Name:        spec.Name,
			Version:     "1.0",
			Provider:    "local-onnx",
			Dim:         spec.Dim,
			Lang:        spec.Lang,
			LocalPath:   modelDir,
			IsAvailable: true,
		}); err != nil {
			return fmt.Errorf("register model in DB: %w", err)
		}

		fmt.Printf("\n🎉 Model %q is ready!\n", spec.Name)
		if spec.BuiltIn {
			fmt.Println("   This is now the active default model.")
		} else {
			fmt.Printf("   To use it: export AXON_DEFAULT_MODEL=%s\n", spec.Name)
		}
		return nil
	},
}

// ── model rm ─────────────────────────────────────────────────────────────────

var modelRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a downloaded model",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		// Prevent removing the built-in default
		if builtin := modelreg.BuiltInModel(); builtin != nil && name == builtin.Name {
			return fmt.Errorf("cannot remove built-in default model %q — it will be re-downloaded on next use", name)
		}

		cfg, err := config.Load(globalDB)
		if err != nil {
			return err
		}

		dir := fmt.Sprintf("%s/%s", cfg.ModelsDir, name)
		if _, err := os.Stat(dir); err != nil {
			return fmt.Errorf("model %q not found at %s", name, dir)
		}

		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove model: %w", err)
		}

		// Update DB
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()

		_ = db.Models().Upsert(store.Model{
			Name:        name,
			Provider:    "local-onnx",
			IsAvailable: false,
		})

		fmt.Printf("🗑️  Removed model: %s\n", name)
		return nil
	},
}

// ── model ensure-builtin ──────────────────────────────────────────────────────
// Internal helper command — ensures the built-in default model is downloaded.
// Called automatically on first use.

var modelEnsureBuiltinCmd = &cobra.Command{
	Use:    "ensure-builtin",
	Short:  "Download the built-in default model if not already present",
	Hidden: true, // not shown in help
	RunE: func(cmd *cobra.Command, args []string) error {
		mirror, _ := cmd.Flags().GetString("mirror")
		return ensureBuiltinModel(globalDB, mirror)
	},
}

// ensureBuiltinModel downloads the built-in default model if it's not already
// present on disk. Called automatically by embedder.New() when ONNX is compiled in.
func ensureBuiltinModel(dbPath, mirror string) error {
	spec := modelreg.BuiltInModel()
	if spec == nil {
		return nil
	}

	cfg, err := config.Load(dbPath)
	if err != nil {
		return err
	}

	// Check if already downloaded
	modelPath := fmt.Sprintf("%s/%s/model.onnx", cfg.ModelsDir, spec.Name)
	if fi, err := os.Stat(modelPath); err == nil && fi.Size() > 1024 {
		return nil // already present
	}

	fmt.Printf("⬇️  First-time setup: downloading built-in model %q (~%d MB)…\n", spec.Name, spec.SizeMB)
	if mirror != "" {
		fmt.Printf("   Using mirror: %s\n", mirror)
	} else {
		fmt.Println("   Tip: use --mirror hf-mirror for faster downloads in China")
		fmt.Println("        axon model download", spec.Name, "--mirror hf-mirror")
	}
	fmt.Println()

	opts := modelreg.DownloadOptions{Mirror: mirror}
	modelDir, err := modelreg.DownloadModel(spec, cfg.ModelsDir, opts)
	if err != nil {
		return fmt.Errorf("auto-download built-in model: %w", err)
	}

	// Register in DB
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	return db.Models().Upsert(store.Model{
		Name:        spec.Name,
		Version:     "1.0",
		Provider:    "local-onnx",
		Dim:         spec.Dim,
		Lang:        spec.Lang,
		LocalPath:   modelDir,
		IsAvailable: true,
	})
}

func init() {
	modelDownloadCmd.Flags().StringVar(&downloadMirror, "mirror", "",
		`Download mirror: preset name or full URL.
Presets: huggingface (default), hf-mirror, modelscope
Custom:  https://my-cdn.example.com`)
	modelDownloadCmd.Flags().BoolVar(&downloadForce, "force", false,
		"Re-download even if files already exist")

	modelEnsureBuiltinCmd.Flags().String("mirror", "", "Mirror to use for auto-download")

	modelCmd.AddCommand(modelListCmd)
	modelCmd.AddCommand(modelDownloadCmd)
	modelCmd.AddCommand(modelMirrorsCmd)
	modelCmd.AddCommand(modelRmCmd)
	modelCmd.AddCommand(modelEnsureBuiltinCmd)
}
