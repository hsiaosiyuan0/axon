//go:build onnx
// +build onnx

package classify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	tokenizers "github.com/daulet/tokenizers"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/embed"
	"github.com/hsiaosiyuan0/axon/internal/modelreg"
)

// nliScorer runs a local NLI cross-encoder ONNX model (nli-deberta-v3-small).
// Input: (premise, hypothesis) pair.
// Output: entailment score (index 1 of logits [contradiction, entailment, neutral]).
type nliScorer struct {
	tokenizer   *tokenizers.Tokenizer
	session     *ort.DynamicAdvancedSession
	inputInfos  []ort.InputOutputInfo
	outputInfos []ort.InputOutputInfo
}

// newNLIScorer loads the NLI model from disk and initialises the ONNX session.
func newNLIScorer(cfg *config.Config) (*nliScorer, error) {
	spec := modelreg.NLIModel()
	if spec == nil {
		return nil, fmt.Errorf("NLI model spec not found in registry")
	}

	modelDir := filepath.Join(cfg.ModelsDir, spec.Name)
	modelPath := filepath.Join(modelDir, "model.onnx")
	tokPath := filepath.Join(modelDir, "tokenizer.json")

	if _, err := os.Stat(modelPath); err != nil {
		return nil, fmt.Errorf("NLI model not found at %s — run `axon model download %s`", modelPath, spec.Name)
	}
	if _, err := os.Stat(tokPath); err != nil {
		return nil, fmt.Errorf("tokenizer.json not found at %s — run `axon model download %s`", tokPath, spec.Name)
	}

	// Reuse the ORT runtime initialised by the embed package
	if err := embed.InitOrtOnce(); err != nil {
		return nil, fmt.Errorf("init ORT: %w", err)
	}

	inputInfos, outputInfos, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("get NLI model info: %w", err)
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
		return nil, fmt.Errorf("create NLI ORT session: %w", err)
	}

	tok, err := tokenizers.FromFile(tokPath)
	if err != nil {
		session.Destroy()
		return nil, fmt.Errorf("load NLI tokenizer: %w", err)
	}

	return &nliScorer{
		tokenizer:   tok,
		session:     session,
		inputInfos:  inputInfos,
		outputInfos: outputInfos,
	}, nil
}

func (s *nliScorer) Close() {
	if s.session != nil {
		s.session.Destroy()
	}
	if s.tokenizer != nil {
		s.tokenizer.Close()
	}
}

// Score returns the entailment logit for the (premise, hypothesis) pair.
// Higher score means the document is more likely to belong to that collection.
func (s *nliScorer) Score(_ context.Context, premise, hypothesis string) (float64, error) {
	// The cross-encoder tokenizer handles the pair via the second argument to Encode.
	// We encode premise + hypothesis together as a sequence pair.
	enc := s.tokenizer.EncodeWithOptions(premise, true,
		tokenizers.WithReturnTypeIDs(),
		tokenizers.WithReturnAttentionMask(),
	)
	encPair := s.tokenizer.EncodeWithOptions(hypothesis, true,
		tokenizers.WithReturnTypeIDs(),
		tokenizers.WithReturnAttentionMask(),
	)

	// Concatenate: [premise tokens] [SEP] [hypothesis tokens]
	// The tokenizer already adds CLS/SEP. We concatenate raw IDs with segment IDs.
	seqLen := len(enc.IDs) + len(encPair.IDs)
	if seqLen == 0 {
		return 0, nil
	}

	inputIDs := make([]int64, seqLen)
	attMask := make([]int64, seqLen)
	tokenTypeIDs := make([]int64, seqLen)

	for j, id := range enc.IDs {
		inputIDs[j] = int64(id)
		attMask[j] = 1
		tokenTypeIDs[j] = 0
	}
	for j, id := range encPair.IDs {
		off := len(enc.IDs)
		inputIDs[off+j] = int64(id)
		attMask[off+j] = 1
		tokenTypeIDs[off+j] = 1
	}

	shape := ort.NewShape(1, int64(seqLen))

	var inputs []ort.Value
	var toDestroy []interface{ Destroy() error }

	for _, info := range s.inputInfos {
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
			return 0, fmt.Errorf("create tensor %s: %w", info.Name, err)
		}
		toDestroy = append(toDestroy, t)
		inputs = append(inputs, t)
	}

	defer func() {
		for _, td := range toDestroy {
			td.Destroy()
		}
	}()

	var outputs []ort.Value
	for range s.outputInfos {
		outT, err := ort.NewEmptyTensor[float32](ort.NewShape(0))
		if err != nil {
			return 0, fmt.Errorf("create output tensor: %w", err)
		}
		toDestroy = append(toDestroy, outT)
		outputs = append(outputs, outT)
	}

	if err := s.session.Run(inputs, outputs); err != nil {
		return 0, fmt.Errorf("NLI inference: %w", err)
	}

	if len(outputs) == 0 {
		return 0, fmt.Errorf("no outputs from NLI model")
	}

	outTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return 0, fmt.Errorf("NLI output tensor is not float32")
	}

	logits := outTensor.GetData()
	// NLI logits: [contradiction(0), entailment(1), neutral(2)]
	// We use entailment score (index 1) as the relevance signal.
	if len(logits) < 2 {
		return 0, fmt.Errorf("unexpected NLI output length: %d", len(logits))
	}
	return float64(logits[1]), nil
}
