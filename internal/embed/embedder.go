package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/modelreg"
	"github.com/hsiaosiyuan0/axon/internal/store"
)

// Embedder computes vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dim() int
	ModelName() string
	Provider() string
}

// New returns the appropriate Embedder based on config.
//
// Provider resolution order:
//  1. AXON_EMBED_PROVIDER=api    → API embedder (OpenAI-compatible)
//  2. AXON_EMBED_PROVIDER=purego → pure-Go TF-IDF (zero deps)
//  3. AXON_EMBED_PROVIDER=onnx   → local ONNX model (explicit)
//  4. AXON_DEFAULT_MODEL=api:*   → API embedder
//  5. AXON_DEFAULT_MODEL=purego  → pure-Go TF-IDF
//  6. (default)                  → local ONNX model (bge-small-zh-v1.5)
//
// The default provider is "onnx" (local ONNX model).
// Set AXON_EMBED_PROVIDER=api to use an OpenAI-compatible embedding API.
func New(modelName string, cfg *config.Config) (Embedder, error) {
	if modelName == "" {
		modelName = cfg.DefaultModel
	}

	// ── Resolve provider ──────────────────────────────────────────────────────
	provider := cfg.EmbedProvider // explicit override

	// Infer from model name prefix if no explicit provider
	if provider == "" {
		switch {
		case strings.HasPrefix(modelName, "api:"):
			provider = "api"
		case modelName == "purego" || modelName == "purego:tfidf-512":
			provider = "purego"
		default:
			provider = "onnx"
		}
	}

	// ── Dispatch to provider ──────────────────────────────────────────────────
	switch provider {
	case "api":
		return newAPIProvider(modelName, cfg)
	case "purego":
		return NewPureGoEmbedder()
	default: // "onnx" or any unrecognised value
		return newONNXProvider(modelName, cfg)
	}
}

// ── Provider constructors ─────────────────────────────────────────────────────

// newAPIProvider creates an API embedder.
// Uses cfg.EmbedAPIKey / EmbedAPIEndpoint / EmbedAPIModel.
// Accepts model name variants:
//   - "api:text-embedding-3-small"  (from AXON_DEFAULT_MODEL)
//   - "text-embedding-3-small"      (stripped prefix)
//   - ""                            (falls back to cfg.EmbedAPIModel)
func newAPIProvider(modelName string, cfg *config.Config) (Embedder, error) {
	// Determine the API key to use
	apiKey := cfg.EmbedAPIKey
	if apiKey == "" {
		return nil, fmt.Errorf(
			"embedding API requires an API key.\n" +
				"  Set AXON_EMBED_API_KEY=<your-key>  (dedicated embed key)\n" +
				"  or  AXON_LLM_API_KEY=<your-key>   (shared LLM key)")
	}

	// Resolve the actual model name sent to the API
	apiModel := strings.TrimPrefix(modelName, "api:")
	if apiModel == "" || apiModel == "api" {
		apiModel = cfg.EmbedAPIModel // e.g. "text-embedding-3-small"
	}

	// Build a temporary config that points to the embed endpoint + key
	embedCfg := *cfg
	embedCfg.LLMEndpoint = cfg.EmbedAPIEndpoint
	embedCfg.LLMAPIKey = apiKey

	return NewAPIEmbedder("api:"+apiModel, &embedCfg)
}

// newONNXProvider loads a local ONNX model.
// Falls back to PureGo if ONNX is not compiled in or model files are missing.
func newONNXProvider(modelName string, cfg *config.Config) (Embedder, error) {
	// Strip "onnx:" prefix if present
	onnxName := strings.TrimPrefix(modelName, "onnx:")
	if onnxName == "" {
		onnxName = cfg.DefaultModel
	}
	onnxName = strings.TrimPrefix(onnxName, "onnx:")

	// Auto-download built-in model if missing
	if err := maybeAutoDownload(onnxName, cfg); err != nil {
		fmt.Printf("⚠️  Auto-download failed (%v), falling back to PureGo embedder\n", err)
		return NewPureGoEmbedder()
	}

	e, err := NewONNXEmbedder(onnxName, cfg)
	if err != nil {
		switch {
		case strings.Contains(err.Error(), "not compiled"):
			// Silent: normal for builds without --tags onnx
		case strings.Contains(err.Error(), "not found"):
			fmt.Printf("⚠️  Model %q not downloaded yet.\n", onnxName)
			fmt.Printf("   Run: axon model download %s\n", onnxName)
			fmt.Printf("   Falling back to PureGo embedder.\n")
		case strings.Contains(err.Error(), "API version") || strings.Contains(err.Error(), "ORT Version"):
			// ONNX Runtime version mismatch — silent fallback
		default:
			fmt.Printf("⚠️  ONNX unavailable (%v), falling back to PureGo embedder\n", err)
		}
		return NewPureGoEmbedder()
	}
	return e, nil
}

// ── Auto-download ─────────────────────────────────────────────────────────────

// maybeAutoDownload ensures the built-in default model is available on disk.
//
// Priority:
//  1. Already on disk → do nothing
//  2. Embedded in binary (hasBuiltinModel) → extract from binary (fast, offline)
//  3. Not embedded → download from network (fallback for non-embedded builds)
//
// For non-built-in models this is a no-op (user must call `axon model download`).
func maybeAutoDownload(modelName string, cfg *config.Config) error {
	builtin := modelreg.BuiltInModel()
	if builtin == nil {
		return nil
	}
	if modelName != builtin.Name {
		return nil // only manage the built-in default here
	}

	// Check if already on disk
	modelPath := filepath.Join(cfg.ModelsDir, modelName, "model.onnx")
	if fi, err := os.Stat(modelPath); err == nil && fi.Size() > 1024*1024 {
		return nil // already present
	}

	// ── Try embedded binary first ─────────────────────────────────────────
	if hasBuiltinModel() {
		if err := extractBuiltinModel(cfg.ModelsDir); err != nil {
			return fmt.Errorf("extract built-in model: %w", err)
		}
		registerModel(builtin, filepath.Join(cfg.ModelsDir, builtin.Name), cfg)
		return nil
	}

	// ── Fallback: network download ────────────────────────────────────────
	fmt.Printf("⬇️  First-time setup: downloading built-in model %q (~%d MB)…\n",
		builtin.Name, builtin.SizeMB)
	fmt.Println("   Tip: use --mirror hf-mirror for faster downloads in mainland China")
	fmt.Println()

	opts := modelreg.DownloadOptions{}
	modelDir, err := modelreg.DownloadModel(builtin, cfg.ModelsDir, opts)
	if err != nil {
		return err
	}
	registerModel(builtin, modelDir, cfg)
	fmt.Printf("✅ Model %q ready\n\n", builtin.Name)
	return nil
}

// registerModel records a model in the DB (best-effort, errors are ignored).
func registerModel(spec *modelreg.ModelSpec, modelDir string, cfg *config.Config) {
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return
	}
	defer db.Close()
	_ = db.Models().Upsert(store.Model{
		Name:        spec.Name,
		Version:     "1.0",
		Provider:    "local-onnx",
		Dim:         spec.Dim,
		Lang:        spec.Lang,
		LocalPath:   modelDir,
		IsAvailable: true,
	})
}
