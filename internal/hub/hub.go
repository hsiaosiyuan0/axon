// Package hub provides a simple pub/sub event hub for broadcasting
// knowledge-base change events to WebSocket subscribers.
//
// Usage:
//
//	h := hub.New()
//	go h.Run()
//	h.Publish(hub.Event{Type: "ingest", Payload: ...})
//	// In HTTP handler:
//	h.ServeSSE(w, r)
package hub

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event is broadcast to all connected WebSocket clients.
type Event struct {
	Type      string `json:"type"`       // "ingest" | "delete" | "relate" | "ping"
	SourceID  string `json:"source_id,omitempty"`
	Title     string `json:"title,omitempty"`
	Collection string `json:"collection,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any    `json:"payload,omitempty"`
}

// Hub manages a set of WebSocket subscribers.
type Hub struct {
	mu       sync.RWMutex
	clients  map[chan []byte]struct{}
	publish  chan Event
	done     chan struct{}
}

// New creates a new Hub. Call Run() in a goroutine before publishing.
func New() *Hub {
	return &Hub{
		clients: make(map[chan []byte]struct{}),
		publish: make(chan Event, 64),
		done:    make(chan struct{}),
	}
}

// Run processes published events and fan-outs to all subscribers.
// Run blocks; call it in a goroutine.
func (h *Hub) Run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-h.done:
			return
		case <-ticker.C:
			h.Publish(Event{Type: "ping", Timestamp: time.Now()})
		case ev := <-h.publish:
			if ev.Timestamp.IsZero() {
				ev.Timestamp = time.Now()
			}
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			h.mu.RLock()
			for ch := range h.clients {
				select {
				case ch <- data:
				default:
					// Slow subscriber — skip rather than block
				}
			}
			h.mu.RUnlock()
		}
	}
}

// Publish sends an event to all subscribers (non-blocking).
func (h *Hub) Publish(ev Event) {
	select {
	case h.publish <- ev:
	default:
	}
}

// Close stops the hub.
func (h *Hub) Close() { close(h.done) }

// subscribe registers a new channel and returns an unsubscribe func.
func (h *Hub) subscribe() (chan []byte, func()) {
	ch := make(chan []byte, 32)
	h.mu.Lock()
	h.clients[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.clients, ch)
		h.mu.Unlock()
		close(ch)
	}
}

// ── HTTP handler (Server-Sent Events) ────────────────────────────────────────
// We use SSE instead of raw WebSocket to avoid external dependencies.
// Clients can connect with:  curl -N http://localhost:7474/v1/watch
// or   new EventSource("/v1/watch") in a browser.

// ServeSSE handles GET /v1/watch as a Server-Sent Events stream.
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Send connected event
	fmt.Fprintf(w, "data: {\"type\":\"connected\",\"timestamp\":%q}\n\n",
		time.Now().Format(time.RFC3339))
	flusher.Flush()

	ch, unsub := h.subscribe()
	defer unsub()

	for {
		select {
		case <-r.Context().Done():
			return
		case data, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// Subscribers returns the current number of connected clients.
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
