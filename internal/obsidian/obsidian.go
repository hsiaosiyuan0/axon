// Package obsidian provides Obsidian vault parsing support.
// It extracts [[wikilinks]], #tags, and metadata from Obsidian markdown files.
package obsidian

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Link represents a parsed wikilink.
type Link struct {
	Raw     string // original [[Target|Alias]] text
	Target  string // resolved note name (without alias)
	Alias   string // display text (may be empty)
	Section string // optional #section within target
	IsEmbed bool   // ![[embed]] vs [[link]]
}

// Note holds metadata extracted from an Obsidian markdown file.
type Note struct {
	Path     string            // absolute file path
	Name     string            // note name (filename without .md)
	Links    []Link            // outgoing [[wikilinks]]
	Tags     []string          // #tags found inline
	Aliases  []string          // aliases from frontmatter
	Frontmatter map[string]string // raw frontmatter key-value pairs
}

var (
	reWikiLink  = regexp.MustCompile(`(!?)\[\[([^\[\]]+)\]\]`)
	reTag       = regexp.MustCompile(`(?:^|\s)#([\w/\-]+)`)
	reFrontmatter = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n?`)
	reYAMLLine  = regexp.MustCompile(`^(\w[\w\-]*)\s*:\s*(.*)$`)
)

// ParseFile parses a single Obsidian markdown file.
func ParseFile(path string) (*Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(path, string(data)), nil
}

// Parse parses Obsidian markdown content.
func Parse(path, content string) *Note {
	name := strings.TrimSuffix(filepath.Base(path), ".md")

	note := &Note{
		Path: path,
		Name: name,
	}

	// Extract frontmatter
	if m := reFrontmatter.FindStringSubmatchIndex(content); m != nil {
		fm := content[m[2]:m[3]]
		note.Frontmatter = parseFrontmatter(fm)
		// Parse aliases
		if aliases, ok := note.Frontmatter["aliases"]; ok {
			for _, a := range splitList(aliases) {
				a = strings.TrimSpace(a)
				if a != "" {
					note.Aliases = append(note.Aliases, a)
				}
			}
		}
		// Strip frontmatter from content for further parsing
		content = content[m[1]:]
	}

	// Extract wikilinks
	matches := reWikiLink.FindAllStringSubmatch(content, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		isEmbed := m[1] == "!"
		inner := m[2]

		// Parse [[Target#Section|Alias]]
		target, section, alias := parseWikiTarget(inner)
		if target == "" {
			continue
		}
		key := target + "#" + section + "|" + alias
		if seen[key] {
			continue
		}
		seen[key] = true

		note.Links = append(note.Links, Link{
			Raw:     m[0],
			Target:  target,
			Alias:   alias,
			Section: section,
			IsEmbed: isEmbed,
		})
	}

	// Extract tags
	tagMatches := reTag.FindAllStringSubmatch(content, -1)
	tagSeen := map[string]bool{}
	for _, m := range tagMatches {
		tag := m[1]
		if !tagSeen[tag] {
			tagSeen[tag] = true
			note.Tags = append(note.Tags, tag)
		}
	}

	return note
}

// parseWikiTarget splits "Target#Section|Alias" into its parts.
func parseWikiTarget(inner string) (target, section, alias string) {
	// Split alias
	if idx := strings.Index(inner, "|"); idx >= 0 {
		alias = inner[idx+1:]
		inner = inner[:idx]
	}
	// Split section
	if idx := strings.Index(inner, "#"); idx >= 0 {
		section = inner[idx+1:]
		inner = inner[:idx]
	}
	target = strings.TrimSpace(inner)
	return
}

// parseFrontmatter parses simple YAML frontmatter (key: value pairs).
func parseFrontmatter(fm string) map[string]string {
	result := map[string]string{}
	for _, line := range strings.Split(fm, "\n") {
		m := reYAMLLine.FindStringSubmatch(line)
		if m != nil {
			result[m[1]] = strings.Trim(strings.TrimSpace(m[2]), `"'`)
		}
	}
	return result
}

// splitList splits YAML list values like "[a, b, c]" or "a, b, c".
func splitList(s string) []string {
	s = strings.Trim(s, "[]")
	return strings.Split(s, ",")
}

// ── Vault ─────────────────────────────────────────────────────────────────────

// Vault represents a scanned Obsidian vault directory.
type Vault struct {
	Root  string
	Notes []*Note
	Index map[string]*Note // name → Note (for link resolution)
}

// ScanVault recursively scans a directory for .md files and parses them all.
func ScanVault(root string) (*Vault, error) {
	v := &Vault{
		Root:  root,
		Index: map[string]*Note{},
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible
		}
		// Skip hidden directories (e.g. .obsidian, .git)
		if info.IsDir() && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		if !info.IsDir() && strings.ToLower(filepath.Ext(path)) == ".md" {
			note, err := ParseFile(path)
			if err != nil {
				return nil // skip unreadable files
			}
			v.Notes = append(v.Notes, note)
			v.Index[note.Name] = note
			// Also index aliases
			for _, alias := range note.Aliases {
				v.Index[alias] = note
			}
		}
		return nil
	})
	return v, err
}

// ResolveLink resolves a wikilink target to an absolute file path.
// Returns empty string if not found.
func (v *Vault) ResolveLink(fromNote *Note, link Link) string {
	// Exact name match
	if n, ok := v.Index[link.Target]; ok {
		return n.Path
	}
	// Case-insensitive match
	lower := strings.ToLower(link.Target)
	for name, note := range v.Index {
		if strings.ToLower(name) == lower {
			return note.Path
		}
	}
	// Partial path match (Obsidian allows "folder/Note" links)
	for _, note := range v.Notes {
		rel, _ := filepath.Rel(v.Root, note.Path)
		relNoExt := strings.TrimSuffix(rel, ".md")
		if relNoExt == link.Target || note.Name == link.Target {
			return note.Path
		}
	}
	return ""
}

// BackLinks returns all notes that link to the given note.
func (v *Vault) BackLinks(targetNote *Note) []*Note {
	var result []*Note
	for _, note := range v.Notes {
		if note.Path == targetNote.Path {
			continue
		}
		for _, link := range note.Links {
			resolved := v.ResolveLink(note, link)
			if resolved == targetNote.Path {
				result = append(result, note)
				break
			}
		}
	}
	return result
}
