// Package watch implements file-system watching for automatic re-ingestion.
//
// Strategy: poll-based (no CGO/fsnotify dependency) using a ticker.
// Walk the directory every `interval`, compare file mtime + size against
// a local snapshot. This keeps the binary dependency-free and cross-platform.
//
// Events emitted:
//   - Created  — new file found
//   - Modified — mtime or size changed
//   - Deleted  — file disappeared from snapshot
//
// Debounce: each path has an independent timer. A burst of rapid saves
// (common in editors) collapses into a single event emitted only after
// the path has been stable for DebounceDelay.
package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DebounceDelay is how long a path must be stable before its event fires.
const DebounceDelay = 2 * time.Second

// EventType classifies a filesystem event.
type EventType int

const (
	EventCreated  EventType = iota
	EventModified EventType = iota
	EventDeleted  EventType = iota
)

func (e EventType) String() string {
	switch e {
	case EventCreated:
		return "created"
	case EventModified:
		return "modified"
	case EventDeleted:
		return "deleted"
	default:
		return "unknown"
	}
}

// Event carries information about a single file change.
type Event struct {
	Type EventType
	Path string
}

// fileState tracks what we know about a file.
type fileState struct {
	ModTime time.Time
	Size    int64
}

// pendingEvent holds a debounced event waiting to be emitted.
type pendingEvent struct {
	event Event
	timer *time.Timer
}

// Config controls watcher behaviour.
type Config struct {
	// Dirs is the list of directories to watch (recursively).
	Dirs []string

	// Exts is the whitelist of extensions to care about (e.g. ".md", ".txt").
	// Empty means watch everything.
	Exts []string

	// Interval is how often to poll. Default: 3 seconds.
	Interval time.Duration

	// IgnoreDotfiles skips hidden files/dirs (names starting with ".").
	IgnoreDotfiles bool

	// DebounceDelay overrides the package-level DebounceDelay constant.
	// Zero means use the package default (2s).
	DebounceDelay time.Duration
}

// Watcher polls directories and emits Events.
type Watcher struct {
	cfg           Config
	snapshot      map[string]fileState
	mu            sync.Mutex
	Events        chan Event
	debounceDelay time.Duration

	// pending holds per-path debounce timers (protected by mu).
	pending map[string]*pendingEvent
}

// New creates a Watcher but does not start it.
func New(cfg Config) *Watcher {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	dd := cfg.DebounceDelay
	if dd <= 0 {
		dd = DebounceDelay
	}
	return &Watcher{
		cfg:           cfg,
		snapshot:      make(map[string]fileState),
		Events:        make(chan Event, 256),
		debounceDelay: dd,
		pending:       make(map[string]*pendingEvent),
	}
}

// Run starts polling until ctx is cancelled. Blocks.
func (w *Watcher) Run(ctx context.Context) error {
	// Build initial snapshot without emitting events.
	if err := w.buildSnapshot(); err != nil {
		return fmt.Errorf("initial scan: %w", err)
	}
	fmt.Printf("👁  Watching %s (polling every %v, debounce %v)\n",
		strings.Join(w.cfg.Dirs, ", "), w.cfg.Interval, w.debounceDelay)

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Cancel all pending debounce timers before closing channel.
			w.mu.Lock()
			for _, p := range w.pending {
				p.timer.Stop()
			}
			w.mu.Unlock()
			close(w.Events)
			return nil
		case <-ticker.C:
			w.poll()
		}
	}
}

// buildSnapshot walks all dirs and records current state. No events emitted.
func (w *Watcher) buildSnapshot() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, dir := range w.cfg.Dirs {
		if err := w.walkDir(dir, func(path string, info os.FileInfo) {
			w.snapshot[path] = fileState{ModTime: info.ModTime(), Size: info.Size()}
		}); err != nil {
			return err
		}
	}
	return nil
}

// poll compares current filesystem state against snapshot and emits events.
func (w *Watcher) poll() {
	w.mu.Lock()
	defer w.mu.Unlock()

	current := make(map[string]fileState)

	for _, dir := range w.cfg.Dirs {
		_ = w.walkDir(dir, func(path string, info os.FileInfo) {
			fs := fileState{ModTime: info.ModTime(), Size: info.Size()}
			current[path] = fs

			prev, exists := w.snapshot[path]
			if !exists {
				w.scheduleDebounced(Event{Type: EventCreated, Path: path})
			} else if fs.ModTime != prev.ModTime || fs.Size != prev.Size {
				w.scheduleDebounced(Event{Type: EventModified, Path: path})
			}
		})
	}

	// Detect deletions — deletions are NOT debounced (fire immediately).
	for path := range w.snapshot {
		if _, ok := current[path]; !ok {
			// Cancel any pending debounce for this path (e.g. create+delete).
			if p, ok := w.pending[path]; ok {
				p.timer.Stop()
				delete(w.pending, path)
			}
			w.emit(Event{Type: EventDeleted, Path: path})
		}
	}

	w.snapshot = current
}

// scheduleDebounced arms (or re-arms) a per-path debounce timer.
// Must be called with w.mu held.
func (w *Watcher) scheduleDebounced(e Event) {
	if p, ok := w.pending[e.Path]; ok {
		// Reset the timer: the file is still changing.
		p.timer.Reset(w.debounceDelay)
		p.event = e // update event type (e.g. re-arm as Modified)
		return
	}
	// First event for this path — create a new timer.
	p := &pendingEvent{event: e}
	p.timer = time.AfterFunc(w.debounceDelay, func() {
		w.mu.Lock()
		evt, ok := w.pending[e.Path]
		if ok {
			delete(w.pending, e.Path)
		}
		w.mu.Unlock()
		if ok {
			w.emit(evt.event)
		}
	})
	w.pending[e.Path] = p
}

// walkDir walks a directory, calling fn for each matching file.
func (w *Watcher) walkDir(root string, fn func(string, os.FileInfo)) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable paths
		}
		name := filepath.Base(path)
		if w.cfg.IgnoreDotfiles && strings.HasPrefix(name, ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if len(w.cfg.Exts) > 0 && !w.matchExt(path) {
			return nil
		}
		fn(path, info)
		return nil
	})
}

func (w *Watcher) matchExt(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	for _, e := range w.cfg.Exts {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

func (w *Watcher) emit(e Event) {
	select {
	case w.Events <- e:
	default:
		fmt.Printf("⚠️  [watcher] event channel full, dropping %s event for: %s\n", e.Type, e.Path)
	}
}
