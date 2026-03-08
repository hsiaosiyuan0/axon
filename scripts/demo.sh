#!/usr/bin/env bash
# demo.sh — Interactive demo script for Axon v0.1.0
# Usage: ./scripts/demo.sh
# Tip:   Record with `vhs demo.tape` or `asciinema rec`

set -e

AXON=${AXON:-./axon}
DEMO_DB=$(mktemp -d)/demo.db

echo "🦞 Axon v0.1.0 Demo"
echo "===================="
echo ""
sleep 0.5

# ── Step 1: Init ─────────────────────────────────────────────────────────────
echo "$ axon --db $DEMO_DB init"
sleep 0.3
$AXON --db "$DEMO_DB" init
echo ""
sleep 0.5

# ── Step 2: Create a collection ──────────────────────────────────────────────
echo "$ axon --db $DEMO_DB collection new --name \"Go Notes\" --type notes"
sleep 0.3
$AXON --db "$DEMO_DB" collection new --name "Go Notes" --type notes
echo ""
sleep 0.5

# ── Step 3: Add some content ─────────────────────────────────────────────────
# Write a sample markdown file
TMPFILE=$(mktemp /tmp/goroutines.md)
cat > "$TMPFILE" << 'EOF'
# Go Concurrency Patterns

## Goroutines

A goroutine is a lightweight thread managed by the Go runtime.
You start a goroutine by using the `go` keyword before a function call.

```go
go func() {
    fmt.Println("Hello from goroutine")
}()
```

## Channels

Channels are the conduit through which goroutines communicate.

```go
ch := make(chan int)
go func() { ch <- 42 }()
val := <-ch
```

## Select Statement

The `select` statement lets a goroutine wait on multiple communication operations.

```go
select {
case msg := <-ch1:
    fmt.Println("ch1:", msg)
case msg := <-ch2:
    fmt.Println("ch2:", msg)
}
```
EOF

echo "$ axon --db $DEMO_DB add /tmp/goroutines.md -c \"Go Notes\""
sleep 0.3
$AXON --db "$DEMO_DB" add "$TMPFILE" -c "Go Notes"
echo ""
sleep 0.5

# Add a URL
echo "$ axon --db $DEMO_DB add https://go.dev/blog/pipelines -c \"Go Notes\""
sleep 0.3
$AXON --db "$DEMO_DB" add https://go.dev/blog/pipelines -c "Go Notes" || echo "  (URL ingestion requires network)"
echo ""
sleep 0.5

# ── Step 4: List sources ──────────────────────────────────────────────────────
echo "$ axon --db $DEMO_DB list"
sleep 0.3
$AXON --db "$DEMO_DB" list
echo ""
sleep 0.5

# ── Step 5: Query ─────────────────────────────────────────────────────────────
echo "$ axon --db $DEMO_DB query \"goroutine channel communication\""
sleep 0.3
$AXON --db "$DEMO_DB" query "goroutine channel communication"
echo ""
sleep 0.5

# ── Step 6: Status ────────────────────────────────────────────────────────────
echo "$ axon --db $DEMO_DB status"
sleep 0.3
$AXON --db "$DEMO_DB" status
echo ""
sleep 0.5

# ── Cleanup ───────────────────────────────────────────────────────────────────
rm -f "$TMPFILE"
echo "✅ Demo complete!"
echo ""
echo "Next steps:"
echo "  axon tui          # Interactive search UI"
echo "  axon serve        # HTTP API + Web UI at http://localhost:7474/ui"
echo "  axon mcp          # MCP server for Claude Desktop"
echo "  axon watch ~/notes/ --daemon  # Background file watcher"
