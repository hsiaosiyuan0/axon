package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
)

// PDFPlugin reads and extracts text from PDF files.
type PDFPlugin struct{}

func (p *PDFPlugin) Describe() PluginMeta {
	return PluginMeta{
		Name:        "pdf",
		SourceType:  "pdf",
		Description: "Extract text from PDF documents",
	}
}

func (p *PDFPlugin) Fetch(_ context.Context, origin string, _ map[string]any) (*SourceData, error) {
	data, err := os.ReadFile(origin)
	if err != nil {
		return nil, fmt.Errorf("read pdf: %w", err)
	}

	info, _ := os.Stat(origin)
	title := strings.TrimSuffix(filepath.Base(origin), filepath.Ext(origin))

	plain, meta, err := extractPDFText(data)
	if err != nil {
		// Non-fatal: store the source anyway; BM25 won't help much without text.
		plain = fmt.Sprintf("[PDF text extraction failed: %v]", err)
		meta = map[string]any{}
	}

	// Prefer PDF embedded title over filename
	if t, ok := meta["title"].(string); ok && t != "" {
		title = t
	}

	meta["filename"] = filepath.Base(origin)
	meta["size"] = len(data)
	if info != nil {
		meta["mtime"] = info.ModTime().String()
	}

	return &SourceData{
		RawContent: data,
		RawMime:    "application/pdf",
		PlainText:  plain,
		Title:      title,
		Meta:       meta,
	}, nil
}

func (p *PDFPlugin) HasChanged(_ context.Context, origin string, lastHash string) (bool, error) {
	data, err := os.ReadFile(origin)
	if err != nil {
		return false, err
	}
	return ContentHash(data) != lastHash, nil
}

func (p *PDFPlugin) ExtractRelations(_ string) ([]RelationHint, error) {
	return nil, nil
}

// extractPDFText extracts plain text and metadata from raw PDF bytes.
func extractPDFText(data []byte) (string, map[string]any, error) {
	meta := map[string]any{}

	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", meta, fmt.Errorf("open pdf: %w", err)
	}

	// Extract document info
	info := r.Trailer().Key("Info")
	if !info.IsNull() {
		readStr := func(key string) string {
			v := info.Key(key)
			if v.IsNull() {
				return ""
			}
			return strings.TrimSpace(fmt.Sprintf("%v", v))
		}
		if t := readStr("Title"); t != "" {
			meta["title"] = t
		}
		if a := readStr("Author"); a != "" {
			meta["author"] = a
		}
		if s := readStr("Subject"); s != "" {
			meta["subject"] = s
		}
		if k := readStr("Keywords"); k != "" {
			meta["keywords"] = k
		}
	}

	numPages := r.NumPage()
	meta["pages"] = numPages

	var sb strings.Builder
	for i := 1; i <= numPages; i++ {
		pg := r.Page(i)
		if pg.V.IsNull() {
			continue
		}
		text, err := pg.GetPlainText(nil)
		if err != nil || text == "" {
			continue
		}
		sb.WriteString(text)
		if !strings.HasSuffix(strings.TrimSpace(text), "\n") {
			sb.WriteString("\n")
		}
	}

	return normalizePDFText(sb.String()), meta, nil
}

// normalizePDFText cleans up raw PDF text output.
var (
	rePDFTrailingSpaces = regexp.MustCompile(`[ \t]+\n`)
	rePDFBlankLines     = regexp.MustCompile(`\n{3,}`)
)

func normalizePDFText(s string) string {
	// Replace form feeds with newlines
	s = strings.ReplaceAll(s, "\f", "\n")
	// Trim trailing spaces per line
	s = rePDFTrailingSpaces.ReplaceAllString(s, "\n")
	// Collapse excessive blank lines
	s = rePDFBlankLines.ReplaceAllString(s, "\n\n")
	// Strip non-printable characters (keep \n \t \r and printable ASCII+Unicode)
	var buf strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r == '\r' || (r >= 32 && r != 127) {
			buf.WriteRune(r)
		}
	}
	return strings.TrimSpace(buf.String())
}

// isPDF returns true if the file extension is .pdf.
func isPDF(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

// Compile-time assertion
var _ SourcePlugin = (*PDFPlugin)(nil)
