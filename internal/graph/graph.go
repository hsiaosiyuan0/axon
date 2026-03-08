// Package graph builds an in-memory graph of sources and their relations,
// used by both the REST API (/v1/graph) and the terminal visualiser (axon graph).
package graph

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/store"
)

// ── Data model ────────────────────────────────────────────────────────────────

// Node represents a knowledge item (source or chunk) in the graph.
type Node struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Type       string `json:"type"`       // "source" | "chunk"
	Collection string `json:"collection"`
	Origin     string `json:"origin,omitempty"`
}

// Edge represents a directed relationship between two nodes.
type Edge struct {
	ID     string  `json:"id"`
	From   string  `json:"from"`
	To     string  `json:"to"`
	ToOrigin string `json:"to_origin,omitempty"` // pending wikilink
	Label  string  `json:"label"`
	Weight float64 `json:"weight"`
	Bidir  bool    `json:"bidirectional"`
}

// Graph is the full nodes + edges response.
type Graph struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// ── Builder ───────────────────────────────────────────────────────────────────

// BuildOptions controls what gets included in the graph.
type BuildOptions struct {
	Collection string // filter to a specific collection; "" = all
	MaxNodes   int    // cap nodes (0 = unlimited)
	RootID     string // start from a specific node (depth-limited BFS)
	Depth      int    // BFS depth from RootID (0 = no limit)
}

// Build constructs a Graph from the database.
func Build(db *store.DB, opts BuildOptions) (*Graph, error) {
	// 1. Load sources
	var sources []store.Source
	if opts.Collection != "" {
		col, err := db.Collections().Get(opts.Collection)
		if err != nil {
			return nil, fmt.Errorf("collection %q not found: %w", opts.Collection, err)
		}
		sources, err = db.Sources().ListByCollection(col.ID)
		if err != nil {
			return nil, err
		}
	} else {
		var err error
		sources, err = db.Sources().List()
		if err != nil {
			return nil, err
		}
	}

	// Cap nodes
	if opts.MaxNodes > 0 && len(sources) > opts.MaxNodes {
		sources = sources[:opts.MaxNodes]
	}

	// Build node map
	nodeMap := make(map[string]Node, len(sources))
	for _, src := range sources {
		n := Node{
			ID:         src.ID,
			Label:      nodeLabel(src),
			Type:       "source",
			Collection: src.Collection,
			Origin:     src.Origin,
		}
		nodeMap[src.ID] = n
	}

	// 2. Load all relations
	rels, err := db.Relations().ListAll()
	if err != nil {
		return nil, err
	}

	// Build edges (only for nodes we have)
	var edges []Edge
	for _, rel := range rels {
		// Include edge if either endpoint is in our node set
		_, fromOK := nodeMap[rel.FromID]
		_, toOK := nodeMap[rel.ToID]
		if !fromOK && !toOK {
			continue
		}
		// Auto-add phantom nodes for pending wikilinks
		if rel.ToID == "" && rel.ToOrigin != "" {
			phantomID := "pending:" + rel.ToOrigin
			if _, exists := nodeMap[phantomID]; !exists {
				nodeMap[phantomID] = Node{
					ID:    phantomID,
					Label: rel.ToOrigin,
					Type:  "pending",
				}
			}
		}
		toID := rel.ToID
		if toID == "" {
			toID = "pending:" + rel.ToOrigin
		}
		edges = append(edges, Edge{
			ID:       rel.ID,
			From:     rel.FromID,
			To:       toID,
			ToOrigin: rel.ToOrigin,
			Label:    rel.RelType,
			Weight:   rel.Weight,
			Bidir:    rel.Bidirectional,
		})
	}

	// 3. BFS depth filter (if RootID is set)
	if opts.RootID != "" && opts.Depth > 0 {
		nodeMap, edges = bfsFilter(nodeMap, edges, opts.RootID, opts.Depth)
	}

	// Flatten to slices
	nodes := make([]Node, 0, len(nodeMap))
	for _, n := range nodeMap {
		nodes = append(nodes, n)
	}

	return &Graph{Nodes: nodes, Edges: edges}, nil
}

// ── BFS filter ────────────────────────────────────────────────────────────────

func bfsFilter(nodes map[string]Node, edges []Edge, rootID string, depth int) (map[string]Node, []Edge) {
	// Build adjacency
	adj := make(map[string][]string)
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		if e.Bidir {
			adj[e.To] = append(adj[e.To], e.From)
		}
	}

	visited := map[string]int{rootID: 0}
	queue := []string{rootID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		d := visited[cur]
		if d >= depth {
			continue
		}
		for _, nb := range adj[cur] {
			if _, seen := visited[nb]; !seen {
				visited[nb] = d + 1
				queue = append(queue, nb)
			}
		}
	}

	filteredNodes := make(map[string]Node)
	for id := range visited {
		if n, ok := nodes[id]; ok {
			filteredNodes[id] = n
		}
	}

	var filteredEdges []Edge
	for _, e := range edges {
		if _, ok := filteredNodes[e.From]; !ok {
			continue
		}
		if _, ok := filteredNodes[e.To]; !ok {
			continue
		}
		filteredEdges = append(filteredEdges, e)
	}

	return filteredNodes, filteredEdges
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func nodeLabel(src store.Source) string {
	if src.Title != "" {
		return src.Title
	}
	base := filepath.Base(src.Origin)
	// strip extension
	if idx := strings.LastIndex(base, "."); idx > 0 {
		base = base[:idx]
	}
	if base == "" || base == "." {
		return src.Origin
	}
	return base
}
