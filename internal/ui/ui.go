// Package ui embeds the single-page web interface for Axon.
// The UI is served at /ui by the HTTP API server.
// It provides:
//   - D3.js force-directed knowledge graph
//   - Full-text search panel
//   - Collection browser
//   - Source detail view
package ui

import _ "embed"

// IndexHTML is the single-file web app.
//
//go:embed index.html
var IndexHTML []byte
