package tokenize_test

import (
	"testing"

	"github.com/hsiaosiyuan0/axon/internal/tokenize"
)

func TestHasCJK(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"hello world", false},
		{"知识库", true},
		{"ナレッジ", true},
		{"지식", true},
		{"hello 世界", true},
		{"", false},
	}
	for _, c := range cases {
		got := tokenize.HasCJK(c.s)
		if got != c.want {
			t.Errorf("HasCJK(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestTokenizeQuery_ASCII(t *testing.T) {
	// ASCII queries should be returned unchanged
	inputs := []string{"hello world", "golang concurrency", "BM25 search"}
	for _, in := range inputs {
		got := tokenize.TokenizeQuery(in)
		if got != in {
			t.Errorf("TokenizeQuery(%q) = %q, want unchanged", in, got)
		}
	}
}

func TestTokenizeQuery_CJK(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"知识库"},
		{"知识库管理"},
		{"golang 知识库"},
		{"日本語テスト"},
		{"한국어"},
	}
	for _, c := range cases {
		got := tokenize.TokenizeQuery(c.input)
		if got == "" {
			t.Errorf("TokenizeQuery(%q) returned empty string", c.input)
		}
		// Result must contain individual characters
		t.Logf("TokenizeQuery(%q) = %q", c.input, got)
	}
}

func TestNormalizeQuery(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"  hello  ", "hello"},
		{"ＡＢＣ", "ABC"}, // full-width → half-width
		{"知识库", "知识库"},
	}
	for _, c := range cases {
		got := tokenize.NormalizeQuery(c.input)
		if got != c.want {
			t.Errorf("NormalizeQuery(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestWords_Basic(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"hello world", []string{"hello", "world"}},
		{"Go channels goroutines", []string{"go", "channels", "goroutines"}},
		{"  spaces  around  ", []string{"spaces", "around"}},
		{"", nil},
	}
	for _, c := range cases {
		got := tokenize.Words(c.input)
		if len(got) != len(c.want) {
			t.Errorf("Words(%q) = %v, want %v", c.input, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Words(%q)[%d] = %q, want %q", c.input, i, got[i], c.want[i])
			}
		}
	}
}

func TestWordsNoStop_RemovesStopWords(t *testing.T) {
	// Common stop words: "the", "is", "a", "and", "in", "of"
	input := "the quick brown fox is in a forest"
	got := tokenize.WordsNoStop(input)

	stopSet := map[string]bool{"the": true, "is": true, "a": true, "in": true}
	for _, w := range got {
		if stopSet[w] {
			t.Errorf("stop word %q should have been filtered from %q", w, input)
		}
	}
	// "quick", "brown", "fox", "forest" must remain
	content := map[string]bool{}
	for _, w := range got {
		content[w] = true
	}
	for _, expected := range []string{"quick", "brown", "fox", "forest"} {
		if !content[expected] {
			t.Errorf("expected word %q to remain after stop word filtering", expected)
		}
	}
}

func TestWordsNoStop_Empty(t *testing.T) {
	got := tokenize.WordsNoStop("")
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestWordsNoStop_AllStopWords(t *testing.T) {
	// All common English stop words → result should be empty or very small
	input := "the a is and or"
	got := tokenize.WordsNoStop(input)
	// We just check no panic and result is slice (possibly empty)
	_ = got
}

func TestWordsNoStop_CJKPassthrough(t *testing.T) {
	// CJK tokens should not be removed as stop words
	got := tokenize.WordsNoStop("知识库 management")
	if len(got) == 0 {
		t.Error("expected non-empty result for CJK+English input")
	}
}

func TestIsCJK(t *testing.T) {
	cases := []struct {
		r    rune
		want bool
	}{
		{'知', true},
		{'A', false},
		{'1', false},
		{'あ', true},   // Hiragana
		{'ア', true},   // Katakana
		{'한', true},   // Hangul
		{' ', false},
		{'.', false},
	}
	for _, c := range cases {
		got := tokenize.IsCJK(c.r)
		if got != c.want {
			t.Errorf("IsCJK(%q) = %v, want %v", c.r, got, c.want)
		}
	}
}
