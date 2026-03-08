package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Collection represents a knowledge collection.
type Collection struct {
	ID            string
	Name          string
	Type          string
	Description   string
	ModelName     string
	ChunkStrategy string
	Meta          string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CollectionRepo struct{ db *sql.DB }

type CreateCollectionParams struct {
	Name          string
	Type          string
	Description   string
	ModelName     string
	ChunkStrategy string
}

func (r *CollectionRepo) Create(p CreateCollectionParams) (*Collection, error) {
	if p.Type == "" {
		p.Type = "custom"
	}
	if p.ChunkStrategy == "" {
		p.ChunkStrategy = "markdown"
	}

	c := &Collection{
		ID:            uuid.NewString(),
		Name:          p.Name,
		Type:          p.Type,
		Description:   p.Description,
		ModelName:     p.ModelName,
		ChunkStrategy: p.ChunkStrategy,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	_, err := r.db.Exec(`
		INSERT INTO collections (id, name, type, description, model_name, chunk_strategy, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.Name, c.Type, c.Description, c.ModelName, c.ChunkStrategy, c.CreatedAt, c.UpdatedAt,
	)
	return c, err
}

func (r *CollectionRepo) List() ([]Collection, error) {
	rows, err := r.db.Query(`
		SELECT id, name, type, description, model_name, chunk_strategy, created_at, updated_at
		FROM collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Type, &c.Description,
			&c.ModelName, &c.ChunkStrategy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}

// Get looks up a collection first by ID, then by name.
// Separating the two lookups avoids ambiguity when a UUID happens to collide
// with a collection name.
func (r *CollectionRepo) Get(id string) (*Collection, error) {
	var c Collection
	// Try by primary key first (exact, fast).
	err := r.db.QueryRow(`
		SELECT id, name, type, description, model_name, chunk_strategy, created_at, updated_at
		FROM collections WHERE id = ?`, id).Scan(
		&c.ID, &c.Name, &c.Type, &c.Description,
		&c.ModelName, &c.ChunkStrategy, &c.CreatedAt, &c.UpdatedAt,
	)
	if err == nil {
		return &c, nil
	}
	// Fall back to name lookup.
	err = r.db.QueryRow(`
		SELECT id, name, type, description, model_name, chunk_strategy, created_at, updated_at
		FROM collections WHERE name = ?`, id).Scan(
		&c.ID, &c.Name, &c.Type, &c.Description,
		&c.ModelName, &c.ChunkStrategy, &c.CreatedAt, &c.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Delete removes a collection by ID.
// Returns an error if the collection still contains sources (data-safety guard).
// Pass force=true to bypass the check and cascade-delete all contents via SQLite FK.
func (r *CollectionRepo) Delete(id string) error {
	return r.DeleteWithForce(id, false)
}

// DeleteWithForce removes a collection and, when force=true, all its sources/chunks/embeddings.
// When force=false, the operation is rejected if any source exists in the collection.
func (r *CollectionRepo) DeleteWithForce(id string, force bool) error {
	if !force {
		var count int
		if err := r.db.QueryRow(
			`SELECT COUNT(*) FROM sources WHERE collection = ?`, id,
		).Scan(&count); err != nil {
			return fmt.Errorf("pre-check sources: %w", err)
		}
		if count > 0 {
			return fmt.Errorf("collection %q still has %d source(s); use force=true to delete anyway", id, count)
		}
	}
	_, err := r.db.Exec(`DELETE FROM collections WHERE id = ?`, id)
	return err
}
