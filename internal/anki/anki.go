package anki

// Package anki implements Anki .apkg export for Axon chunks.
//
// .apkg format:
//   anki2.apkg
//   ├── collection.anki2   (SQLite database)
//   └── media              (JSON map of media files, usually "{}")
//
// We generate one card per chunk: Front = section header or first sentence,
// Back = full chunk content. Optionally include source metadata.

import (
	"archive/zip"
	"crypto/sha1"
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Card represents a single Anki flashcard.
type Card struct {
	Front      string
	Back       string
	Tags       []string
	DeckName   string
	SourceTitle string
}

// ExportAPKG writes cards to an Anki .apkg file at destPath.
func ExportAPKG(cards []Card, destPath string) error {
	// Create temp SQLite DB for collection.anki2
	tmpDB, err := os.CreateTemp("", "axon-anki-*.anki2")
	if err != nil {
		return fmt.Errorf("create temp db: %w", err)
	}
	tmpDBPath := tmpDB.Name()
	tmpDB.Close()
	defer os.Remove(tmpDBPath)

	if err := buildAnkiDB(tmpDBPath, cards); err != nil {
		return fmt.Errorf("build anki db: %w", err)
	}

	// Package into .apkg (zip)
	f, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	defer zw.Close()

	// Add collection.anki2
	dbBytes, err := os.ReadFile(tmpDBPath)
	if err != nil {
		return fmt.Errorf("read temp db: %w", err)
	}
	w, err := zw.Create("collection.anki2")
	if err != nil {
		return err
	}
	if _, err := w.Write(dbBytes); err != nil {
		return err
	}

	// Add media (empty JSON object)
	mw, err := zw.Create("media")
	if err != nil {
		return err
	}
	if _, err := mw.Write([]byte("{}")); err != nil {
		return err
	}

	return nil
}

// buildAnkiDB creates a minimal Anki2 SQLite database with the given cards.
func buildAnkiDB(path string, cards []Card) error {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return err
	}
	defer db.Close()

	// Create Anki2 schema (simplified but valid subset)
	schema := `
CREATE TABLE IF NOT EXISTS col (
    id        INTEGER PRIMARY KEY,
    crt       INTEGER NOT NULL,
    mod       INTEGER NOT NULL,
    scm       INTEGER NOT NULL,
    ver       INTEGER NOT NULL,
    dty       INTEGER NOT NULL,
    usn       INTEGER NOT NULL,
    ls        INTEGER NOT NULL,
    conf      TEXT NOT NULL,
    models    TEXT NOT NULL,
    decks     TEXT NOT NULL,
    dconf     TEXT NOT NULL,
    tags      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS notes (
    id        INTEGER PRIMARY KEY,
    guid      TEXT NOT NULL,
    mid       INTEGER NOT NULL,
    mod       INTEGER NOT NULL,
    usn       INTEGER NOT NULL,
    tags      TEXT NOT NULL,
    flds      TEXT NOT NULL,
    sfld      TEXT NOT NULL,
    csum      INTEGER NOT NULL,
    flags     INTEGER NOT NULL,
    data      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS cards (
    id        INTEGER PRIMARY KEY,
    nid       INTEGER NOT NULL,
    did       INTEGER NOT NULL,
    ord       INTEGER NOT NULL,
    mod       INTEGER NOT NULL,
    usn       INTEGER NOT NULL,
    type      INTEGER NOT NULL,
    queue     INTEGER NOT NULL,
    due       INTEGER NOT NULL,
    ivl       INTEGER NOT NULL,
    factor    INTEGER NOT NULL,
    reps      INTEGER NOT NULL,
    lapses    INTEGER NOT NULL,
    left      INTEGER NOT NULL,
    odue      INTEGER NOT NULL,
    odid      INTEGER NOT NULL,
    flags     INTEGER NOT NULL,
    data      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS revlog (
    id        INTEGER PRIMARY KEY,
    cid       INTEGER NOT NULL,
    usn       INTEGER NOT NULL,
    ease      INTEGER NOT NULL,
    ivl       INTEGER NOT NULL,
    lastIvl   INTEGER NOT NULL,
    factor    INTEGER NOT NULL,
    time      INTEGER NOT NULL,
    type      INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS graves (
    usn       INTEGER NOT NULL,
    oid       INTEGER NOT NULL,
    type      INTEGER NOT NULL
);
`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	now := time.Now().Unix()
	deckID := int64(1000000000 + rand.Int63n(1000000000))
	modelID := int64(1000000000 + rand.Int63n(1000000000))

	// Build deck and model JSON
	deckJSON := fmt.Sprintf(`{"%d": {"id": %d, "name": "Axon", "conf": 1, "extendRev": 50, "usn": 0, "collapsed": false, "browserCollapsed": false, "extendNew": 10, "dyn": 0, "newToday": [0,0], "revToday": [0,0], "lrnToday": [0,0], "timeToday": [0,0], "mod": %d}}`, deckID, deckID, now)

	modelJSON := fmt.Sprintf(`{"%d": {
		"id": %d,
		"name": "Axon Basic",
		"type": 0,
		"mod": %d,
		"usn": 0,
		"sortf": 0,
		"did": %d,
		"tmpls": [{"name": "Card 1", "ord": 0, "qfmt": "{{Front}}", "afmt": "{{FrontSide}}<hr id=answer>{{Back}}", "bqfmt": "", "bafmt": "", "did": null, "bfont": "", "bsize": 0}],
		"flds": [{"name": "Front", "ord": 0, "sticky": false, "rtl": false, "font": "Arial", "size": 20}, {"name": "Back", "ord": 1, "sticky": false, "rtl": false, "font": "Arial", "size": 20}],
		"css": ".card { font-family: arial; font-size: 20px; color: black; background-color: white; }",
		"latexPre": "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
		"latexPost": "\\end{document}",
		"vers": [],
		"tags": [],
		"req": [[0, "any", [0]]]
	}}`, modelID, modelID, now, deckID)

	_, err = db.Exec(`INSERT INTO col VALUES (1, ?, ?, ?, 11, 0, 0, 0, '{}', ?, ?, '{"1":{"id":1,"name":"Default","new":{"bury":false,"delays":[1,10],"initialFactor":2500,"ints":[1,4,7],"order":1,"perDay":20},"lapse":{"delays":[10],"leechAction":1,"leechFails":8,"minInt":1,"mult":0},"rev":{"bury":false,"ease4":1.3,"fuzz":0.05,"ivlFct":1,"maxIvl":36500,"perDay":200}}}', '{}')`,
		now, now, now*1000, modelJSON, deckJSON)
	if err != nil {
		return fmt.Errorf("insert col: %w", err)
	}

	// Insert notes and cards
	for i, card := range cards {
		noteID := now*1000 + int64(i)
		cardID := noteID + 1
		guid := generateGUID(card.Front)
		tags := strings.Join(card.Tags, " ")
		if tags != "" {
			tags = " " + tags + " "
		}

		// Fields are separated by 0x1f (ASCII unit separator)
		flds := card.Front + "\x1f" + card.Back

		// sfld = sort field (front)
		sfld := card.Front

		// csum = sha1 first 8 chars of sfld
		csum := fieldChecksum(sfld)

		_, err = db.Exec(`INSERT INTO notes VALUES (?, ?, ?, ?, -1, ?, ?, ?, ?, 0, '')`,
			noteID, guid, modelID, now, tags, flds, sfld, csum)
		if err != nil {
			return fmt.Errorf("insert note %d: %w", i, err)
		}

		_, err = db.Exec(`INSERT INTO cards VALUES (?, ?, ?, 0, ?, -1, 0, 0, ?, 0, 2500, 0, 0, 0, 0, 0, 0, '')`,
			cardID, noteID, deckID, now, noteID)
		if err != nil {
			return fmt.Errorf("insert card %d: %w", i, err)
		}
	}

	return nil
}

// generateGUID creates a stable 10-char GUID from a string.
func generateGUID(s string) string {
	h := sha1.Sum([]byte(s))
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!#"
	result := make([]byte, 10)
	for i := range result {
		result[i] = chars[h[i]%64]
	}
	return string(result)
}

// fieldChecksum returns the first 32 bits of SHA1(field) as an integer.
func fieldChecksum(s string) int64 {
	h := sha1.Sum([]byte(s))
	return int64(h[0])<<24 | int64(h[1])<<16 | int64(h[2])<<8 | int64(h[3])
}

// ChunkToCard converts a chunk's content into an Anki card.
// Front: section header (if any) or first sentence; Back: full content.
func ChunkToCard(section, content, sourceTitle, collectionName string) Card {
	front := section
	if front == "" {
		// Use first sentence or first 100 chars
		front = firstSentence(content, 120)
	}

	back := content
	if sourceTitle != "" {
		back += fmt.Sprintf("\n\n---\n*Source: %s*", sourceTitle)
	}

	// Build tags from collection name
	tag := strings.ToLower(strings.ReplaceAll(collectionName, " ", "_"))

	return Card{
		Front:       front,
		Back:        back,
		Tags:        []string{"axon", tag},
		DeckName:    "Axon",
		SourceTitle: sourceTitle,
	}
}

func firstSentence(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	// Find first sentence end
	for i, ch := range s {
		if (ch == '.' || ch == '!' || ch == '?') && i > 10 {
			result := strings.TrimSpace(s[:i+1])
			if len(result) <= maxLen {
				return result
			}
			break
		}
	}
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
