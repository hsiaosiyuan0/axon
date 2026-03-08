package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mattn/go-sqlite3"
	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQLite connection and exposes typed repositories.
type DB struct {
	sql *sql.DB
}

// Open opens (or creates) the Axon SQLite database.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &DB{sql: db}, nil
}

// Close closes the database connection.
func (db *DB) Close() error { return db.sql.Close() }

// Ping verifies the database connection is still alive.
func (db *DB) Ping() error { return db.sql.Ping() }

// Collections returns the collections repository.
func (db *DB) Collections() *CollectionRepo { return &CollectionRepo{db: db.sql} }

// Sources returns the sources repository.
func (db *DB) Sources() *SourceRepo { return &SourceRepo{db: db.sql} }

// Chunks returns the chunks repository.
func (db *DB) Chunks() *ChunkRepo { return &ChunkRepo{db: db.sql} }

// Embeddings returns the embeddings repository.
func (db *DB) Embeddings() *EmbeddingRepo { return &EmbeddingRepo{db: db.sql} }

// Relations returns the relations repository.
func (db *DB) Relations() *RelationRepo { return &RelationRepo{db: db.sql} }

// Models returns the models repository.
func (db *DB) Models() *ModelRepo { return &ModelRepo{db: db.sql} }

// isSQLiteDuplicateColumn returns true when err is a SQLite "duplicate column"
// error (SQLITE_ERROR / code 1 with that message), which we encounter on
// idempotent ALTER TABLE ADD COLUMN migrations.
func isSQLiteDuplicateColumn(err error) bool {
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code == sqlite3.ErrError &&
			strings.Contains(sqliteErr.Error(), "duplicate column")
	}
	// Fallback for drivers that wrap the message differently.
	return strings.Contains(err.Error(), "duplicate column")
}

// Migrate runs all DDL migrations to bring the schema up to date.
func (db *DB) Migrate() error {
	for _, stmt := range migrations {
		if _, err := db.sql.Exec(stmt); err != nil {
			if isSQLiteDuplicateColumn(err) {
				continue
			}
			return fmt.Errorf("migration failed: %w\nSQL: %s", err, stmt)
		}
	}
	return nil
}

// migrations contains all schema creation statements (idempotent).
var migrations = []string{
	// ── Collections ────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS collections (
		id              TEXT PRIMARY KEY,
		name            TEXT NOT NULL,
		type            TEXT NOT NULL DEFAULT 'custom',
		description     TEXT,
		model_name      TEXT,
		chunk_strategy  TEXT NOT NULL DEFAULT 'markdown',
		meta            TEXT,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// ── Sources ────────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS sources (
		id              TEXT PRIMARY KEY,
		collection      TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
		source_type     TEXT NOT NULL,
		origin          TEXT NOT NULL,
		origin_hash     TEXT,
		raw_content     BLOB,
		raw_encoding    TEXT NOT NULL DEFAULT 'utf-8',
		raw_mime        TEXT,
		plain_text      TEXT,
		title           TEXT,
		lang            TEXT,
		meta            TEXT,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		fetched_at      DATETIME
	)`,

	`CREATE INDEX IF NOT EXISTS idx_sources_collection ON sources(collection)`,
	`CREATE INDEX IF NOT EXISTS idx_sources_origin     ON sources(origin)`,

	// ── Chunks ─────────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS chunks (
		id              TEXT PRIMARY KEY,
		source_id       TEXT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
		collection      TEXT NOT NULL,
		content         TEXT NOT NULL,
		content_hash    TEXT,
		position        INTEGER,
		char_start      INTEGER,
		char_end        INTEGER,
		section         TEXT,
		meta            TEXT,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_chunks_source ON chunks(source_id)`,

	// FTS5 full-text index for BM25
	`CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
		content,
		content='chunks',
		content_rowid='rowid'
	)`,

	// Keep FTS in sync via triggers
	`CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
		INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
	END`,
	`CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
	END`,
	`CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
		INSERT INTO chunks_fts(chunks_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
		INSERT INTO chunks_fts(rowid, content) VALUES (new.rowid, new.content);
	END`,

	// ── Embeddings ─────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS embeddings (
		id              TEXT PRIMARY KEY,
		chunk_id        TEXT NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
		model_name      TEXT NOT NULL,
		model_version   TEXT,
		provider        TEXT,
		vector          BLOB NOT NULL,
		dim             INTEGER NOT NULL,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(chunk_id, model_name)
	)`,

	`CREATE INDEX IF NOT EXISTS idx_embeddings_chunk ON embeddings(chunk_id)`,
	`CREATE INDEX IF NOT EXISTS idx_embeddings_model ON embeddings(model_name)`,

	// ── Relations ──────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS relations (
		id              TEXT PRIMARY KEY,
		from_type       TEXT NOT NULL,
		from_id         TEXT NOT NULL,
		from_collection TEXT NOT NULL,
		to_type         TEXT NOT NULL,
		to_id           TEXT NOT NULL,
		to_collection   TEXT NOT NULL,
		rel_type        TEXT NOT NULL,
		weight          REAL NOT NULL DEFAULT 1.0,
		bidirectional   INTEGER NOT NULL DEFAULT 0,
		established_by  TEXT,
		evidence        TEXT,
		meta            TEXT,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	`CREATE INDEX IF NOT EXISTS idx_relations_from ON relations(from_type, from_id)`,
	`CREATE INDEX IF NOT EXISTS idx_relations_to   ON relations(to_type, to_id)`,

	// Migration: add to_origin for pending wikilinks (target not yet ingested)
	// SQLite ignores "duplicate column" errors via IF NOT EXISTS workaround:
	// We use a no-op approach — just try and ignore the error in Migrate().
	`ALTER TABLE relations ADD COLUMN to_origin TEXT`,
	`CREATE INDEX IF NOT EXISTS idx_relations_to_origin ON relations(to_origin)`,

	// ── Models ─────────────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS models (
		name            TEXT PRIMARY KEY,
		version         TEXT,
		provider        TEXT NOT NULL,
		dim             INTEGER NOT NULL,
		lang            TEXT,
		local_path      TEXT,
		api_config      TEXT,
		is_available    INTEGER NOT NULL DEFAULT 0,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`,

	// ── Re-embed Jobs ──────────────────────────────────────────────────
	`CREATE TABLE IF NOT EXISTS re_embed_jobs (
		id              TEXT PRIMARY KEY,
		collection      TEXT,
		old_model       TEXT,
		new_model       TEXT NOT NULL,
		status          TEXT NOT NULL DEFAULT 'pending',
		progress        INTEGER NOT NULL DEFAULT 0,
		total           INTEGER,
		error           TEXT,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		started_at      DATETIME,
		finished_at     DATETIME
	)`,

	// Seed built-in API models
	`INSERT OR IGNORE INTO models (name, provider, dim, lang, is_available) VALUES
		('api:text-embedding-3-small', 'openai',  1536, 'multilingual', 1),
		('api:text-embedding-3-large', 'openai',  3072, 'multilingual', 1),
		('api:text-embedding-ada-002', 'openai',  1536, 'multilingual', 1)
	`,
}
