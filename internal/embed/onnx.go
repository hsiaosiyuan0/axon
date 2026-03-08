//go:build onnx
// +build onnx

package embed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tokenizers "github.com/daulet/tokenizers"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/hsiaosiyuan0/axon/internal/config"
)

// ONNXEmbedder runs a local ONNX model (e.g. bge-small-en-v1.5, bge-m3) for embedding.
//
// This embedder requires the `onnx` build tag:
//
//	go build --tags onnx ./...
//
// And model files downloaded via:
//
//	axon model download bge-small-en-v1.5
type ONNXEmbedder struct {
	modelName   string
	modelDir    string
	dim         int
	tokenizer   *tokenizers.Tokenizer
	session     *ort.DynamicAdvancedSession
	inputInfos  []ort.InputOutputInfo
	outputInfos []ort.InputOutputInfo
}

func NewONNXEmbedder(modelName string, cfg *config.Config) (*ONNXEmbedder, error) {
	name := modelName
	if len(name) > 5 && name[:5] == "onnx:" {
		name = name[5:]
	}

	modelDir := filepath.Join(cfg.ModelsDir, name)
	modelPath := filepath.Join(modelDir, "model.onnx")
	tokPath := filepath.Join(modelDir, "tokenizer.json")

	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("ONNX model not found at %s — run `axon model download %s`", modelPath, name)
	}
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("tokenizer.json not found at %s — run `axon model download %s`", tokPath, name)
	}

	// Suppress ORT stderr noise during initialization (version mismatch messages, etc.)
	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if devNull != nil {
		origStderr := os.Stderr
		os.Stderr = devNull
		err := InitOrtOnce()
		os.Stderr = origStderr
		devNull.Close()
		if err != nil {
			return nil, err
		}
	} else if err := InitOrtOnce(); err != nil {
		return nil, err
	}

	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("get model info: %w", err)
	}

	inputNames := make([]string, len(inputInfos))
	outputNames := make([]string, len(outputInfos))
	for i, info := range inputInfos {
		inputNames[i] = info.Name
	}
	for i, info := range outputInfos {
		outputNames[i] = info.Name
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, inputNames, outputNames, nil)
	if err != nil {
		return nil, fmt.Errorf("create ort session: %w", err)
	}

	tok, err := tokenizers.FromFile(tokPath)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}

	dim := inferDim(name, outputInfos)

	return &ONNXEmbedder{
		modelName:   modelName,
		modelDir:    modelDir,
		dim:         dim,
		tokenizer:   tok,
		session:     session,
		inputInfos:  inputInfos,
		outputInfos: outputInfos,
	}, nil
}

func (e *ONNXEmbedder) ModelName() string { return e.modelName }
func (e *ONNXEmbedder) Provider() string  { return "local-onnx" }
func (e *ONNXEmbedder) Dim() int          { return e.dim }

func (e *ONNXEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		vec, err := e.embedOne(text)
		if err != nil {
			return nil, fmt.Errorf("embed[%d]: %w", i, err)
		}
		results[i] = vec
	}
	return results, nil
}

func (e *ONNXEmbedder) embedOne(text string) ([]float32, error) {
	enc := e.tokenizer.EncodeWithOptions(text, true,
		tokenizers.WithReturnTypeIDs(),
		tokenizers.WithReturnAttentionMask(),
	)

	seqLen := len(enc.IDs)
	if seqLen == 0 {
		return make([]float32, e.dim), nil
	}

	inputIDs := make([]int64, seqLen)
	attMask := make([]int64, seqLen)
	tokenTypeIDs := make([]int64, seqLen)
	for j := 0; j < seqLen; j++ {
		inputIDs[j] = int64(enc.IDs[j])
		attMask[j] = int64(enc.AttentionMask[j])
		if j < len(enc.TypeIDs) {
			tokenTypeIDs[j] = int64(enc.TypeIDs[j])
		}
	}

	shape := ort.NewShape(1, int64(seqLen))

	var inputs []ort.Value
	var toDestroy []interface{ Destroy() error }

	for _, info := range e.inputInfos {
		var data []int64
		switch info.Name {
		case "input_ids":
			data = inputIDs
		case "attention_mask":
			data = attMask
		case "token_type_ids":
			data = tokenTypeIDs
		default:
			data = make([]int64, seqLen)
		}
		t, err := ort.NewTensor(shape, data)
		if err != nil {
			for _, td := range toDestroy {
				td.Destroy()
			}
			return nil, fmt.Errorf("create tensor %s: %w", info.Name, err)
		}
		toDestroy = append(toDestroy, t)
		inputs = append(inputs, t)
	}

	defer func() {
		for _, td := range toDestroy {
			td.Destroy()
		}
	}()

	// Pre-allocate output tensors
	var outputs []ort.Value
	for range e.outputInfos {
		outT, err := ort.NewEmptyTensor[float32](ort.NewShape(0))
		if err != nil {
			return nil, fmt.Errorf("create output tensor: %w", err)
		}
		toDestroy = append(toDestroy, outT)
		outputs = append(outputs, outT)
	}

	if err := e.session.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("ort inference: %w", err)
	}

	if len(outputs) == 0 {
		return nil, fmt.Errorf("no outputs from model")
	}

	outTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("output tensor is not float32")
	}

	data := outTensor.GetData()
	outShape := outTensor.GetShape()

	var vec []float32
	switch len(outShape) {
	case 2:
		vec = make([]float32, len(data))
		copy(vec, data)
	case 3:
		dim := int(outShape[2])
		vec = meanPool(data, seqLen, dim, attMask)
	default:
		return nil, fmt.Errorf("unexpected output shape: %v", outShape)
	}

	result := l2Normalize(vec)
	e.dim = len(result)
	return result, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func inferDim(name string, outputs []ort.InputOutputInfo) int {
	if len(outputs) > 0 {
		shape := outputs[0].Dimensions
		if len(shape) >= 2 {
			last := int(shape[len(shape)-1])
			if last > 0 {
				return last
			}
		}
	}
	if containsStr(name, "bge-m3") {
		return 1024
	}
	if containsStr(name, "large") {
		return 1024
	}
	if containsStr(name, "small") {
		return 384
	}
	return 768
}

func meanPool(data []float32, seqLen, dim int, mask []int64) []float32 {
	vec := make([]float32, dim)
	var totalMask float32
	for i := 0; i < seqLen && i < len(mask); i++ {
		if mask[i] == 0 {
			continue
		}
		totalMask += float32(mask[i])
		for d := 0; d < dim; d++ {
			idx := i*dim + d
			if idx < len(data) {
				vec[d] += data[idx] * float32(mask[i])
			}
		}
	}
	if totalMask > 0 {
		for d := range vec {
			vec[d] /= totalMask
		}
	}
	return vec
}

func l2Normalize(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	if norm == 0 {
		return v
	}
	norm = sqrtf64(norm)
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = float32(float64(x) / norm)
	}
	return out
}

func sqrtf64(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x / 2
	for i := 0; i < 20; i++ {
		z -= (z*z - x) / (2 * z)
	}
	return z
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
