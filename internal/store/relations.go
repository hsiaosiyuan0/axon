package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Relation represents a directed relationship between two knowledge items.
type Relation struct {
	ID             string
	FromType       string
	FromID         string
	FromCollection string
	ToType         string
	ToID           string
	ToCollection   string
	ToOrigin       string // pending wikilink target (not yet ingested)
	RelType        string
	Weight         float64
	Bidirectional  bool
	EstablishedBy  string
	Evidence       string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type RelationRepo struct{ db *sql.DB }

type CreateRelationParams struct {
	FromType       string
	FromID         string
	FromCollection string
	ToType         string
	ToID           string
	ToCollection   string
	ToOrigin       string // optional: pending wikilink target
	RelType        string
	Weight         float64
	Bidirectional  bool
	EstablishedBy  string
	Evidence       string
}

func (r *RelationRepo) Create(p CreateRelationParams) (*Relation, error) {
	if p.Weight == 0 {
		p.Weight = 1.0
	}
	now := time.Now()
	rel := &Relation{
		ID:             uuid.NewString(),
		FromType:       p.FromType,
		FromID:         p.FromID,
		FromCollection: p.FromCollection,
		ToType:         p.ToType,
		ToID:           p.ToID,
		ToCollection:   p.ToCollection,
		ToOrigin:       p.ToOrigin,
		RelType:        p.RelType,
		Weight:         p.Weight,
		Bidirectional:  p.Bidirectional,
		EstablishedBy:  p.EstablishedBy,
		Evidence:       p.Evidence,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	_, err := r.db.Exec(`
		INSERT INTO relations
			(id, from_type, from_id, from_collection, to_type, to_id, to_collection,
			 to_origin, rel_type, weight, bidirectional, established_by, evidence, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		rel.ID, rel.FromType, rel.FromID, rel.FromCollection,
		rel.ToType, rel.ToID, rel.ToCollection,
		rel.ToOrigin,
		rel.RelType, rel.Weight, rel.Bidirectional,
		rel.EstablishedBy, rel.Evidence, rel.CreatedAt, rel.UpdatedAt,
	)
	return rel, err
}

// ResolvePendingWikilinks resolves previously saved pending wikilinks that now
// point to a newly-ingested source. Call this after every Add().
func (r *RelationRepo) ResolvePendingWikilinks(newSource *Source) error {
	// Find all pending relations whose to_origin matches the new source's origin or name
	baseName := strings.TrimSuffix(filepath.Base(newSource.Origin), ".md")
	rows, err := r.db.Query(`
		SELECT id FROM relations
		WHERE to_id = '' AND (to_origin = ? OR to_origin = ?)`,
		newSource.Origin, baseName,
	)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	for _, id := range ids {
		_, err := r.db.Exec(`
			UPDATE relations
			SET to_id = ?, to_collection = ?, to_type = 'source', updated_at = CURRENT_TIMESTAMP
			WHERE id = ?`,
			newSource.ID, newSource.Collection, id,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *RelationRepo) ListByFrom(fromID string) ([]Relation, error) {
	return r.query(`WHERE from_id = ?`, fromID)
}

func (r *RelationRepo) ListByTo(toID string) ([]Relation, error) {
	return r.query(`WHERE to_id = ?`, toID)
}

func (r *RelationRepo) DeleteBySource(sourceID string) error {
	_, err := r.db.Exec(
		`DELETE FROM relations WHERE from_id = ? OR to_id = ?`, sourceID, sourceID)
	return err
}

func (r *RelationRepo) query(where string, args ...any) ([]Relation, error) {
	q := `SELECT id, from_type, from_id, from_collection, to_type, to_id, to_collection,
			     COALESCE(to_origin,''), rel_type, weight, bidirectional, established_by, evidence, created_at, updated_at
		  FROM relations ` + where
	rows, err := r.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rels []Relation
	for rows.Next() {
		var rel Relation
		if err := rows.Scan(
			&rel.ID, &rel.FromType, &rel.FromID, &rel.FromCollection,
			&rel.ToType, &rel.ToID, &rel.ToCollection, &rel.ToOrigin,
			&rel.RelType, &rel.Weight, &rel.Bidirectional,
			&rel.EstablishedBy, &rel.Evidence, &rel.CreatedAt, &rel.UpdatedAt,
		); err != nil {
			return nil, err
		}
		rels = append(rels, rel)
	}
	return rels, rows.Err()
}

// Count returns the total number of relations in the database.
func (r *RelationRepo) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM relations`).Scan(&n)
	return n, err
}

// ListAll returns every relation in the database.
func (r *RelationRepo) ListAll() ([]Relation, error) {
	return r.query(`ORDER BY created_at DESC`)
}
