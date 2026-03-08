package chunk_test

import (
	"strings"
	"testing"

	"github.com/hsiaosiyuan0/axon/internal/chunk"
)

// ── Helpers ───────────────────────────────────────────────────────────────────

func totalContent(chunks []chunk.Chunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString(c.Content)
	}
	return sb.String()
}

func assertPositionsMonotonic(t *testing.T, chunks []chunk.Chunk) {
	t.Helper()
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Position <= chunks[i-1].Position {
			t.Errorf("positions not monotonically increasing at %d: %d <= %d",
				i, chunks[i].Position, chunks[i-1].Position)
		}
	}
}

// ── MarkdownChunker ───────────────────────────────────────────────────────────

func TestMarkdownChunker_Empty(t *testing.T) {
	c := &chunk.MarkdownChunker{MaxChunkSize: 1000, Overlap: 100}
	chunks, err := c.Chunk("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty input, got %d", len(chunks))
	}
}

func TestMarkdownChunker_SingleSection(t *testing.T) {
	input := "# Introduction\n\nThis is a short intro paragraph.\n"
	c := &chunk.MarkdownChunker{MaxChunkSize: 1000, Overlap: 0}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least 1 chunk")
	}
	if chunks[0].Section == "" {
		t.Error("expected Section to be set for headed content")
	}
}

func TestMarkdownChunker_MultiSection(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString("## Section\n\n")
		// ~200 chars per section
		sb.WriteString(strings.Repeat("word ", 40))
		sb.WriteString("\n\n")
	}
	input := sb.String()

	c := &chunk.MarkdownChunker{MaxChunkSize: 300, Overlap: 50}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks for long multi-section text, got %d", len(chunks))
	}
	assertPositionsMonotonic(t, chunks)

	// Each chunk must respect MaxChunkSize (with some tolerance for edge flushes)
	for i, ch := range chunks {
		if len(ch.Content) > c.MaxChunkSize*2 {
			t.Errorf("chunk %d too large: %d chars", i, len(ch.Content))
		}
	}
}

func TestMarkdownChunker_PositionsStart0(t *testing.T) {
	input := "# A\n\nContent here.\n\n# B\n\nMore content.\n"
	c := &chunk.MarkdownChunker{MaxChunkSize: 1000, Overlap: 0}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) > 0 && chunks[0].Position != 0 {
		t.Errorf("first chunk position should be 0, got %d", chunks[0].Position)
	}
}

// ── ParagraphChunker ──────────────────────────────────────────────────────────

func TestParagraphChunker_Empty(t *testing.T) {
	c := &chunk.ParagraphChunker{MaxChunkSize: 800}
	chunks, err := c.Chunk("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty, got %d", len(chunks))
	}
}

func TestParagraphChunker_SingleParagraph(t *testing.T) {
	input := "This is a single paragraph with some text."
	c := &chunk.ParagraphChunker{MaxChunkSize: 800}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Content, "single paragraph") {
		t.Errorf("chunk content unexpected: %q", chunks[0].Content)
	}
}

func TestParagraphChunker_MultiParagraph(t *testing.T) {
	var parts []string
	for i := 0; i < 10; i++ {
		// ~100 chars each
		parts = append(parts, strings.Repeat("word ", 20))
	}
	input := strings.Join(parts, "\n\n")

	c := &chunk.ParagraphChunker{MaxChunkSize: 300}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	assertPositionsMonotonic(t, chunks)
}

func TestParagraphChunker_ContentPreserved(t *testing.T) {
	const unique = "xyzuniquetoken123"
	input := unique + "\n\nSome other paragraph.\n\nAnother one here."
	c := &chunk.ParagraphChunker{MaxChunkSize: 800}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := totalContent(chunks)
	if !strings.Contains(combined, unique) {
		t.Errorf("unique token %q not found in any chunk", unique)
	}
}

// ── FixedChunker ──────────────────────────────────────────────────────────────

func TestFixedChunker_Empty(t *testing.T) {
	c := &chunk.FixedChunker{Size: 100, Overlap: 10}
	chunks, err := c.Chunk("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks, got %d", len(chunks))
	}
}

func TestFixedChunker_ShortText(t *testing.T) {
	input := "Hello world"
	c := &chunk.FixedChunker{Size: 500, Overlap: 50}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk for short text, got %d", len(chunks))
	}
	if chunks[0].Content != input {
		t.Errorf("content mismatch: %q vs %q", chunks[0].Content, input)
	}
}

func TestFixedChunker_MultipleChunks(t *testing.T) {
	// 1000 chars → with size=200, overlap=20 → ~5 chunks
	input := strings.Repeat("a", 1000)
	c := &chunk.FixedChunker{Size: 200, Overlap: 20}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) < 4 {
		t.Errorf("expected ≥4 chunks, got %d", len(chunks))
	}
	assertPositionsMonotonic(t, chunks)
}

func TestFixedChunker_ChunkSizeRespected(t *testing.T) {
	input := strings.Repeat("x", 500)
	c := &chunk.FixedChunker{Size: 100, Overlap: 10}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for i, ch := range chunks {
		if len([]rune(ch.Content)) > c.Size {
			t.Errorf("chunk %d exceeds max size: %d > %d", i, len([]rune(ch.Content)), c.Size)
		}
	}
}

func TestFixedChunker_ZeroOverlap(t *testing.T) {
	input := strings.Repeat("b", 300)
	c := &chunk.FixedChunker{Size: 100, Overlap: 0}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(chunks) != 3 {
		t.Errorf("expected exactly 3 chunks, got %d", len(chunks))
	}
}

func TestFixedChunker_PositionsStart0(t *testing.T) {
	input := strings.Repeat("c", 300)
	c := &chunk.FixedChunker{Size: 100, Overlap: 0}
	chunks, err := c.Chunk(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chunks[0].Position != 0 {
		t.Errorf("first chunk position should be 0, got %d", chunks[0].Position)
	}
}

// ── New() factory ─────────────────────────────────────────────────────────────

func TestNew_Strategies(t *testing.T) {
	cases := []chunk.Strategy{
		chunk.StrategyMarkdown,
		chunk.StrategyParagraph,
		chunk.StrategyFixed,
		chunk.StrategyCode,
		"unknown", // should default to ParagraphChunker
	}
	input := "Hello world.\n\nSecond paragraph."
	for _, s := range cases {
		c := chunk.New(s)
		if c == nil {
			t.Errorf("New(%q) returned nil", s)
			continue
		}
		chunks, err := c.Chunk(input)
		if err != nil {
			t.Errorf("New(%q).Chunk error: %v", s, err)
		}
		if len(chunks) == 0 {
			t.Errorf("New(%q) produced 0 chunks for non-empty input", s)
		}
	}
}
