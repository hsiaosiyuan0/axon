package store

import (
	"database/sql"
	"time"
)

// Model represents a registered embedding model.
type Model struct {
	Name        string
	Version     string
	Provider    string
	Dim         int
	Lang        string
	LocalPath   string
	APIConfig   string
	IsAvailable bool
	CreatedAt   time.Time
}

type ModelRepo struct{ db *sql.DB }

func (r *ModelRepo) List() ([]Model, error) {
	rows, err := r.db.Query(`
		SELECT name, version, provider, dim, lang, local_path, is_available, created_at
		FROM models ORDER BY provider, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []Model
	for rows.Next() {
		var m Model
		var localPath sql.NullString
		var version sql.NullString
		if err := rows.Scan(&m.Name, &version, &m.Provider, &m.Dim,
			&m.Lang, &localPath, &m.IsAvailable, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Version = version.String
		m.LocalPath = localPath.String
		models = append(models, m)
	}
	return models, rows.Err()
}

func (r *ModelRepo) Get(name string) (*Model, error) {
	var m Model
	var localPath sql.NullString
	var version sql.NullString
	err := r.db.QueryRow(`
		SELECT name, version, provider, dim, lang, local_path, is_available, created_at
		FROM models WHERE name = ?`, name).Scan(
		&m.Name, &version, &m.Provider, &m.Dim,
		&m.Lang, &localPath, &m.IsAvailable, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.Version = version.String
	m.LocalPath = localPath.String
	return &m, nil
}

func (r *ModelRepo) Upsert(m Model) error {
	_, err := r.db.Exec(`
		INSERT INTO models (name, version, provider, dim, lang, local_path, is_available, created_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET
			version = excluded.version,
			local_path = excluded.local_path,
			is_available = excluded.is_available`,
		m.Name, m.Version, m.Provider, m.Dim, m.Lang, m.LocalPath, m.IsAvailable, time.Now(),
	)
	return err
}
