package plugin

import (
	"context"
	"crypto/md5"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// SourceData is the result of fetching a source.
type SourceData struct {
	RawContent []byte
	RawMime    string
	PlainText  string
	Title      string
	Lang       string
	Meta       map[string]any
}

// RelationHint is a relation suggested by a plugin.
type RelationHint struct {
	ToOrigin string
	RelType  string
	Evidence string
}

// PluginMeta describes a plugin's capabilities.
type PluginMeta struct {
	Name        string
	SourceType  string
	Description string
}

// SourcePlugin is the interface for all data source plugins.
type SourcePlugin interface {
	Describe() PluginMeta
	Fetch(ctx context.Context, origin string, config map[string]any) (*SourceData, error)
	HasChanged(ctx context.Context, origin string, lastHash string) (bool, error)
	ExtractRelations(content string) ([]RelationHint, error)
}

// ContentHash returns an MD5 hex digest of content bytes.
// This is used purely as a change-detection fingerprint (not for security);
// dedupe.normalizedHash uses SHA256 for content identity checks.
func ContentHash(data []byte) string {
	return fmt.Sprintf("%x", md5.Sum(data))
}

// ── File Plugin ──────────────────────────────────────────────────────────────

type FilePlugin struct{}

func (p *FilePlugin) Describe() PluginMeta {
	return PluginMeta{
		Name:        "file",
		SourceType:  "file",
		Description: "Read local files (markdown, text, etc.)",
	}
}

func (p *FilePlugin) Fetch(_ context.Context, origin string, _ map[string]any) (*SourceData, error) {
	data, err := os.ReadFile(origin)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	mime := detectMime(origin)
	title := strings.TrimSuffix(filepath.Base(origin), filepath.Ext(origin))
	info, _ := os.Stat(origin)

	meta := map[string]any{
		"filename": filepath.Base(origin),
		"size":     len(data),
	}
	if info != nil {
		meta["mtime"] = info.ModTime().String()
	}

	plain := string(data)
	if mime == "text/markdown" {
		plain = stripMarkdown(plain)
	}

	return &SourceData{
		RawContent: data,
		RawMime:    mime,
		PlainText:  plain,
		Title:      title,
		Meta:       meta,
	}, nil
}

func (p *FilePlugin) HasChanged(_ context.Context, origin string, lastHash string) (bool, error) {
	data, err := os.ReadFile(origin)
	if err != nil {
		return false, err
	}
	return ContentHash(data) != lastHash, nil
}

func (p *FilePlugin) ExtractRelations(content string) ([]RelationHint, error) {
	return extractMarkdownLinks(content), nil
}

func detectMime(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown"
	case ".html", ".htm":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	case ".go", ".py", ".js", ".ts", ".rs", ".java", ".c", ".cpp":
		return "text/plain"
	default:
		return "text/plain"
	}
}

// ── URL Plugin ───────────────────────────────────────────────────────────────

type URLPlugin struct{}

func (p *URLPlugin) Describe() PluginMeta {
	return PluginMeta{
		Name:        "url",
		SourceType:  "url",
		Description: "Fetch and index web pages",
	}
}

func (p *URLPlugin) Fetch(ctx context.Context, origin string, _ map[string]any) (*SourceData, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Axon/1.0 (personal knowledge base)")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB max
	if err != nil {
		return nil, err
	}

	contentType := resp.Header.Get("Content-Type")
	mime := "text/html"
	if strings.Contains(contentType, "text/plain") {
		mime = "text/plain"
	}

	plain := string(body)
	title := origin
	if mime == "text/html" {
		plain = htmlToText(body)
		if t := extractHTMLTitle(body); t != "" {
			title = t
		}
	}

	return &SourceData{
		RawContent: body,
		RawMime:    mime,
		PlainText:  plain,
		Title:      title,
		Meta: map[string]any{
			"status_code": resp.StatusCode,
			"final_url":   resp.Request.URL.String(),
		},
	}, nil
}

// ── HTML utilities ────────────────────────────────────────────────────────────

var (
	reScript  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	reStyle   = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	reTag     = regexp.MustCompile(`<[^>]+>`)
	reTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reSpaces  = regexp.MustCompile(`[ \t]+`)
	reNewlines = regexp.MustCompile(`\n{3,}`)
)

func htmlToText(html []byte) string {
	s := string(html)
	s = reScript.ReplaceAllString(s, "")
	s = reStyle.ReplaceAllString(s, "")
	// Replace block tags with newlines
	s = regexp.MustCompile(`(?i)</(p|div|li|h[1-6]|br|tr|blockquote)[^>]*>`).ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	// Decode common HTML entities
	s = strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
		"&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
	).Replace(s)
	// Normalize whitespace
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		line = reSpaces.ReplaceAllString(line, " ")
		line = strings.TrimFunc(line, unicode.IsSpace)
		if line != "" {
			out = append(out, line)
		}
	}
	result := strings.Join(out, "\n")
	result = reNewlines.ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}

func extractHTMLTitle(html []byte) string {
	m := reTitle.FindSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	t := reTag.ReplaceAllString(string(m[1]), "")
	return strings.TrimSpace(t)
}

func (p *URLPlugin) HasChanged(ctx context.Context, origin string, lastHash string) (bool, error) {
	data, err := p.Fetch(ctx, origin, nil)
	if err != nil {
		return false, err
	}
	return ContentHash(data.RawContent) != lastHash, nil
}

func (p *URLPlugin) ExtractRelations(content string) ([]RelationHint, error) {
	return nil, nil
}

// ── Snippet Plugin ───────────────────────────────────────────────────────────

type SnippetPlugin struct{}

func (p *SnippetPlugin) Describe() PluginMeta {
	return PluginMeta{
		Name:        "snippet",
		SourceType:  "snippet",
		Description: "Store text snippets pasted directly by the user",
	}
}

func (p *SnippetPlugin) Fetch(_ context.Context, origin string, config map[string]any) (*SourceData, error) {
	content, _ := config["content"].(string)
	title, _ := config["title"].(string)
	return &SourceData{
		RawContent: []byte(content),
		RawMime:    "text/plain",
		PlainText:  content,
		Title:      title,
		Meta: map[string]any{
			"added_by": "user",
		},
	}, nil
}

func (p *SnippetPlugin) HasChanged(_ context.Context, _ string, _ string) (bool, error) {
	return false, nil
}

func (p *SnippetPlugin) ExtractRelations(content string) ([]RelationHint, error) {
	return nil, nil
}

// ── Markdown link extractor ──────────────────────────────────────────────────

func extractMarkdownLinks(content string) []RelationHint {
	var hints []RelationHint
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		// [text](path)
		for {
			start := strings.Index(line, "](")
			if start < 0 {
				break
			}
			end := strings.Index(line[start:], ")")
			if end < 0 {
				break
			}
			link := line[start+2 : start+end]
			if link != "" && !strings.HasPrefix(link, "http") {
				hints = append(hints, RelationHint{
					ToOrigin: link,
					RelType:  "ref",
					Evidence: line,
				})
			}
			line = line[start+end+1:]
		}
		// [[wiki link]]
		for {
			start := strings.Index(line, "[[")
			if start < 0 {
				break
			}
			end := strings.Index(line[start:], "]]")
			if end < 0 {
				break
			}
			link := line[start+2 : start+end]
			if link != "" {
				hints = append(hints, RelationHint{
					ToOrigin: link,
					RelType:  "cite",
					Evidence: line,
				})
			}
			line = line[start+end+2:]
		}
	}
	return hints
}

// ── Markdown stripper ────────────────────────────────────────────────────────

var (
	reMDFence      = regexp.MustCompile("(?m)^```[^`]*$")              // fenced code block markers
	reMDHeading    = regexp.MustCompile(`(?m)^#{1,6}\s+`)              // ## Heading
	// Go regexp2 does not support backreferences; use separate patterns for
	// star-emphasis (***bold-italic***, **bold**, *italic*) and
	// underscore-emphasis (___bold-italic___, __bold__, _italic_).
	reMDEmphasisStar  = regexp.MustCompile(`\*{1,3}(.+?)\*{1,3}`)     // *italic*, **bold**, ***both***
	reMDEmphasisUnderscore = regexp.MustCompile(`_{1,3}(.+?)_{1,3}`)  // _italic_, __bold__, ___both___
	reMDCode       = regexp.MustCompile("`+[^`]*`+")                   // `inline code`
	reMDLink       = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)    // [text](url) and ![alt](img)
	reMDWikiLink   = regexp.MustCompile(`\[\[([^\]]+)\]\]`)           // [[wiki link]]
	reMDHRule      = regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)         // --- or *** or ___
	reMDBlockquote = regexp.MustCompile(`(?m)^>\s?`)                  // > blockquote
	reMDListBullet = regexp.MustCompile(`(?m)^[\s]*[-*+]\s+`)         // - item, * item, + item
	reMDListNum    = regexp.MustCompile(`(?m)^[\s]*\d+\.\s+`)         // 1. item
	reMDTable      = regexp.MustCompile(`(?m)^\|.*\|$`)               // | table |
	reMDTableSep   = regexp.MustCompile(`(?m)^\|[-| :]+\|$`)          // |---|---|
)

// stripMarkdown converts Markdown text to clean plain text suitable for
// embedding and search. It removes formatting syntax while preserving
// the underlying words and sentences.
func stripMarkdown(md string) string {
	s := md

	// Remove fenced code block markers (keep content)
	s = reMDFence.ReplaceAllString(s, "")

	// Remove table separator rows entirely
	s = reMDTableSep.ReplaceAllString(s, "")

	// Strip table cell pipes but keep content
	s = reMDTable.ReplaceAllStringFunc(s, func(line string) string {
		line = strings.ReplaceAll(line, "|", " ")
		return strings.TrimSpace(line)
	})

	// Headings: remove # prefix, keep heading text
	s = reMDHeading.ReplaceAllString(s, "")

	// Horizontal rules → remove
	s = reMDHRule.ReplaceAllString(s, "")

	// Blockquote markers
	s = reMDBlockquote.ReplaceAllString(s, "")

	// List bullets → remove marker, keep content
	s = reMDListBullet.ReplaceAllString(s, "")
	s = reMDListNum.ReplaceAllString(s, "")

	// Images and links: keep alt/link text only
	s = reMDLink.ReplaceAllString(s, "$1")

	// Wiki links: keep inner text
	s = reMDWikiLink.ReplaceAllString(s, "$1")

	// Inline code: keep content, remove backticks
	s = reMDCode.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Trim(m, "`")
	})

	// Bold/italic: keep inner text (two passes — star and underscore variants)
	s = reMDEmphasisStar.ReplaceAllString(s, "$1")
	s = reMDEmphasisUnderscore.ReplaceAllString(s, "$1")

	// Normalize whitespace
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		line = reSpaces.ReplaceAllString(line, " ")
		line = strings.TrimFunc(line, unicode.IsSpace)
		out = append(out, line) // keep blank lines to preserve paragraph structure
	}
	result := strings.Join(out, "\n")
	result = reNewlines.ReplaceAllString(result, "\n\n")
	return strings.TrimSpace(result)
}
