package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	"github.com/hsiaosiyuan0/axon/internal/tokenize"
	"github.com/google/uuid"
)

// contentHash returns a SHA-256 hex digest of the given string.
func contentHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// Chunk is a piece of a source document.
type Chunk struct {
	ID          string
	SourceID    string
	Collection  string
	Content     string
	ContentHash string
	Position    int
	CharStart   int
	CharEnd     int
	Section     string
	CreatedAt   time.Time
}

type ChunkRepo struct{ db *sql.DB }

type CreateChunkParams struct {
	SourceID   string
	Collection string
	Content    string
	Position   int
	CharStart  int
	CharEnd    int
	Section    string
}

func (r *ChunkRepo) Create(p CreateChunkParams) (*Chunk, error) {
	c := &Chunk{
		ID:          uuid.NewString(),
		SourceID:    p.SourceID,
		Collection:  p.Collection,
		Content:     p.Content,
		ContentHash: contentHash(p.Content),
		Position:    p.Position,
		CharStart:   p.CharStart,
		CharEnd:     p.CharEnd,
		Section:     p.Section,
		CreatedAt:   time.Now(),
	}
	_, err := r.db.Exec(`
		INSERT INTO chunks (id, source_id, collection, content, content_hash, position, char_start, char_end, section, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.ID, c.SourceID, c.Collection, c.Content, c.ContentHash, c.Position, c.CharStart, c.CharEnd, c.Section, c.CreatedAt,
	)
	return c, err
}

func (r *ChunkRepo) BatchCreate(chunks []CreateChunkParams) ([]Chunk, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO chunks (id, source_id, collection, content, content_hash, position, char_start, char_end, section, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer stmt.Close()

	now := time.Now()
	var result []Chunk
	for _, p := range chunks {
		c := Chunk{
			ID:          uuid.NewString(),
			SourceID:    p.SourceID,
			Collection:  p.Collection,
			Content:     p.Content,
			ContentHash: contentHash(p.Content),
			Position:    p.Position,
			CharStart:   p.CharStart,
			CharEnd:     p.CharEnd,
			Section:     p.Section,
			CreatedAt:   now,
		}
		if _, err := stmt.Exec(c.ID, c.SourceID, c.Collection, c.Content, c.ContentHash,
			c.Position, c.CharStart, c.CharEnd, c.Section, c.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, tx.Commit()
}

// BM25Search performs full-text search using SQLite FTS5 (BM25 built-in).
// CJK queries are automatically tokenized for better match quality.
func (r *ChunkRepo) BM25Search(query, collection string, limit int) ([]SearchResult, error) {
	// CJK-aware query normalisation
	ftsQuery := tokenize.TokenizeQuery(tokenize.NormalizeQuery(query))

	var rows *sql.Rows
	var err error

	if collection != "" {
		rows, err = r.db.Query(`
			SELECT c.id, c.source_id, c.collection, c.content,
			       -bm25(chunks_fts) AS score
			FROM chunks_fts
			JOIN chunks c ON c.rowid = chunks_fts.rowid
			WHERE chunks_fts MATCH ? AND c.collection = ?
			ORDER BY score DESC LIMIT ?`,
			ftsQuery, collection, limit)
	} else {
		rows, err = r.db.Query(`
			SELECT c.id, c.source_id, c.collection, c.content,
			       -bm25(chunks_fts) AS score
			FROM chunks_fts
			JOIN chunks c ON c.rowid = chunks_fts.rowid
			WHERE chunks_fts MATCH ?
			ORDER BY score DESC LIMIT ?`,
			ftsQuery, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.ChunkID, &r.SourceID, &r.Collection, &r.Content, &r.Score); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, rows.Err()
}

// SearchResult is a ranked search result.
type SearchResult struct {
	ChunkID    string
	SourceID   string
	Collection string
	Content    string
	Source     string // filled by hybrid searcher
	Score      float64
	Rank       int
}

func (r *ChunkRepo) GetByID(id string) (*Chunk, error) {
	var c Chunk
	err := r.db.QueryRow(
		`SELECT id, source_id, collection, content, position, char_start, char_end, section, created_at
		 FROM chunks WHERE id = ?`, id).Scan(
		&c.ID, &c.SourceID, &c.Collection, &c.Content,
		&c.Position, &c.CharStart, &c.CharEnd, &c.Section, &c.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *ChunkRepo) GetBySourceID(sourceID string) ([]Chunk, error) {
	rows, err := r.db.Query(
		`SELECT id, source_id, collection, content, position, char_start, char_end, section, created_at
		 FROM chunks WHERE source_id = ? ORDER BY position`, sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.SourceID, &c.Collection, &c.Content,
			&c.Position, &c.CharStart, &c.CharEnd, &c.Section, &c.CreatedAt); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func (r *ChunkRepo) GetByCollectionID(collectionID string) ([]Chunk, error) {
	rows, err := r.db.Query(
		`SELECT id, source_id, collection, content, position, char_start, char_end, section, created_at
		 FROM chunks WHERE collection = ? ORDER BY source_id, position`, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var chunks []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.SourceID, &c.Collection, &c.Content,
			&c.Position, &c.CharStart, &c.CharEnd, &c.Section, &c.CreatedAt); err != nil {
			return nil, err
		}
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

// Count returns the total number of chunks in the database.
func (r *ChunkRepo) Count() (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM chunks`).Scan(&n)
	return n, err
}

// CountBySource returns a map of source_id → chunk count for all sources.
func (r *ChunkRepo) CountBySource() (map[string]int, error) {
	rows, err := r.db.Query(`SELECT source_id, COUNT(*) FROM chunks GROUP BY source_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int)
	for rows.Next() {
		var sourceID string
		var count int
		if err := rows.Scan(&sourceID, &count); err != nil {
			return nil, err
		}
		result[sourceID] = count
	}
	return result, rows.Err()
}
