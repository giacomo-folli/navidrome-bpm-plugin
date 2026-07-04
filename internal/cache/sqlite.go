package cache

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type Track struct {
	Path       string
	Modified   time.Time
	BPM        float64
	Confidence float64
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS tracks (
	path TEXT PRIMARY KEY,
	modified INTEGER NOT NULL,
	bpm REAL NOT NULL,
	confidence REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS tracks_modified_idx ON tracks(modified);
`)
	return err
}

func (s *Store) GetFresh(path string, modified time.Time) (Track, bool, error) {
	var t Track
	var unix int64
	err := s.db.QueryRow(`SELECT path, modified, bpm, confidence FROM tracks WHERE path = ?`, path).
		Scan(&t.Path, &unix, &t.BPM, &t.Confidence)
	if errors.Is(err, sql.ErrNoRows) {
		return Track{}, false, nil
	}
	if err != nil {
		return Track{}, false, err
	}
	t.Modified = time.Unix(unix, 0)
	if t.Modified.Equal(modified.Truncate(time.Second)) {
		return t, true, nil
	}
	return Track{}, false, nil
}

func (s *Store) Upsert(t Track) error {
	_, err := s.db.Exec(`
INSERT INTO tracks(path, modified, bpm, confidence)
VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	modified = excluded.modified,
	bpm = excluded.bpm,
	confidence = excluded.confidence
`, t.Path, t.Modified.Unix(), t.BPM, t.Confidence)
	return err
}
