// Package plugin provides source plugins for Axon.
// This file implements Notion export parsing (HTML and Markdown).
package plugin

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ── Public API ────────────────────────────────────────────────────────────────

// ParseNotionHTML parses a Notion HTML export file and returns a SourceData ready for ingestion.
// Returns an error if the file does not look like a Notion HTML export.
func ParseNotionHTML(path string) (*SourceData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read notion html: %w", err)
	}

	if !isNotionHTML(raw) {
		return nil, fmt.Errorf("not a Notion HTML export: %s", filepath.Base(path))
	}

	title := extractNotionTitle(raw)
	if title == "" {
		title = cleanNotionTitle(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}

	plain := extractNotionBody(raw)
	props := extractNotionProperties(raw)

	// Append properties to plain text so they are searchable
	if len(props) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n---\n")
		for k, v := range props {
			if v != "" {
				sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
			}
		}
		plain += sb.String()
	}

	meta := map[string]any{
		"notion_export": true,
		"filename":      filepath.Base(path),
	}
	if len(props) > 0 {
		meta["properties"] = props
	}

	return &SourceData{
		RawContent: raw,
		RawMime:    "text/html",
		PlainText:  plain,
		Title:      title,
		Meta:       meta,
	}, nil
}

// ParseNotionMarkdown parses a Notion Markdown export file.
// Notion markdown files may have UUID-suffixed filenames and optional YAML frontmatter.
func ParseNotionMarkdown(path string) (*SourceData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read notion markdown: %w", err)
	}

	content := string(raw)

	// Derive title from filename, stripping any Notion UUID suffix
	title := cleanNotionTitle(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))

	// Extract YAML frontmatter if present
	props := map[string]string{}
	body := content
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end > 0 {
			frontmatter := content[4 : end+4]
			body = strings.TrimSpace(content[end+8:])
			for _, line := range strings.Split(frontmatter, "\n") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					k := strings.TrimSpace(parts[0])
					v := strings.TrimSpace(parts[1])
					props[k] = v
				}
			}
		}
	}

	// Prefer title from frontmatter
	if t, ok := props["title"]; ok && t != "" {
		title = strings.Trim(t, `"'`)
	}

	// Append non-title properties for searchability
	if len(props) > 0 {
		var sb strings.Builder
		sb.WriteString("\n\n---\n")
		for k, v := range props {
			if k == "title" || v == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("%s: %s\n", k, v))
		}
		body += sb.String()
	}

	meta := map[string]any{
		"notion_export": true,
		"filename":      filepath.Base(path),
	}
	if len(props) > 0 {
		meta["properties"] = props
	}

	return &SourceData{
		RawContent: raw,
		RawMime:    "text/markdown",
		PlainText:  body,
		Title:      title,
		Meta:       meta,
	}, nil
}

// IsNotionExport heuristically detects whether a directory is a Notion export.
// It checks for .html files with Notion-specific markers and UUID-suffixed filenames.
func IsNotionExport(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	hits := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))

		if ext == ".html" {
			p := filepath.Join(dir, name)
			f, ferr := os.Open(p)
			if ferr != nil {
				continue
			}
			buf := make([]byte, 2048)
			n, _ := f.Read(buf)
			f.Close()
			if isNotionHTML(buf[:n]) {
				hits++
			}
		}

		// Notion-style filenames end with a 32-hex-char UUID segment
		stem := strings.TrimSuffix(name, ext)
		if hasNotionUUIDSuffix(stem) {
			hits++
		}

		if hits >= 2 {
			return true
		}
	}
	return hits >= 2
}

// ── Internal helpers ──────────────────────────────────────────────────────────

var (
	reNotionH1Title  = regexp.MustCompile(`(?i)<h1[^>]*class="[^"]*page-title[^"]*"[^>]*>(.*?)</h1>`)
	reHTMLTitleTag   = regexp.MustCompile(`(?i)<title[^>]*>(.*?)</title>`)
	reNotionPageBody = regexp.MustCompile(`(?is)<div[^>]*class="[^"]*page-body[^"]*"[^>]*>(.*?)</div>`)
	reThCell         = regexp.MustCompile(`(?i)<th[^>]*>(.*?)</th>`)
	reTdCell         = regexp.MustCompile(`(?i)<td[^>]*>(.*?)</td>`)
	reNotionUUIDSfx  = regexp.MustCompile(`\s+[0-9a-f]{32}$`)
	reStripTag       = regexp.MustCompile(`<[^>]+>`)
)

func isNotionHTML(data []byte) bool {
	s := strings.ToLower(string(data))
	return strings.Contains(s, "notion") &&
		(strings.Contains(s, "page-body") ||
			strings.Contains(s, "notion-body") ||
			strings.Contains(s, `generator" content="notion`) ||
			strings.Contains(s, "notion.so"))
}

func extractNotionTitle(raw []byte) string {
	// Try the Notion-specific <h1 class="page-title"> element first
	if m := reNotionH1Title.FindSubmatch(raw); len(m) >= 2 {
		t := reStripTag.ReplaceAllString(string(m[1]), "")
		return strings.TrimSpace(notionHTMLEntityDecode(t))
	}
	// Fall back to the <title> tag
	if m := reHTMLTitleTag.FindSubmatch(raw); len(m) >= 2 {
		t := reStripTag.ReplaceAllString(string(m[1]), "")
		t = strings.TrimSpace(notionHTMLEntityDecode(t))
		// Notion appends " | Notion" to the page title
		if idx := strings.LastIndex(t, " | Notion"); idx > 0 {
			t = t[:idx]
		}
		return strings.TrimSpace(t)
	}
	return ""
}

func extractNotionBody(raw []byte) string {
	if m := reNotionPageBody.FindSubmatch(raw); len(m) >= 2 {
		return htmlToText(m[1])
	}
	return htmlToText(raw)
}

func extractNotionProperties(raw []byte) map[string]string {
	props := map[string]string{}
	s := string(raw)

	names := reThCell.FindAllStringSubmatch(s, -1)
	values := reTdCell.FindAllStringSubmatch(s, -1)

	for i, n := range names {
		if i >= len(values) {
			break
		}
		key := strings.TrimSpace(reStripTag.ReplaceAllString(n[1], ""))
		val := strings.TrimSpace(reStripTag.ReplaceAllString(values[i][1], ""))
		if key != "" && val != "" {
			props[key] = val
		}
	}
	return props
}

func cleanNotionTitle(name string) string {
	return strings.TrimSpace(reNotionUUIDSfx.ReplaceAllString(name, ""))
}

func hasNotionUUIDSuffix(name string) bool {
	return reNotionUUIDSfx.MatchString(name)
}

func notionHTMLEntityDecode(s string) string {
	return strings.NewReplacer(
		"&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", `"`, "&#39;", "'", "&nbsp;", " ",
		"&mdash;", "—", "&ndash;", "–", "&hellip;", "…",
	).Replace(s)
}
