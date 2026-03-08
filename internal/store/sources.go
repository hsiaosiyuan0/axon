package store

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Source represents an original document or data source.
type Source struct {
	ID          string
	Collection  string
	SourceType  string
	Origin      string
	OriginHash  string
	RawContent  []byte
	RawEncoding string
	RawMime     string
	PlainText   string
	Title       string
	Lang        string
	Meta        map[string]any
	CreatedAt   time.Time
	UpdatedAt   time.Time
	FetchedAt   *time.Time
}

type SourceRepo struct{ db *sql.DB }

type CreateSourceParams struct {
	Collection  string
	SourceType  string
	Origin      string
	OriginHash  string
	RawContent  []byte
	RawEncoding string
	RawMime     string
	PlainText   string
	Title       string
	Lang        string
	Meta        map[string]any
}

func (r *SourceRepo) Create(p CreateSourceParams) (*Source, error) {
	if p.RawEncoding == "" {
		p.RawEncoding = "utf-8"
	}
	metaJSON, _ := json.Marshal(p.Meta)
	now := time.Now()

	s := &Source{
		ID:          uuid.NewString(),
		Collection:  p.Collection,
		SourceType:  p.SourceType,
		Origin:      p.Origin,
		OriginHash:  p.OriginHash,
		RawContent:  p.RawContent,
		RawEncoding: p.RawEncoding,
		RawMime:     p.RawMime,
		PlainText:   p.PlainText,
		Title:       p.Title,
		Lang:        p.Lang,
		Meta:        p.Meta,
		CreatedAt:   now,
		UpdatedAt:   now,
		FetchedAt:   &now,
	}

	_, err := r.db.Exec(`
		INSERT INTO sources
			(id, collection, source_type, origin, origin_hash,
			 raw_content, raw_encoding, raw_mime, plain_text,
			 title, lang, meta, created_at, updated_at, fetched_at)
		VALUES (?,?,?,?,?, ?,?,?,?, ?,?,?,?,?,?)`,
		s.ID, s.Collection, s.SourceType, s.Origin, s.OriginHash,
		s.RawContent, s.RawEncoding, s.RawMime, s.PlainText,
		s.Title, s.Lang, string(metaJSON), s.CreatedAt, s.UpdatedAt, s.FetchedAt,
	)
	return s, err
}

func (r *SourceRepo) GetByOrigin(origin string) (*Source, error) {
	return r.getByOrigin(origin)
}

func (r *SourceRepo) GetByID(id string) (*Source, error) {
	return r.getByID(id)
}

// getByOrigin looks up a source by its origin field using a parameterised query.
func (r *SourceRepo) getByOrigin(origin string) (*Source, error) {
	var s Source
	var metaStr string
	err := r.db.QueryRow(`
		SELECT id, collection, source_type, origin, origin_hash, plain_text, title, lang, meta, created_at, updated_at
		FROM sources WHERE origin = ?`, origin).Scan(
		&s.ID, &s.Collection, &s.SourceType, &s.Origin, &s.OriginHash,
		&s.PlainText, &s.Title, &s.Lang, &metaStr, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(metaStr), &s.Meta)
	return &s, nil
}

// getByID looks up a source by its primary key using a parameterised query.
func (r *SourceRepo) getByID(id string) (*Source, error) {
	var s Source
	var metaStr string
	err := r.db.QueryRow(`
		SELECT id, collection, source_type, origin, origin_hash, plain_text, title, lang, meta, created_at, updated_at
		FROM sources WHERE id = ?`, id).Scan(
		&s.ID, &s.Collection, &s.SourceType, &s.Origin, &s.OriginHash,
		&s.PlainText, &s.Title, &s.Lang, &metaStr, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(metaStr), &s.Meta)
	return &s, nil
}

func (r *SourceRepo) UpdateHash(id, hash string) error {
	_, err := r.db.Exec(
		`UPDATE sources SET origin_hash = ?, updated_at = ? WHERE id = ?`,
		hash, time.Now(), id,
	)
	return err
}

// List returns all sources ordered by created_at desc.
func (r *SourceRepo) List() ([]Source, error) {
	rows, err := r.db.Query(`
		SELECT id, collection, source_type, origin, origin_hash, plain_text, title, lang, meta, created_at, updated_at
		FROM sources ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanSources(rows)
}

// ListByCollection returns all sources in a collection.
func (r *SourceRepo) ListByCollection(collectionID string) ([]Source, error) {
	rows, err := r.db.Query(`
		SELECT id, collection, source_type, origin, origin_hash, plain_text, title, lang, meta, created_at, updated_at
		FROM sources WHERE collection = ? ORDER BY created_at DESC`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanSources(rows)
}

func (r *SourceRepo) scanSources(rows interface{ Next() bool; Scan(...any) error; Err() error }) ([]Source, error) {
	var list []Source
	for rows.Next() {
		var s Source
		var metaStr string
		if err := rows.Scan(&s.ID, &s.Collection, &s.SourceType, &s.Origin, &s.OriginHash,
			&s.PlainText, &s.Title, &s.Lang, &metaStr, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(metaStr), &s.Meta)
		list = append(list, s)
	}
	return list, rows.Err()
}

// Delete removes a source and all associated chunks, embeddings, and relations.
// Embeddings and chunks are removed via SQLite ON DELETE CASCADE; we only need
// to explicitly delete relations (which reference source IDs, not chunk IDs).
func (r *SourceRepo) Delete(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Delete relations referencing this source (not covered by FK cascade).
	if _, err := tx.Exec(`DELETE FROM relations WHERE from_id = ? OR to_id = ?`, id, id); err != nil {
		return err
	}

	// Delete source — ON DELETE CASCADE propagates to chunks then embeddings.
	// FTS delete-trigger fires automatically when chunks rows are removed.
	if _, err := tx.Exec(`DELETE FROM sources WHERE id = ?`, id); err != nil {
		return err
	}

	return tx.Commit()
}

// GetByIDs returns sources for all given IDs in a single batch query.
// Missing IDs are silently ignored.
func (r *SourceRepo) GetByIDs(ids []string) ([]Source, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Build WHERE id IN (?,?,...)
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `
		SELECT id, collection, source_type, origin, origin_hash, plain_text, title, lang, meta, created_at, updated_at
		FROM sources WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return r.scanSources(rows)
}

// Count returns the total number of sources in the database.
func (r *SourceRepo) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&n)
	return n, err
}
