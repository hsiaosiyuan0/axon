//go:build !onnx
// +build !onnx

package embed

// runtime.go stubs — only used when built with --tags onnx.
// These are here so non-onnx builds don't break.

const OrtVersion = ""

func FindOrtLib() (string, error)  { return "", nil }
func InitOrtOnce() error           { return nil }
func OrtLibURL() (string, string, error) { return "", "", nil }
func DefaultLibDir() string        { return "" }
