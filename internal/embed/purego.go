package embed

import (
	"context"
	"math"

	"github.com/hsiaosiyuan0/axon/internal/tokenize"
)

// PureGoEmbedder is a pure-Go TF-IDF based embedder.
// It provides zero-dependency embedding as a fallback.
// Dim is fixed at 512 (hash-based feature space).
// Quality is lower than neural models but works everywhere.
type PureGoEmbedder struct{}

const pureGoDim = 512

func NewPureGoEmbedder() (*PureGoEmbedder, error) {
	return &PureGoEmbedder{}, nil
}

func (e *PureGoEmbedder) ModelName() string { return "purego:tfidf-512" }
func (e *PureGoEmbedder) Provider() string  { return "local-go" }
func (e *PureGoEmbedder) Dim() int          { return pureGoDim }

func (e *PureGoEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i, text := range texts {
		result[i] = e.embed(text)
	}
	return result, nil
}

// embed converts text to a 512-dim float32 vector using character n-gram hashing.
func (e *PureGoEmbedder) embed(text string) []float32 {
	vec := make([]float32, pureGoDim)

	tokens := tokenize.Words(text)
	if len(tokens) == 0 {
		return vec
	}

	// Term frequency
	tf := make(map[string]float64)
	for _, t := range tokens {
		tf[t]++
	}

	// Project each term into vector space via hash
	for term, freq := range tf {
		weight := math.Log(1 + freq)
		// Character n-grams (1-3)
		ngrams := charNgrams(term, 3)
		for _, ng := range ngrams {
			idx := fnv32(ng) % uint32(pureGoDim)
			vec[idx] += float32(weight)
		}
	}

	// L2 normalize
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] /= float32(norm)
		}
	}
	return vec
}

func charNgrams(s string, maxN int) []string {
	runes := []rune(s)
	var result []string
	for n := 1; n <= maxN; n++ {
		for i := 0; i <= len(runes)-n; i++ {
			result = append(result, string(runes[i:i+n]))
		}
	}
	return result
}

func fnv32(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}
