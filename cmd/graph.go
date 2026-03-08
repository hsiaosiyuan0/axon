package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/hsiaosiyuan0/axon/internal/ascii"
	"github.com/hsiaosiyuan0/axon/internal/config"
	"github.com/hsiaosiyuan0/axon/internal/graph"
	"github.com/hsiaosiyuan0/axon/internal/store"
	"github.com/spf13/cobra"
)

var graphCmd = &cobra.Command{
	Use:   "graph",
	Short: "Visualise the knowledge graph in the terminal",
	Long: `Render an ASCII graph of sources and their relations.

Examples:
  axon graph                         # all sources and edges
  axon graph -c notes                # only the "notes" collection
  axon graph --root <sourceID> -d 2  # 2-hop neighbourhood of a node
  axon graph --max-nodes 20          # cap to 20 nodes
  axon graph --json                  # output raw JSON (same as /v1/graph)
`,
	RunE: runGraph,
}

func init() {
	rootCmd.AddCommand(graphCmd)

	graphCmd.Flags().StringP("collection", "c", "", "Filter to a specific collection")
	graphCmd.Flags().String("root", "", "Start BFS from this node ID")
	graphCmd.Flags().IntP("depth", "d", 0, "BFS depth from root (0 = unlimited)")
	graphCmd.Flags().Int("max-nodes", 0, "Maximum nodes to include (0 = unlimited)")
	graphCmd.Flags().Bool("json", false, "Output raw JSON instead of ASCII art")
}

func runGraph(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load(globalDB)
	if err != nil {
		return err
	}

	collection, _ := cmd.Flags().GetString("collection")
	rootID, _ := cmd.Flags().GetString("root")
	depth, _ := cmd.Flags().GetInt("depth")
	maxNodes, _ := cmd.Flags().GetInt("max-nodes")
	asJSON, _ := cmd.Flags().GetBool("json")

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer db.Close()

	opts := graph.BuildOptions{
		Collection: collection,
		RootID:     rootID,
		Depth:      depth,
		MaxNodes:   maxNodes,
	}

	g, err := graph.Build(db, opts)
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	if asJSON {
		// Pretty JSON output
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"nodes": g.Nodes,
			"edges": g.Edges,
			"meta": map[string]any{
				"node_count": len(g.Nodes),
				"edge_count": len(g.Edges),
			},
		})
	}

	// ASCII render
	ascii.Render(os.Stdout, g)
	return nil
}
