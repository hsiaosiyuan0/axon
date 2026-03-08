//go:build onnx

package embed

// model_assets.go — Embeds the built-in bge-small-zh-v1.5 model files
// into the binary and extracts them to ~/.axon/models/ on first use.
//
// The model directory (internal/embed/model/) is populated by scripts/build.sh
// before compilation. Files embedded:
//   - model/model.onnx       (~24 MB, quantized ONNX)
//   - model/tokenizer.json   (~2 MB)

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed model/model.onnx
var builtinModelONNX []byte

//go:embed model/tokenizer.json
var builtinTokenizerJSON []byte

// extractBuiltinModel writes the embedded model files to destDir/bge-small-zh-v1.5/
// if they are not already present. This replaces the network download for the
// built-in default model.
func extractBuiltinModel(modelsDir string) error {
	const modelName = "bge-small-zh-v1.5"
	modelDir := filepath.Join(modelsDir, modelName)

	onnxDest := filepath.Join(modelDir, "model.onnx")
	tokDest := filepath.Join(modelDir, "tokenizer.json")

	// Already extracted?
	if fi, err := os.Stat(onnxDest); err == nil && fi.Size() > 1024*1024 {
		return nil
	}

	fmt.Printf("📦 Extracting built-in model %q from binary…\n", modelName)

	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	if err := os.WriteFile(onnxDest, builtinModelONNX, 0o644); err != nil {
		return fmt.Errorf("write model.onnx: %w", err)
	}
	if err := os.WriteFile(tokDest, builtinTokenizerJSON, 0o644); err != nil {
		return fmt.Errorf("write tokenizer.json: %w", err)
	}

	fmt.Printf("✅ Built-in model ready at %s\n\n", modelDir)
	return nil
}

// hasBuiltinModel returns true when the binary was compiled with the built-in
// model embedded (i.e. builtinModelONNX has real content, not a placeholder).
func hasBuiltinModel() bool {
	return len(builtinModelONNX) > 1024*1024 // must be > 1 MB to be real
}
