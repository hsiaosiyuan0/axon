package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// SetValue updates a single key in the TOML config file.
//
// The key is a dotted path in "section.key" or "section.subsection.key" format.
// Examples:
//
//	SetValue(path, "llm.key",         "sk-...")
//	SetValue(path, "embed.provider",  "api")
//	SetValue(path, "embed.api.model", "text-embedding-3-small")
//
// If the key already exists in the file it is updated in-place.
// If the key is not found, it is appended under the appropriate section header.
// The section header is created if it doesn't exist.
func SetValue(cfgPath, dotKey, value string) error {
	// Parse dotKey → (section, key)
	// "llm.key"         → section="llm",       key="key"
	// "embed.api.model" → section="embed.api",  key="model"
	// "embed.provider"  → section="embed",      key="provider"
	section, key, err := splitDotKey(dotKey)
	if err != nil {
		return err
	}

	// Read existing file
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	lines := strings.Split(string(raw), "\n")
	newLine := key + ` = "` + escapeValue(value) + `"`

	// ── Pass 1: update existing key in the correct section ────────────────────
	currentSection := ""
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track section
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			currentSection = trimmed[1 : len(trimmed)-1]
			continue
		}

		if currentSection != section {
			continue
		}

		// Check if this line matches the key (possibly commented out)
		bare := strings.TrimLeft(line, " \t")
		stripped := strings.TrimLeft(bare, "#") // strip leading comment markers
		stripped = strings.TrimSpace(stripped)

		k, _, ok := parseLine(stripped)
		if !ok || k != key {
			continue
		}

		// Found the key — replace/uncomment this line
		indent := leadingWhitespace(line)
		lines[i] = indent + newLine
		return os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600)
	}

	// ── Pass 2: append under section (or create section) ─────────────────────
	// Find the section and append after the last line in it.
	sectionHeader := "[" + section + "]"
	inSection := false
	insertAt := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inSection {
				// We've left our section, insert before this line
				insertAt = i
				break
			}
			if trimmed == sectionHeader {
				inSection = true
			}
			continue
		}

		if inSection {
			insertAt = i + 1 // keep moving to end of section
		}
	}

	if insertAt < 0 && inSection {
		// Section found, append at end of file
		insertAt = len(lines)
	}

	if insertAt < 0 {
		// Section not found — append section + key at end of file
		// Ensure file ends with newline
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, sectionHeader)
		lines = append(lines, newLine)
		lines = append(lines, "")
	} else {
		// Insert into existing section
		newLines := make([]string, 0, len(lines)+1)
		newLines = append(newLines, lines[:insertAt]...)
		newLines = append(newLines, newLine)
		newLines = append(newLines, lines[insertAt:]...)
		lines = newLines
	}

	return os.WriteFile(cfgPath, []byte(strings.Join(lines, "\n")), 0o600)
}

// splitDotKey splits "section.key" or "section.sub.key" into
// (section, key). The section is everything up to the last dot.
func splitDotKey(dotKey string) (section, key string, err error) {
	idx := strings.LastIndexByte(dotKey, '.')
	if idx < 0 {
		return "", "", fmt.Errorf("config key must be in section.key format, got %q", dotKey)
	}
	return dotKey[:idx], dotKey[idx+1:], nil
}

func escapeValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func leadingWhitespace(s string) string {
	for i, c := range s {
		if c != ' ' && c != '\t' {
			return s[:i]
		}
	}
	return s
}

// RewriteAll writes all config values back to the file (destructive).
// Prefer SetValue for targeted updates.
func RewriteAll(cfgPath string, cfg *Config) error {
	var sb strings.Builder
	w := bufio.NewWriter(&sb)

	fmt.Fprintln(w, "# Axon configuration — managed by axon config set")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[db]")
	fmt.Fprintf(w, "path        = %q\n", cfg.DBPath)
	fmt.Fprintf(w, "models_dir  = %q\n", cfg.ModelsDir)
	fmt.Fprintf(w, "plugins_dir = %q\n", cfg.PluginsDir)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[classify]")
	fmt.Fprintf(w, "provider = %q\n", cfg.ClassifyProvider)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[embed]")
	fmt.Fprintf(w, "provider = %q\n", cfg.EmbedProvider)
	fmt.Fprintf(w, "model    = %q\n", cfg.DefaultModel)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[embed.api]")
	fmt.Fprintf(w, "endpoint = %q\n", cfg.EmbedAPIEndpoint)
	fmt.Fprintf(w, "key      = %q\n", cfg.EmbedAPIKey)
	fmt.Fprintf(w, "model    = %q\n", cfg.EmbedAPIModel)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[llm]")
	fmt.Fprintf(w, "endpoint = %q\n", cfg.LLMEndpoint)
	fmt.Fprintf(w, "key      = %q\n", cfg.LLMAPIKey)
	fmt.Fprintf(w, "model    = %q\n", cfg.LLMModel)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "[server]")
	fmt.Fprintf(w, "api_key = %q\n", cfg.APIKey)

	w.Flush()
	return os.WriteFile(cfgPath, []byte(sb.String()), 0o600)
}
