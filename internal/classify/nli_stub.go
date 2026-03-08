//go:build !onnx
// +build !onnx

package classify

import (
	"context"
	"fmt"

	"github.com/hsiaosiyuan0/axon/internal/config"
)

// nliScorer is a stub when ONNX is not compiled in.
type nliScorer struct{}

func newNLIScorer(_ *config.Config) (*nliScorer, error) {
	return nil, fmt.Errorf("NLI classification requires the onnx build tag (go build --tags onnx)")
}

func (s *nliScorer) Close() {}

func (s *nliScorer) Score(_ context.Context, _, _ string) (float64, error) {
	return 0, fmt.Errorf("NLI not available without onnx build tag")
}
