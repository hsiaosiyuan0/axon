package store

import (
	"database/sql"
	"encoding/binary"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Embedding stores a vector for a chunk under a specific model.
type Embedding struct {
	ID           string
	ChunkID      string
	ModelName    string
	ModelVersion string
	Provider     string
	Vector       []float32
	Dim          int
	CreatedAt    time.Time
}

type EmbeddingRepo struct{ db *sql.DB }

func (r *EmbeddingRepo) Upsert(chunkID, modelName, provider string, vector []float32) error {
	blob := float32SliceToBlob(vector)
	id := uuid.NewString()
	_, err := r.db.Exec(`
		INSERT INTO embeddings (id, chunk_id, model_name, provider, vector, dim, created_at)
		VALUES (?,?,?,?,?,?,?)
		ON CONFLICT(chunk_id, model_name) DO UPDATE SET
			vector = excluded.vector,
			created_at = excluded.created_at`,
		id, chunkID, modelName, provider, blob, len(vector), time.Now(),
	)
	return err
}

// maxVectorScanRows is a safety cap on how many embeddings are loaded into
// memory for in-process cosine similarity search. Exceeding this suggests the
// caller should switch to an index-accelerated solution (e.g. sqlite-vec).
const maxVectorScanRows = 100_000

// VectorSearch performs cosine similarity search in pure Go.
// For production: use sqlite-vec extension for index-accelerated search.
func (r *EmbeddingRepo) VectorSearch(query []float32, modelName, collection string, limit int) ([]SearchResult, error) {
	rows, err := r.db.Query(`
		SELECT e.chunk_id, e.vector, c.source_id, c.collection, c.content
		FROM embeddings e
		JOIN chunks c ON c.id = e.chunk_id
		WHERE e.model_name = ?
		  AND (? = '' OR c.collection = ?)
		LIMIT ?`,
		modelName, collection, collection, maxVectorScanRows)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type candidate struct {
		SearchResult
		vector []float32
	}

	var candidates []candidate
	for rows.Next() {
		var c candidate
		var blob []byte
		if err := rows.Scan(&c.ChunkID, &blob, &c.SourceID, &c.Collection, &c.Content); err != nil {
			return nil, err
		}
		c.vector = blobToFloat32Slice(blob)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Score all candidates
	type scored struct {
		r     SearchResult
		score float64
	}
	var scored_list []scored
	for _, c := range candidates {
		sim := cosineSimilarity(query, c.vector)
		scored_list = append(scored_list, scored{r: c.SearchResult, score: sim})
	}

	// Sort descending by score
	sort.Slice(scored_list, func(i, j int) bool {
		return scored_list[i].score > scored_list[j].score
	})

	var results []SearchResult
	for i, s := range scored_list {
		if i >= limit {
			break
		}
		s.r.Score = s.score
		results = append(results, s.r)
	}
	return results, nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func float32SliceToBlob(v []float32) []byte {
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func blobToFloat32Slice(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// GetByChunkID returns the first embedding for a given chunk, or nil if none exists.
func (r *EmbeddingRepo) GetByChunkID(chunkID string) (*Embedding, error) {
	var e Embedding
	var blob []byte
	err := r.db.QueryRow(`
		SELECT id, chunk_id, model_name, provider, vector, dim, created_at
		FROM embeddings WHERE chunk_id = ? LIMIT 1`, chunkID).Scan(
		&e.ID, &e.ChunkID, &e.ModelName, &e.Provider, &blob, &e.Dim, &e.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	e.Vector = blobToFloat32Slice(blob)
	return &e, nil
}
