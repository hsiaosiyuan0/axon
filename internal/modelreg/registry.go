// Package modelreg defines the built-in model registry and download helpers.
//
// Built-in model:
//   - bge-small-zh-v1.5  (24 MB, Chinese + multilingual, 512-dim)
//     This model is the default local model. On first use axon will auto-download
//     it from the configured mirror so users don't need any extra steps.
//
// Users can download additional models with:
//
//	axon model download bge-m3
//	axon model download bge-m3 --mirror hf-mirror
//	axon model download bge-m3 --mirror https://my-cdn.example.com
package modelreg

import "fmt"

// ModelSpec describes a downloadable ONNX embedding model.
type ModelSpec struct {
	// Name is the canonical model ID used in axon (e.g. "bge-small-zh-v1.5").
	Name string

	// HFRepo is the HuggingFace model repository (e.g. "BAAI/bge-small-zh-v1.5").
	HFRepo string

	// OnnxPath is the path inside the repo for the ONNX model file.
	OnnxPath string

	// TokenizerPath is the path inside the repo for tokenizer.json.
	TokenizerPath string

	// Dim is the output embedding dimension (0 for classification models).
	Dim int

	// Lang describes supported languages.
	Lang string

	// SizeMB is the approximate size of the ONNX model file in MB.
	SizeMB int

	// Description is a human-readable summary.
	Description string

	// BuiltIn marks this as the default bundled model (auto-downloaded on first use).
	BuiltIn bool

	// ModelType describes the model's architecture/task.
	// Values: "embedding" (default), "nli-classifier"
	ModelType string
}

// Registry is the list of all models axon knows about.
var Registry = []ModelSpec{
	// ── Built-in default (embedded in binary via go:embed) ────────────────
	{
		Name:          "bge-small-zh-v1.5",
		HFRepo:        "Xenova/bge-small-zh-v1.5",
		OnnxPath:      "onnx/model_quantized.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           512,
		Lang:          "zh",
		SizeMB:        24,
		Description:   "BAAI bge-small-zh-v1.5 (quantized) — lightweight Chinese+multilingual model, 512-dim (~24 MB). Built-in.",
		BuiltIn:       true,
	},
	// ── Additional downloadable models ────────────────────────────────────
	{
		Name:          "bge-small-en-v1.5",
		HFRepo:        "BAAI/bge-small-en-v1.5",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           384,
		Lang:          "en",
		SizeMB:        33,
		Description:   "BAAI bge-small-en-v1.5 — lightweight English model, 384-dim (~33 MB).",
	},
	{
		Name:          "bge-base-zh-v1.5",
		HFRepo:        "BAAI/bge-base-zh-v1.5",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           768,
		Lang:          "zh",
		SizeMB:        98,
		Description:   "BAAI bge-base-zh-v1.5 — mid-size Chinese model, 768-dim (~98 MB).",
	},
	{
		Name:          "bge-large-zh-v1.5",
		HFRepo:        "BAAI/bge-large-zh-v1.5",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           1024,
		Lang:          "zh",
		SizeMB:        326,
		Description:   "BAAI bge-large-zh-v1.5 — high-quality Chinese model, 1024-dim (~326 MB).",
	},
	{
		Name:          "bge-m3",
		HFRepo:        "BAAI/bge-m3",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           1024,
		Lang:          "multilingual",
		SizeMB:        570,
		Description:   "BAAI bge-m3 — state-of-the-art multilingual model, 1024-dim (~570 MB).",
	},
	{
		Name:          "e5-small-v2",
		HFRepo:        "intfloat/e5-small-v2",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           384,
		Lang:          "en",
		SizeMB:        33,
		Description:   "intfloat e5-small-v2 — compact English model, 384-dim (~33 MB).",
	},
	// ── NLI classification models (for local collection classification) ───
	{
		Name:          "nli-deberta-v3-small",
		HFRepo:        "cross-encoder/nli-deberta-v3-small",
		OnnxPath:      "onnx/model.onnx",
		TokenizerPath: "tokenizer.json",
		Dim:           0,
		Lang:          "multilingual",
		SizeMB:        44,
		Description:   "cross-encoder/nli-deberta-v3-small — multilingual NLI zero-shot classifier (~44 MB). Used for local collection classification.",
		ModelType:     "nli-classifier",
	},
}

// Find returns the ModelSpec for the given name, or nil.
func Find(name string) *ModelSpec {
	for i := range Registry {
		if Registry[i].Name == name {
			return &Registry[i]
		}
	}
	return nil
}

// BuiltInModel returns the default built-in ModelSpec.
func BuiltInModel() *ModelSpec {
	for i := range Registry {
		if Registry[i].BuiltIn {
			return &Registry[i]
		}
	}
	return nil
}

// NLIModel returns the ModelSpec for the built-in NLI classifier.
func NLIModel() *ModelSpec {
	return Find("nli-deberta-v3-small")
}

// ── Mirror registry ────────────────────────────────────────────────────────

// MirrorPreset represents a named download mirror.
type MirrorPreset struct {
	Name        string
	BaseURL     string // base URL prefix for HuggingFace repos
	Description string
}

// Mirrors lists all built-in mirror presets.
// Users can also pass a full URL to --mirror for custom mirrors.
var Mirrors = []MirrorPreset{
	{
		Name:        "huggingface",
		BaseURL:     "https://huggingface.co",
		Description: "Official HuggingFace Hub (default)",
	},
	{
		Name:        "hf-mirror",
		BaseURL:     "https://hf-mirror.com",
		Description: "hf-mirror.com — HuggingFace mirror for China mainland",
	},
	{
		Name:        "modelscope",
		BaseURL:     "https://modelscope.cn/models",
		Description: "ModelScope (Alibaba) — China CDN, good for BAAI models",
	},
}

// FindMirror returns the base URL for a named mirror or a custom URL.
// If name matches a known preset, returns its BaseURL.
// If name looks like a full URL (starts with http), returns it directly.
// Otherwise returns an error.
func FindMirror(name string) (string, error) {
	if name == "" {
		return Mirrors[0].BaseURL, nil // default: huggingface
	}
	// Custom URL
	if len(name) > 4 && (name[:7] == "http://" || name[:8] == "https://") {
		return name, nil
	}
	for _, m := range Mirrors {
		if m.Name == name {
			return m.BaseURL, nil
		}
	}
	return "", fmt.Errorf("unknown mirror %q — use a preset name or a full https:// URL\n"+
		"Available presets: huggingface, hf-mirror, modelscope", name)
}

// ResolveFileURL builds the download URL for a specific file in a HuggingFace repo.
// baseURL examples:
//
//	"https://huggingface.co"    → https://huggingface.co/{repo}/resolve/main/{path}
//	"https://hf-mirror.com"     → https://hf-mirror.com/{repo}/resolve/main/{path}
//	"https://modelscope.cn/models" → https://modelscope.cn/models/{repo}/resolve/master/{path}
func ResolveFileURL(baseURL, hfRepo, filePath string) string {
	// ModelScope uses a slightly different URL structure
	if len(baseURL) >= 26 && baseURL[:26] == "https://modelscope.cn/mode" {
		// https://modelscope.cn/models/{org}/{name}/resolve/master/{file}
		return fmt.Sprintf("%s/%s/resolve/master/%s", baseURL, hfRepo, filePath)
	}
	// Standard HuggingFace-compatible mirrors
	return fmt.Sprintf("%s/%s/resolve/main/%s", baseURL, hfRepo, filePath)
}
