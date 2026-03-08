package plugin

import (
	"fmt"
	"strings"
)

// Registry manages all available source plugins.
type Registry struct {
	plugins map[string]SourcePlugin
}

// NewRegistry returns a Registry with all built-in plugins registered.
func NewRegistry() *Registry {
	r := &Registry{plugins: make(map[string]SourcePlugin)}
	r.Register(&FilePlugin{})
	r.Register(&URLPlugin{})
	r.Register(&SnippetPlugin{})
	r.Register(&PDFPlugin{})
	return r
}

// Register adds a plugin to the registry.
func (r *Registry) Register(p SourcePlugin) {
	r.plugins[p.Describe().SourceType] = p
}

// Get returns the plugin for the given source type.
func (r *Registry) Get(sourceType string) (SourcePlugin, error) {
	p, ok := r.plugins[sourceType]
	if !ok {
		return nil, fmt.Errorf("no plugin for source type %q", sourceType)
	}
	return p, nil
}

// DetectSourceType infers the source type from an origin string.
func DetectSourceType(origin string) string {
	if strings.HasPrefix(origin, "http://") || strings.HasPrefix(origin, "https://") {
		return "url"
	}
	if strings.HasPrefix(origin, "axon://snippet/") {
		return "snippet"
	}
	if isPDF(origin) {
		return "pdf"
	}
	return "file"
}
