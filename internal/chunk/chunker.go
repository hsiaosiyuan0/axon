package chunk

import (
	"strings"
)

// Chunk represents a piece of a document.
type Chunk struct {
	Content   string
	Position  int
	CharStart int
	CharEnd   int
	Section   string
}

// Strategy defines how a document should be split.
type Strategy string

const (
	StrategyMarkdown  Strategy = "markdown"
	StrategyParagraph Strategy = "paragraph"
	StrategyFixed     Strategy = "fixed"
	StrategyCode      Strategy = "code"
)

// Chunker splits plain text into chunks.
type Chunker interface {
	Chunk(text string) ([]Chunk, error)
}

// New returns the appropriate Chunker for the given strategy.
func New(strategy Strategy) Chunker {
	switch strategy {
	case StrategyMarkdown:
		return &MarkdownChunker{MaxChunkSize: 1000, Overlap: 100}
	case StrategyCode:
		return &FixedChunker{Size: 800, Overlap: 100}
	case StrategyFixed:
		return &FixedChunker{Size: 500, Overlap: 50}
	default:
		return &ParagraphChunker{MaxChunkSize: 800}
	}
}

// ── Markdown Chunker ─────────────────────────────────────────────────────────

// MarkdownChunker splits on heading boundaries.
type MarkdownChunker struct {
	MaxChunkSize int
	Overlap      int
}

func (c *MarkdownChunker) Chunk(text string) ([]Chunk, error) {
	lines := strings.Split(text, "\n")
	var chunks []Chunk
	var current strings.Builder
	var currentSection string
	charPos := 0
	chunkStart := 0
	pos := 0

	// overlapBuf accumulates the last Overlap bytes of the previous chunk
	// so they can be prepended to the next one for context continuity.
	var overlapBuf string

	// overlapLen tracks how many bytes of overlap text are prepended into
	// current after a flush, so that chunkStart is adjusted correctly and
	// CharStart/CharEnd offsets remain accurate relative to the original text.
	var overlapLen int

	flush := func() {
		content := strings.TrimSpace(current.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Content:   content,
			Position:  pos,
			CharStart: chunkStart,
			CharEnd:   charPos,
			Section:   currentSection,
		})
		pos++
		// The next chunk's CharStart should be at (charPos - overlapLen),
		// i.e. we rewind by the overlap that will be prepended.
		chunkStart = charPos

		// Capture tail of this chunk for overlap into the next one.
		if c.Overlap > 0 && len(content) > c.Overlap {
			overlapBuf = content[len(content)-c.Overlap:]
		} else {
			overlapBuf = content
		}
		overlapLen = 0
		current.Reset()
	}

	for _, line := range lines {
		charPos += len(line) + 1 // +1 for newline

		if isHeading(line) {
			// Flush current chunk on heading boundary
			if current.Len() > c.MaxChunkSize/2 {
				flush()
				// Prepend overlap from previous chunk and track its byte length.
				if overlapBuf != "" {
					overlapLen = len(overlapBuf) + 1 // +1 for the \n
					chunkStart -= overlapLen
					current.WriteString(overlapBuf)
					current.WriteString("\n")
				}
			}
			currentSection = strings.TrimLeft(line, "# ")
		}

		current.WriteString(line)
		current.WriteString("\n")

		// Flush if too large
		if current.Len() >= c.MaxChunkSize {
			flush()
			// Prepend overlap from previous chunk and track its byte length.
			if overlapBuf != "" {
				overlapLen = len(overlapBuf) + 1 // +1 for the \n
				chunkStart -= overlapLen
				current.WriteString(overlapBuf)
				current.WriteString("\n")
			}
		}
	}
	flush()

	return chunks, nil
}

func isHeading(line string) bool {
	return strings.HasPrefix(line, "# ") ||
		strings.HasPrefix(line, "## ") ||
		strings.HasPrefix(line, "### ")
}

// ── Paragraph Chunker ────────────────────────────────────────────────────────

type ParagraphChunker struct {
	MaxChunkSize int
}

func (c *ParagraphChunker) Chunk(text string) ([]Chunk, error) {
	paragraphs := strings.Split(text, "\n\n")
	var chunks []Chunk
	var current strings.Builder
	charPos := 0
	chunkStart := 0
	pos := 0

	flush := func() {
		content := strings.TrimSpace(current.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Content:   content,
			Position:  pos,
			CharStart: chunkStart,
			CharEnd:   charPos,
		})
		pos++
		chunkStart = charPos
		current.Reset()
	}

	for _, para := range paragraphs {
		charPos += len(para) + 2
		current.WriteString(para)
		current.WriteString("\n\n")
		if current.Len() >= c.MaxChunkSize {
			flush()
		}
	}
	flush()

	return chunks, nil
}

// ── Fixed Size Chunker ───────────────────────────────────────────────────────

type FixedChunker struct {
	Size    int
	Overlap int
}

func (c *FixedChunker) Chunk(text string) ([]Chunk, error) {
	var chunks []Chunk
	runes := []rune(text)
	pos := 0

	for start := 0; start < len(runes); start += c.Size - c.Overlap {
		end := start + c.Size
		if end > len(runes) {
			end = len(runes)
		}
		content := strings.TrimSpace(string(runes[start:end]))
		if content == "" {
			break
		}
		chunks = append(chunks, Chunk{
			Content:   content,
			Position:  pos,
			CharStart: start,
			CharEnd:   end,
		})
		pos++
		if end == len(runes) {
			break
		}
	}
	return chunks, nil
}
