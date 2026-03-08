// Package tokenize provides CJK-aware tokenization for BM25 search.
//
// SQLite FTS5's built-in "unicode61" tokenizer treats Chinese characters as
// individual tokens, but a query like "知识库管理" will not match unless each
// character is searched separately. This package transforms CJK queries into
// forms that FTS5 can match effectively:
//
//   - CJK characters are split into individual tokens (unigrams)
//   - Adjacent CJK characters also produce bigrams for better phrase matching
//   - Non-CJK terms are passed through unchanged
//
// Example:
//
//	Input:  "知识库 management"
//	Output: `"知 识 库" management`  (FTS5 phrase query + plain term)
package tokenize

import (
	"strings"
	"unicode"
)

// IsCJK reports whether r is a CJK unified ideograph or common CJK symbol.
func IsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r)
}

// HasCJK reports whether s contains any CJK character.
func HasCJK(s string) bool {
	for _, r := range s {
		if IsCJK(r) {
			return true
		}
	}
	return false
}

// TokenizeQuery transforms a search query into an FTS5-compatible form.
//
// For purely ASCII/Latin queries the original string is returned unchanged
// (fast path). For queries containing CJK characters, each contiguous run of
// CJK characters is expanded into unigrams wrapped in FTS5 phrase syntax,
// and bigrams are added as NEAR queries for better precision.
func TokenizeQuery(query string) string {
	if !HasCJK(query) {
		return query
	}

	// Tokenize into segments: CJK runs vs non-CJK words
	var parts []string
	runes := []rune(query)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if IsCJK(r) {
			// Collect contiguous CJK run
			j := i
			for j < len(runes) && IsCJK(runes[j]) {
				j++
			}
			run := runes[i:j]
			parts = append(parts, expandCJKRun(run))
			i = j
		} else if unicode.IsSpace(r) {
			i++
		} else {
			// Non-CJK word
			j := i
			for j < len(runes) && !IsCJK(runes[j]) && !unicode.IsSpace(runes[j]) {
				j++
			}
			word := strings.TrimSpace(string(runes[i:j]))
			if word != "" {
				parts = append(parts, word)
			}
			i = j
		}
	}
	return strings.Join(parts, " ")
}

// maxCJKTerms is the upper bound on the number of OR terms emitted for a single
// CJK run. Very long runs (e.g. pasting an entire paragraph as a query) would
// otherwise produce thousands of terms and slow down FTS5 significantly.
const maxCJKTerms = 64

// expandCJKRun converts a slice of CJK runes into FTS5 query terms.
// For a run of N characters we emit:
//   - individual unigrams (always)
//   - bigrams (if run length ≥ 2, wrapped in NEAR for soft phrase matching)
//
// The total number of OR terms is capped at maxCJKTerms.
func expandCJKRun(run []rune) string {
	if len(run) == 0 {
		return ""
	}
	if len(run) == 1 {
		return string(run[0])
	}

	var terms []string

	// Unigrams
	for _, r := range run {
		if len(terms) >= maxCJKTerms {
			break
		}
		terms = append(terms, string(r))
	}

	// Bigrams (higher weight via NEAR)
	for i := 0; i+1 < len(run); i++ {
		if len(terms) >= maxCJKTerms {
			break
		}
		bigram := string(run[i]) + string(run[i+1])
		terms = append(terms, bigram)
	}

	// Join as FTS5 OR expression
	return "(" + strings.Join(terms, " OR ") + ")"
}

// NormalizeQuery applies light normalisation before tokenisation:
//   - full-width ASCII → half-width
//   - strip leading/trailing whitespace
func NormalizeQuery(q string) string {
	q = strings.TrimSpace(q)
	// full-width to half-width (Ａ=0xFF21 → A=0x41, offset 0xFEE0)
	var b strings.Builder
	for _, r := range q {
		if r >= 0xFF01 && r <= 0xFF5E {
			b.WriteRune(r - 0xFEE0)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ── Word Tokenizers ───────────────────────────────────────────────────────────

// Words splits s into lowercase word tokens (letters and digits only).
// This is the basic tokenizer used by the pure-Go embedder.
func Words(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var buf strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			buf.WriteRune(r)
		} else {
			if buf.Len() > 0 {
				tokens = append(tokens, buf.String())
				buf.Reset()
			}
		}
	}
	if buf.Len() > 0 {
		tokens = append(tokens, buf.String())
	}
	return tokens
}

// WordsNoStop splits s into lowercase word tokens, removing common English
// stop words and single-character tokens. Used by the reranker.
func WordsNoStop(s string) []string {
	raw := Words(s)
	out := raw[:0]
	for _, tok := range raw {
		if len(tok) > 1 && !isStopWord(tok) {
			out = append(out, tok)
		}
	}
	return out
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true,
	"but": true, "in": true, "on": true, "at": true, "to": true,
	"for": true, "of": true, "with": true, "by": true, "from": true,
	"is": true, "are": true, "was": true, "were": true, "be": true,
	"been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "not": true, "no": true,
	"it": true, "its": true, "this": true, "that": true, "as": true,
}

func isStopWord(s string) bool { return stopWords[s] }
