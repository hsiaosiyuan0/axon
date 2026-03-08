package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all Axon configuration.
// Resolution priority (highest → lowest):
//
//	CLI flag > environment variable > config file > built-in default
type Config struct {
	// DBPath is the path to the SQLite database file.
	DBPath string

	// ModelsDir is where local ONNX models are stored.
	ModelsDir string

	// PluginsDir is where external plugin binaries are stored.
	PluginsDir string

	// DefaultModel is the default embedding model name.
	DefaultModel string

	// ── Embedding ────────────────────────────────────────────────────────────

	// EmbedProvider selects the embedding backend.
	// Values: "onnx" (default), "api", "purego"
	EmbedProvider string

	// EmbedAPIEndpoint is the base URL for the embedding API.
	EmbedAPIEndpoint string

	// EmbedAPIKey is the API key for the embedding API.
	EmbedAPIKey string

	// EmbedAPIModel is the model name sent to the embedding API.
	EmbedAPIModel string

	// ── Classification ───────────────────────────────────────────────────────

	// ClassifyProvider selects the collection classification backend.
	// Values: "llm" (default), "nli" (local NLI cross-encoder), "bge-cosine" (local BGE embedding)
	ClassifyProvider string

	// ── LLM ──────────────────────────────────────────────────────────────────

	LLMEndpoint string
	LLMAPIKey   string
	LLMModel    string

	// ── Server ───────────────────────────────────────────────────────────────

	// APIKey is the secret key required for HTTP API access (empty = no auth).
	APIKey string

	// ── Internal ─────────────────────────────────────────────────────────────

	// ConfigPath is the resolved path of the config file (for display).
	ConfigPath string
}

// ── Loader ────────────────────────────────────────────────────────────────────

// Load loads configuration from (in order):
//
//  1. Built-in defaults
//  2. Config file (~/.axon/config.toml or AXON_CONFIG env)
//  3. Environment variables (override file values)
//
// If dbOverride is non-empty it overrides the db path from all sources.
func Load(dbOverride ...string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	base := filepath.Join(home, ".axon")
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, err
	}

	// ── 1. Defaults ───────────────────────────────────────────────────────────
	cfg := defaults(base)

	// ── 2. Config file ────────────────────────────────────────────────────────
	cfgFile := os.Getenv("AXON_CONFIG")
	if cfgFile == "" {
		cfgFile = filepath.Join(base, "config.toml")
	}
	cfg.ConfigPath = cfgFile

	if err := loadFile(cfgFile, cfg); err != nil {
		return nil, err
	}

	// ── 3. Environment variables (override file) ──────────────────────────────
	applyEnv(cfg)

	// ── 4. CLI db override ────────────────────────────────────────────────────
	if len(dbOverride) > 0 && dbOverride[0] != "" {
		p := dbOverride[0]
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		cfg.DBPath = p
	}

	return cfg, nil
}

// ── Defaults ──────────────────────────────────────────────────────────────────

func defaults(base string) *Config {
	return &Config{
		DBPath:           filepath.Join(base, "axon.db"),
		ModelsDir:        filepath.Join(base, "models"),
		PluginsDir:       filepath.Join(base, "plugins"),
		DefaultModel:     "bge-small-zh-v1.5",
		EmbedProvider:    "onnx",
		EmbedAPIEndpoint: "https://api.openai.com/v1",
		EmbedAPIModel:    "text-embedding-3-small",
		ClassifyProvider: "llm",
		LLMEndpoint:      "https://api.openai.com/v1",
		LLMModel:         "gpt-4o-mini",
	}
}

// ── Config file parser ────────────────────────────────────────────────────────

// loadFile parses a minimal TOML subset.
// Only scalar string values are supported (no arrays/inline tables).
// Returns nil if the file does not exist.
func loadFile(path string, cfg *Config) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil // config file is optional
	}
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip blanks and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Section header: [embed], [embed.api], [llm], [server], [db]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = line[1 : len(line)-1]
			continue
		}

		// key = value
		k, v, ok := parseLine(line)
		if !ok {
			continue
		}

		switch section + "." + k {
		// [db]
		case "db.path":
			cfg.DBPath = expandHome(v)
		case "db.models_dir":
			cfg.ModelsDir = expandHome(v)
		case "db.plugins_dir":
			cfg.PluginsDir = expandHome(v)

		// [embed]
		case "embed.provider":
			cfg.EmbedProvider = v
		case "embed.model":
			cfg.DefaultModel = v

		// [embed.api]
		case "embed.api.endpoint":
			cfg.EmbedAPIEndpoint = v
		case "embed.api.key":
			cfg.EmbedAPIKey = v
		case "embed.api.model":
			cfg.EmbedAPIModel = v

		// [classify]
		case "classify.provider":
			cfg.ClassifyProvider = v

		// [llm]
		case "llm.endpoint":
			cfg.LLMEndpoint = v
		case "llm.key":
			cfg.LLMAPIKey = v
		case "llm.model":
			cfg.LLMModel = v

		// [server]
		case "server.api_key":
			cfg.APIKey = v
		}
	}
	return scanner.Err()
}

// parseLine parses "key = value" or "key = \"value\"" into (key, value, true).
func parseLine(line string) (key, val string, ok bool) {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	val = strings.TrimSpace(line[idx+1:])

	// Strip inline comments (# ...) — but only outside quotes
	val = stripInlineComment(val)

	// Unquote if surrounded by double quotes
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
		// Unescape \" → "
		val = strings.ReplaceAll(val, `\"`, `"`)
	}
	return key, val, true
}

func stripInlineComment(s string) string {
	inQuote := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '"':
			inQuote = !inQuote
		case '#':
			if !inQuote {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// ── Environment variable overlay ──────────────────────────────────────────────

func applyEnv(cfg *Config) {
	if v := os.Getenv("AXON_DB"); v != "" {
		cfg.DBPath = v
	}
	if v := os.Getenv("AXON_MODELS_DIR"); v != "" {
		cfg.ModelsDir = v
	}
	if v := os.Getenv("AXON_PLUGINS_DIR"); v != "" {
		cfg.PluginsDir = v
	}
	if v := os.Getenv("AXON_DEFAULT_MODEL"); v != "" {
		cfg.DefaultModel = v
	}
	if v := os.Getenv("AXON_EMBED_PROVIDER"); v != "" {
		cfg.EmbedProvider = v
	}
	if v := os.Getenv("AXON_EMBED_API_ENDPOINT"); v != "" {
		cfg.EmbedAPIEndpoint = v
	}
	if v := os.Getenv("AXON_EMBED_API_KEY"); v != "" {
		cfg.EmbedAPIKey = v
	}
	if v := os.Getenv("AXON_EMBED_API_MODEL"); v != "" {
		cfg.EmbedAPIModel = v
	}
	if v := os.Getenv("AXON_CLASSIFY_PROVIDER"); v != "" {
		cfg.ClassifyProvider = v
	}
	if v := os.Getenv("AXON_LLM_ENDPOINT"); v != "" {
		cfg.LLMEndpoint = v
	}
	if v := os.Getenv("AXON_LLM_API_KEY"); v != "" {
		cfg.LLMAPIKey = v
	}
	if v := os.Getenv("AXON_LLM_MODEL"); v != "" {
		cfg.LLMModel = v
	}
	if v := os.Getenv("AXON_API_KEY"); v != "" {
		cfg.APIKey = v
	}

	// Legacy fallback: embed key/endpoint fall back to LLM if not set
	if cfg.EmbedAPIKey == "" {
		cfg.EmbedAPIKey = cfg.LLMAPIKey
	}
	if cfg.EmbedAPIEndpoint == "" {
		cfg.EmbedAPIEndpoint = cfg.LLMEndpoint
	}
}
