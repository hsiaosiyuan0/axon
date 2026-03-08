// Package extplugin implements the external plugin system.
// External plugins are standalone executables that communicate via
// stdin/stdout JSON-RPC protocol, enabling any language to extend Axon.
//
// Protocol:
//   - Axon writes a single JSON request line to stdin
//   - Plugin writes a single JSON response line to stdout
//   - Plugin exits with code 0 on success
//
// Supported methods:
//   - "describe"  → { "source_type": "...", "description": "..." }
//   - "fetch"     → { "plain_text": "...", "title": "...", "raw_mime": "...", "lang": "..." }
//   - "has_changed" → { "changed": true/false }
//   - "relations"   → { "relations": [ { "to_origin": "...", "rel_type": "..." } ] }
package extplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/plugin"
)

// ---------------------------------------------------------------------------
// JSON-RPC types
// ---------------------------------------------------------------------------

type rpcRequest struct {
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
// ExternalPlugin
// ---------------------------------------------------------------------------

// ExternalPlugin wraps an external executable as a SourcePlugin.
type ExternalPlugin struct {
	path    string // absolute path to the executable
	timeout time.Duration

	// cached describe result
	desc *plugin.PluginMeta
}

// NewExternalPlugin creates a wrapper for an external plugin binary.
func NewExternalPlugin(path string) (*ExternalPlugin, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("plugin not found: %s", abs)
	}
	ep := &ExternalPlugin{path: abs, timeout: 30 * time.Second}
	return ep, nil
}

func (p *ExternalPlugin) Describe() plugin.PluginMeta {
	if p.desc != nil {
		return *p.desc
	}
	var result struct {
		SourceType  string `json:"source_type"`
		Description string `json:"description"`
	}
	if err := p.call(context.Background(), "describe", nil, &result); err != nil {
		return plugin.PluginMeta{SourceType: "unknown", Description: fmt.Sprintf("error: %v", err)}
	}
	info := plugin.PluginMeta{
		SourceType:  result.SourceType,
		Description: result.Description,
	}
	p.desc = &info
	return info
}

func (p *ExternalPlugin) Fetch(ctx context.Context, origin string, opts map[string]any) (*plugin.SourceData, error) {
	params := map[string]any{"origin": origin}
	if opts != nil {
		for k, v := range opts {
			params[k] = v
		}
	}

	var result struct {
		PlainText string         `json:"plain_text"`
		Title     string         `json:"title"`
		RawMime   string         `json:"raw_mime"`
		Lang      string         `json:"lang"`
		RawContent string        `json:"raw_content"`
		Meta      map[string]any `json:"meta"`
	}
	if err := p.call(ctx, "fetch", params, &result); err != nil {
		return nil, err
	}
	rawContent := []byte(result.RawContent)
	if len(rawContent) == 0 {
		rawContent = []byte(result.PlainText)
	}
	return &plugin.SourceData{
		RawContent: rawContent,
		RawMime:    result.RawMime,
		PlainText:  result.PlainText,
		Title:      result.Title,
		Lang:       result.Lang,
		Meta:       result.Meta,
	}, nil
}

func (p *ExternalPlugin) HasChanged(ctx context.Context, origin string, lastHash string) (bool, error) {
	var result struct {
		Changed bool `json:"changed"`
	}
	err := p.call(ctx, "has_changed",
		map[string]any{"origin": origin, "last_hash": lastHash}, &result)
	if err != nil {
		return true, nil // assume changed on error
	}
	return result.Changed, nil
}

func (p *ExternalPlugin) ExtractRelations(content string) ([]plugin.RelationHint, error) {
	var result struct {
		Relations []struct {
			ToOrigin string `json:"to_origin"`
			RelType  string `json:"rel_type"`
			Evidence string `json:"evidence"`
		} `json:"relations"`
	}
	if err := p.call(context.Background(), "relations",
		map[string]any{"content": content}, &result); err != nil {
		return nil, nil // non-fatal
	}
	var hints []plugin.RelationHint
	for _, r := range result.Relations {
		hints = append(hints, plugin.RelationHint{
			ToOrigin: r.ToOrigin,
			RelType:  r.RelType,
			Evidence: r.Evidence,
		})
	}
	return hints, nil
}

// call executes the plugin binary with a JSON request and decodes the response.
func (p *ExternalPlugin) call(ctx context.Context, method string, params map[string]any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	reqBytes, err := json.Marshal(rpcRequest{Method: method, Params: params})
	if err != nil {
		return err
	}
	reqBytes = append(reqBytes, '\n')

	cmd := exec.CommandContext(ctx, p.path)
	cmd.Stdin = bytes.NewReader(reqBytes)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("plugin %s exited with error: %w\nstderr: %s",
			filepath.Base(p.path), err, stderr.String())
	}

	// Read first non-empty line from stdout
	line, err := firstLine(stdout.Bytes())
	if err != nil {
		return fmt.Errorf("plugin %s returned no output", filepath.Base(p.path))
	}

	var resp rpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return fmt.Errorf("plugin %s invalid JSON response: %w", filepath.Base(p.path), err)
	}
	if resp.Error != "" {
		return fmt.Errorf("plugin error: %s", resp.Error)
	}
	if out != nil && resp.Result != nil {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}

func firstLine(data []byte) ([]byte, error) {
	lines := bytes.Split(data, []byte("\n"))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) > 0 {
			return line, nil
		}
	}
	return nil, io.EOF
}

// ---------------------------------------------------------------------------
// PluginManager — discovers and loads external plugins from a directory
// ---------------------------------------------------------------------------

// Manager discovers and manages external plugins from a directory.
type Manager struct {
	dir     string
	plugins map[string]*ExternalPlugin
}

// NewManager creates a Manager that scans pluginsDir for executables.
func NewManager(pluginsDir string) (*Manager, error) {
	m := &Manager{
		dir:     pluginsDir,
		plugins: make(map[string]*ExternalPlugin),
	}
	if err := m.scan(); err != nil {
		return nil, err
	}
	return m, nil
}

// scan loads all executable files in the plugins directory.
func (m *Manager) scan() error {
	entries, err := os.ReadDir(m.dir)
	if os.IsNotExist(err) {
		return nil // no plugins dir is fine
	}
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(m.dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// Check executable bit
		if info.Mode()&0o111 == 0 {
			continue
		}
		// Skip files without axon-plugin prefix (optional convention)
		if !strings.HasPrefix(entry.Name(), "axon-plugin-") && !strings.HasPrefix(entry.Name(), "axon_plugin_") {
			continue
		}

		ep, err := NewExternalPlugin(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠️  Skipping plugin %s: %v\n", entry.Name(), err)
			continue
		}
		desc := ep.Describe()
		m.plugins[desc.SourceType] = ep
		fmt.Printf("🔌 Loaded plugin: %s (%s)\n", entry.Name(), desc.SourceType)
	}
	return nil
}

// Get returns the external plugin for the given source type, if any.
func (m *Manager) Get(sourceType string) (*ExternalPlugin, bool) {
	ep, ok := m.plugins[sourceType]
	return ep, ok
}

// List returns all loaded external plugins.
func (m *Manager) List() []*ExternalPlugin {
	var result []*ExternalPlugin
	for _, ep := range m.plugins {
		result = append(result, ep)
	}
	return result
}

// RegisterAll registers all external plugins into a plugin.Registry.
func (m *Manager) RegisterAll(reg *plugin.Registry) {
	for _, ep := range m.plugins {
		reg.Register(ep)
	}
}

// ---------------------------------------------------------------------------
// SDK helpers — for writing external plugins in Go
// ---------------------------------------------------------------------------

// Run is a convenience helper for writing external plugins in Go.
// It reads a single JSON-RPC request from stdin, dispatches to handler,
// and writes the response to stdout.
//
// Usage in a plugin main.go:
//
//	func main() {
//	    extplugin.Run(extplugin.Handler{
//	        DescribeFn: func() (string, string) { return "notion", "Notion page importer" },
//	        FetchFn:    myFetch,
//	    })
//	}
type Handler struct {
	DescribeFn    func() (sourceType, description string)
	FetchFn       func(ctx context.Context, origin string, opts map[string]any) (*plugin.SourceData, error)
	HasChangedFn  func(origin, lastHash string) (bool, error)
	RelationsFn   func(content string) ([]plugin.RelationHint, error)
}

func Run(h Handler) {
	var req rpcRequest
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		writeError(fmt.Sprintf("invalid request: %v", err))
		os.Exit(1)
	}

	var result any
	var handlerErr error

	switch req.Method {
	case "describe":
		if h.DescribeFn == nil {
			// describe is mandatory — panic loudly so plugin developers
			// notice immediately during development.
			fmt.Fprintf(os.Stderr, "[axon-plugin] FATAL: DescribeFn is nil — every plugin must implement Describe()\n")
			os.Exit(2)
		}
		st, desc := h.DescribeFn()
		result = map[string]string{"source_type": st, "description": desc}

	case "fetch":
		if h.FetchFn == nil {
			// fetch is the primary action — treat missing impl as a fatal
			// misconfiguration rather than a silent error.
			fmt.Fprintf(os.Stderr, "[axon-plugin] FATAL: FetchFn is nil — this plugin cannot fetch sources\n")
			os.Exit(2)
		}
		origin, _ := req.Params["origin"].(string)
		data, err := h.FetchFn(context.Background(), origin, req.Params)
		if err != nil {
			handlerErr = err
		} else {
			result = map[string]any{
				"plain_text":  data.PlainText,
				"title":       data.Title,
				"raw_mime":    data.RawMime,
				"lang":        data.Lang,
				"raw_content": string(data.RawContent),
				"meta":        data.Meta,
			}
		}

	case "has_changed":
		if h.HasChangedFn == nil {
			result = map[string]bool{"changed": true}
		} else {
			origin, _ := req.Params["origin"].(string)
			lastHash, _ := req.Params["last_hash"].(string)
			changed, err := h.HasChangedFn(origin, lastHash)
			if err != nil {
				handlerErr = err
			} else {
				result = map[string]bool{"changed": changed}
			}
		}

	case "relations":
		if h.RelationsFn == nil {
			result = map[string]any{"relations": []any{}}
		} else {
			content, _ := req.Params["content"].(string)
			hints, err := h.RelationsFn(content)
			if err != nil {
				handlerErr = err
			} else {
				var relations []map[string]string
				for _, h := range hints {
					relations = append(relations, map[string]string{
						"to_origin": h.ToOrigin,
						"rel_type":  h.RelType,
						"evidence":  h.Evidence,
					})
				}
				result = map[string]any{"relations": relations}
			}
		}

	default:
		writeError(fmt.Sprintf("unknown method: %s", req.Method))
		return
	}

	if handlerErr != nil {
		writeError(handlerErr.Error())
		return
	}

	resultBytes, _ := json.Marshal(result)
	resp := rpcResponse{Result: json.RawMessage(resultBytes)}
	respBytes, _ := json.Marshal(resp)
	fmt.Println(string(respBytes))
}

func writeError(msg string) {
	resp := rpcResponse{Error: msg}
	b, _ := json.Marshal(resp)
	fmt.Println(string(b))
}
