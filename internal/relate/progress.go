// Package relate — progress.go handles LLM extraction progress persistence.
// This allows axon relate --llm to resume from where it left off if interrupted.
//
// Progress is stored in a lightweight JSON file at:
//   <axon_dir>/llm_progress_<job_id>.json
//
// The job ID is derived from the options (collection + source filter).
// On completion, the progress file is removed automatically.
package relate

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// progressFile is the on-disk progress state for an LLM extraction job.
type progressFile struct {
	JobID       string            `json:"job_id"`
	StartedAt   time.Time         `json:"started_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Collection  string            `json:"collection,omitempty"`
	SourceID    string            `json:"source_id,omitempty"`
	MaxChunks   int               `json:"max_chunks"`
	Done        map[string]bool   `json:"done"`   // source ID → processed
	Stats       progressStats     `json:"stats"`
}

type progressStats struct {
	Sources   int `json:"sources"`
	Chunks    int `json:"chunks"`
	Triples   int `json:"triples"`
	Relations int `json:"relations"`
}

// progressManager handles reading/writing progress state.
type progressManager struct {
	path string
	pf   *progressFile
}

// newProgressManager creates (or resumes) a progress file for the given options.
func newProgressManager(dir string, opts LLMOptions) (*progressManager, error) {
	jobID := jobIDFor(opts)
	path := filepath.Join(dir, fmt.Sprintf("llm_progress_%s.json", jobID))

	pm := &progressManager{path: path}

	// Try to load existing progress
	if data, err := os.ReadFile(path); err == nil {
		var pf progressFile
		if json.Unmarshal(data, &pf) == nil {
			pm.pf = &pf
			return pm, nil
		}
	}

	// Fresh start
	pm.pf = &progressFile{
		JobID:      jobID,
		StartedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Collection: opts.Collection,
		SourceID:   opts.SourceID,
		MaxChunks:  opts.MaxChunks,
		Done:       make(map[string]bool),
	}
	return pm, pm.save()
}

// isDone returns true if this source was already processed.
func (pm *progressManager) isDone(sourceID string) bool {
	return pm.pf.Done[sourceID]
}

// markDone marks a source as processed and persists.
func (pm *progressManager) markDone(sourceID string, chunks, triples, relations int) error {
	pm.pf.Done[sourceID] = true
	pm.pf.Stats.Sources++
	pm.pf.Stats.Chunks += chunks
	pm.pf.Stats.Triples += triples
	pm.pf.Stats.Relations += relations
	pm.pf.UpdatedAt = time.Now()
	return pm.save()
}

// resume returns previously accumulated stats (for display on resume).
func (pm *progressManager) resume() (int, int, int, int) {
	s := pm.pf.Stats
	return s.Sources, s.Chunks, s.Triples, s.Relations
}

// wasResumed reports if an existing progress file was found.
func (pm *progressManager) wasResumed() bool {
	return !pm.pf.StartedAt.IsZero() && len(pm.pf.Done) > 0
}

// doneCount is how many sources are already processed.
func (pm *progressManager) doneCount() int {
	return len(pm.pf.Done)
}

// complete removes the progress file (job finished).
func (pm *progressManager) complete() {
	_ = os.Remove(pm.path)
}

func (pm *progressManager) save() error {
	data, err := json.MarshalIndent(pm.pf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pm.path, data, 0600)
}

// jobIDFor derives a stable short ID from the LLM options.
func jobIDFor(opts LLMOptions) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d", opts.Collection, opts.SourceID, opts.MaxChunks)
	return fmt.Sprintf("%x", h.Sum(nil))[:12]
}
