//go:build !onnx

package embed

// model_assets_stub.go — stub for non-ONNX builds.
// When compiled without the onnx tag, no model is embedded.

func extractBuiltinModel(_ string) error { return nil }
func hasBuiltinModel() bool              { return false }
