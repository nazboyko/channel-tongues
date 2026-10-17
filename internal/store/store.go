// Package store keeps settings, keep words, jobs, translation history, the
// daily publish budget and small caches in a SQLite file in the data
// directory.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// migrations run in order; each entry is applied once. They are written to be
// safe to run twice, in case two processes open a new database together.
var migrations = []string{
	`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
	 CREATE TABLE IF NOT EXISTS keep_words (word TEXT PRIMARY KEY COLLATE NOCASE, added_at TEXT NOT NULL);
	 CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		video_id TEXT NOT NULL,
		created_at TEXT NOT NULL,
		state TEXT NOT NULL,
		source_lang TEXT NOT NULL DEFAULT '',
		source_title TEXT NOT NULL DEFAULT '',
		source_description TEXT NOT NULL DEFAULT '',
		languages TEXT NOT NULL DEFAULT ''
	 );
	 CREATE TABLE IF NOT EXISTS translations (
		job_id TEXT NOT NULL,
		lang TEXT NOT NULL,
		status TEXT NOT NULL,
		title TEXT NOT NULL DEFAULT '',
		description TEXT NOT NULL DEFAULT '',
		note TEXT NOT NULL DEFAULT '',
		seconds REAL NOT NULL DEFAULT 0,
		retries INTEGER NOT NULL DEFAULT 0,
		edited INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL,
		PRIMARY KEY (job_id, lang)
	 );
	 CREATE TABLE IF NOT EXISTS history (
		video_id TEXT NOT NULL,
		lang TEXT NOT NULL,
		title TEXT NOT NULL,
		description TEXT NOT NULL,
		published_at TEXT NOT NULL,
		backup_id TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (video_id, lang)
	 );
	 CREATE TABLE IF NOT EXISTS quota (day TEXT PRIMARY KEY, units INTEGER NOT NULL);
	 CREATE TABLE IF NOT EXISTS cache (key TEXT PRIMARY KEY, value TEXT NOT NULL, fetched_at TEXT NOT NULL);`,
}

// Open opens (and creates) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT INTO schema_version (version) SELECT 0 WHERE NOT EXISTS (SELECT 1 FROM schema_version)`); err != nil {
		return err
	}
	var v int
	if err := s.db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Setting returns a stored value; ok is false when the key is absent.
func (s *Store) Setting(key string) (value string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}

// SetSetting stores a value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// KeepWords lists the words that stay exactly as written, oldest first.
func (s *Store) KeepWords() ([]string, error) {
	rows, err := s.db.Query(`SELECT word FROM keep_words ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var w string
		if err := rows.Scan(&w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// AddKeepWord adds a word; adding one that exists (in any case) is a no-op.
func (s *Store) AddKeepWord(word string) error {
	word = strings.TrimSpace(word)
	if word == "" {
		return errors.New("the word is empty")
	}
	_, err := s.db.Exec(`INSERT INTO keep_words (word, added_at) VALUES (?, ?) ON CONFLICT DO NOTHING`, word, now())
	return err
}

// RemoveKeepWord removes a word (case-insensitive). It reports whether one was removed.
func (s *Store) RemoveKeepWord(word string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM keep_words WHERE word = ?`, strings.TrimSpace(word))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// AddUnits records API cost on a day (the caller decides which day).
func (s *Store) AddUnits(day string, units int) error {
	_, err := s.db.Exec(`INSERT INTO quota (day, units) VALUES (?, ?)
		ON CONFLICT(day) DO UPDATE SET units = units + excluded.units`, day, units)
	return err
}

// Units returns the API cost recorded on a day.
func (s *Store) Units(day string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT units FROM quota WHERE day = ?`, day).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// CacheGet returns a cached value younger than maxAge.
func (s *Store) CacheGet(key string, maxAge time.Duration) (string, bool, error) {
	var value, fetched string
	err := s.db.QueryRow(`SELECT value, fetched_at FROM cache WHERE key = ?`, key).Scan(&value, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	t, err := time.Parse(time.RFC3339, fetched)
	if err != nil || time.Since(t) > maxAge {
		return "", false, nil
	}
	return value, true, nil
}

// CachePut stores a cached value with the current time.
func (s *Store) CachePut(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO cache (key, value, fetched_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, fetched_at = excluded.fetched_at`, key, value, now())
	return err
}

// HistoryEntry is a translation Channel Tongues wrote to a video.
type HistoryEntry struct {
	Lang        string
	Title       string
	Description string
	PublishedAt string
	BackupID    string
}

// RecordHistory remembers what was written for one language of a video.
func (s *Store) RecordHistory(videoID string, e HistoryEntry) error {
	if e.PublishedAt == "" {
		e.PublishedAt = now()
	}
	_, err := s.db.Exec(`INSERT INTO history (video_id, lang, title, description, published_at, backup_id)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(video_id, lang) DO UPDATE SET title = excluded.title, description = excluded.description,
			published_at = excluded.published_at, backup_id = excluded.backup_id`,
		videoID, e.Lang, e.Title, e.Description, e.PublishedAt, e.BackupID)
	return err
}

// ForgetHistory removes the record for one language (after a restore).
func (s *Store) ForgetHistory(videoID, lang string) error {
	_, err := s.db.Exec(`DELETE FROM history WHERE video_id = ? AND lang = ?`, videoID, lang)
	return err
}

// History returns what Channel Tongues wrote to a video, by language.
func (s *Store) History(videoID string) (map[string]HistoryEntry, error) {
	rows, err := s.db.Query(`SELECT lang, title, description, published_at, backup_id FROM history WHERE video_id = ?`, videoID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]HistoryEntry{}
	for rows.Next() {
		var e HistoryEntry
		if err := rows.Scan(&e.Lang, &e.Title, &e.Description, &e.PublishedAt, &e.BackupID); err != nil {
			return nil, err
		}
		out[e.Lang] = e
	}
	return out, rows.Err()
}
