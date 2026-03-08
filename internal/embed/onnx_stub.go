//go:build !onnx
// +build !onnx

package embed

import (
	"context"
	"fmt"

	"github.com/hsiaosiyuan0/axon/internal/config"
)

// ONNXEmbedder is a stub when built without the `onnx` build tag.
// To enable real ONNX support: go build --tags onnx ./...
type ONNXEmbedder struct{}

func NewONNXEmbedder(modelName string, cfg *config.Config) (*ONNXEmbedder, error) {
	return nil, fmt.Errorf(
		"ONNX support not compiled — rebuild with: go build --tags onnx ./...\n" +
			"  Requires: libtokenizers (Rust) + onnxruntime shared library",
	)
}

func (e *ONNXEmbedder) ModelName() string { return "" }
func (e *ONNXEmbedder) Provider() string  { return "local-onnx" }
func (e *ONNXEmbedder) Dim() int          { return 0 }
func (e *ONNXEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, fmt.Errorf("ONNX not available (build with --tags onnx)")
}
