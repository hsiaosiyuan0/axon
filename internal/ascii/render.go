// Package ascii renders an Axon graph as plain-text art in the terminal.
//
// Layout strategy: nodes are arranged in columns by "layer" (BFS depth from a
// virtual root).  Edges are shown as simple arrow lines between labels.
// For large graphs we fall back to a compact adjacency list view.
package ascii

import (
	"fmt"
	"io"
	"strings"

	"github.com/hsiaosiyuan0/axon/internal/graph"
)

const (
	maxFullNodes = 40 // above this, switch to list view
	colWidth     = 30
)

// Render writes an ASCII representation of g to w.
func Render(w io.Writer, g *graph.Graph) {
	if len(g.Nodes) == 0 {
		fmt.Fprintln(w, "(empty graph — no sources or relations found)")
		return
	}

	if len(g.Nodes) > maxFullNodes {
		renderCompact(w, g)
		return
	}
	renderTree(w, g)
}

// ── Tree / layered view ───────────────────────────────────────────────────────

func renderTree(w io.Writer, g *graph.Graph) {
	// Build adjacency maps
	outEdges := make(map[string][]graph.Edge)
	inDegree := make(map[string]int)
	nodeByID := make(map[string]graph.Node)

	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
		inDegree[n.ID] = 0
	}
	for _, e := range g.Edges {
		outEdges[e.From] = append(outEdges[e.From], e)
		inDegree[e.To]++
		if e.Bidir {
			inDegree[e.From]++
		}
	}

	// BFS layers (roots = nodes with no incoming edges)
	var roots []string
	for _, n := range g.Nodes {
		if inDegree[n.ID] == 0 {
			roots = append(roots, n.ID)
		}
	}
	if len(roots) == 0 {
		// Cyclic — just pick first node
		roots = []string{g.Nodes[0].ID}
	}

	layers := bfsLayers(roots, outEdges)

	// Render header
	fmt.Fprintf(w, "\n  ┌─ Axon Knowledge Graph ── %d nodes, %d edges ─\n\n",
		len(g.Nodes), len(g.Edges))

	// Render each layer
	for i, layer := range layers {
		prefix := strings.Repeat("    ", i)
		connector := "├──"
		if i == 0 {
			connector = "◉"
		}
		for j, id := range layer {
			n := nodeByID[id]
			icon := nodeIcon(n.Type)
			label := truncate(n.Label, colWidth)

			if i == 0 {
				fmt.Fprintf(w, "  %s %s %s\n", connector, icon, label)
			} else {
				_ = j
				fmt.Fprintf(w, "  %s%s %s %s\n", prefix, connector, icon, label)
			}

			// Print outgoing edges
			for _, e := range outEdges[id] {
				toNode, ok := nodeByID[e.To]
				if !ok {
					// Phantom / pending
					toLabel := e.ToOrigin
					if toLabel == "" {
						toLabel = e.To
					}
					fmt.Fprintf(w, "  %s    %s [%s] ──→ ⚪ %s (pending)\n",
						prefix, strings.Repeat(" ", i*2), e.Label, truncate(toLabel, 20))
					continue
				}
				arrow := "──→"
				if e.Bidir {
					arrow = "←─→"
				}
				fmt.Fprintf(w, "  %s    %s [%s] %s %s %s\n",
					prefix, strings.Repeat(" ", i*2),
					e.Label, arrow,
					nodeIcon(toNode.Type), truncate(toNode.Label, 20))
			}
		}
	}

	// Legend
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  Legend: ◉ root  📄 source  ⏳ pending")
	fmt.Fprintln(w)
}

func bfsLayers(roots []string, adj map[string][]graph.Edge) [][]string {
	visited := make(map[string]bool)
	var layers [][]string
	current := roots

	for len(current) > 0 {
		layers = append(layers, current)
		for _, id := range current {
			visited[id] = true
		}
		var next []string
		for _, id := range current {
			for _, e := range adj[id] {
				if !visited[e.To] {
					visited[e.To] = true
					next = append(next, e.To)
				}
			}
		}
		current = next
	}
	return layers
}

// ── Compact adjacency list view ───────────────────────────────────────────────

func renderCompact(w io.Writer, g *graph.Graph) {
	nodeByID := make(map[string]graph.Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}
	outEdges := make(map[string][]graph.Edge)
	for _, e := range g.Edges {
		outEdges[e.From] = append(outEdges[e.From], e)
	}

	fmt.Fprintf(w, "\n  Axon Graph — %d nodes, %d edges\n", len(g.Nodes), len(g.Edges))
	fmt.Fprintln(w, "  (compact view — too many nodes for tree layout)")
	fmt.Fprintln(w)

	for _, n := range g.Nodes {
		icon := nodeIcon(n.Type)
		fmt.Fprintf(w, "  %s %-28s [%s]\n", icon, truncate(n.Label, 28), n.Collection)
		for _, e := range outEdges[n.ID] {
			to, ok := nodeByID[e.To]
			toLabel := e.ToOrigin
			if ok {
				toLabel = to.Label
			}
			arrow := "→"
			if e.Bidir {
				arrow = "↔"
			}
			fmt.Fprintf(w, "       %s %-10s %s %s\n",
				arrow, e.Label, nodeIcon(to.Type), truncate(toLabel, 22))
		}
	}
	fmt.Fprintln(w)
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func nodeIcon(t string) string {
	switch t {
	case "source":
		return "📄"
	case "chunk":
		return "🔹"
	case "pending":
		return "⏳"
	default:
		return "◦"
	}
}

func truncate(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}
