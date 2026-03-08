package watch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/watch"
)

// TestDebounce verifies that rapid file modifications collapse into one event.
func TestDebounce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "note.md")

	// Write initial content so watcher builds snapshot with the file present.
	if err := os.WriteFile(file, []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}

	debounce := 200 * time.Millisecond

	w := watch.New(watch.Config{
		Dirs:          []string{dir},
		Exts:          []string{".md"},
		Interval:      50 * time.Millisecond,
		DebounceDelay: debounce,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() { errCh <- w.Run(ctx) }()

	// Give the watcher time to build its initial snapshot.
	time.Sleep(100 * time.Millisecond)

	// Rapidly write the file 5 times.
	for i := 1; i <= 5; i++ {
		if err := os.WriteFile(file, []byte("v"+string(rune('0'+i))), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Wait for debounce window to flush.
	time.Sleep(debounce + 100*time.Millisecond)
	cancel()

	// Drain events.
	var events []watch.Event
	for e := range w.Events {
		events = append(events, e)
	}

	modified := 0
	for _, e := range events {
		if e.Type == watch.EventModified {
			modified++
		}
	}

	// Debounce should collapse 5 rapid saves into 1 event.
	if modified != 1 {
		t.Errorf("expected 1 modified event after debounce, got %d (all events: %v)", modified, events)
	}
}

// TestCreatedDeleted verifies create and immediate delete handling.
func TestCreatedDeleted(t *testing.T) {
	dir := t.TempDir()
	debounce := 200 * time.Millisecond

	w := watch.New(watch.Config{
		Dirs:          []string{dir},
		Exts:          []string{".md"},
		Interval:      50 * time.Millisecond,
		DebounceDelay: debounce,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = w.Run(ctx) }()
	time.Sleep(80 * time.Millisecond) // let snapshot build

	// Create a file.
	file := filepath.Join(dir, "new.md")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait for debounce.
	time.Sleep(debounce + 150*time.Millisecond)

	// Delete the file.
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	// Wait for delete event (not debounced).
	time.Sleep(200 * time.Millisecond)
	cancel()

	var events []watch.Event
	for e := range w.Events {
		events = append(events, e)
	}

	types := map[watch.EventType]int{}
	for _, e := range events {
		types[e.Type]++
	}

	if types[watch.EventCreated] != 1 {
		t.Errorf("expected 1 created event, got %d", types[watch.EventCreated])
	}
	if types[watch.EventDeleted] != 1 {
		t.Errorf("expected 1 deleted event, got %d", types[watch.EventDeleted])
	}
}
