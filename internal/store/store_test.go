package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "tongues.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReopenKeepsDataAndSkipsMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tongues.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting("a", "1"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	v, ok, err := s.Setting("a")
	if v != "1" || !ok || err != nil {
		t.Fatalf("got %q %v %v", v, ok, err)
	}
}

func TestSettings(t *testing.T) {
	s := open(t)
	if _, ok, _ := s.Setting("missing"); ok {
		t.Fatal("missing key reported present")
	}
	s.SetSetting("k", "one")
	s.SetSetting("k", "two")
	if v, _, _ := s.Setting("k"); v != "two" {
		t.Fatalf("got %q", v)
	}
}

func TestKeepWords(t *testing.T) {
	s := open(t)
	if err := s.AddKeepWord("  "); err == nil {
		t.Fatal("empty word must be refused")
	}
	s.AddKeepWord("The Slow Night Hours")
	s.AddKeepWord("the slow night hours") // same word, other case
	s.AddKeepWord("#sleepsounds")
	words, _ := s.KeepWords()
	if len(words) != 2 || words[0] != "The Slow Night Hours" {
		t.Fatalf("got %v", words)
	}
	removed, _ := s.RemoveKeepWord("THE SLOW NIGHT HOURS")
	if !removed {
		t.Fatal("remove should match case-insensitively")
	}
	removed, _ = s.RemoveKeepWord("nope")
	if removed {
		t.Fatal("nothing to remove")
	}
}

func TestUnitsPerDay(t *testing.T) {
	s := open(t)
	s.AddUnits("2026-10-16", 52)
	s.AddUnits("2026-10-16", 1)
	s.AddUnits("2026-10-17", 50)
	if n, _ := s.Units("2026-10-16"); n != 53 {
		t.Fatalf("got %d", n)
	}
	if n, _ := s.Units("2026-10-18"); n != 0 {
		t.Fatalf("empty day got %d", n)
	}
}

func TestCacheAge(t *testing.T) {
	s := open(t)
	s.CachePut("i18n", "[1,2]")
	if v, ok, _ := s.CacheGet("i18n", time.Hour); !ok || v != "[1,2]" {
		t.Fatalf("fresh cache: %q %v", v, ok)
	}
	if _, ok, _ := s.CacheGet("i18n", -time.Second); ok {
		t.Fatal("an expired entry must not be returned")
	}
}

func TestHistory(t *testing.T) {
	s := open(t)
	s.RecordHistory("vid", HistoryEntry{Lang: "es", Title: "t1", Description: "d1", BackupID: "b1"})
	s.RecordHistory("vid", HistoryEntry{Lang: "es", Title: "t2", Description: "d2", BackupID: "b2"})
	s.RecordHistory("vid", HistoryEntry{Lang: "de", Title: "t", Description: "d"})
	h, _ := s.History("vid")
	if len(h) != 2 || h["es"].Title != "t2" || h["es"].BackupID != "b2" {
		t.Fatalf("got %+v", h)
	}
	s.ForgetHistory("vid", "es")
	h, _ = s.History("vid")
	if _, ok := h["es"]; ok || len(h) != 1 {
		t.Fatalf("after forget: %+v", h)
	}
}

func TestOpenPathWithPercentAndSpace(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "odd 100% name", "tongues.db"))
	if err != nil {
		t.Fatalf("a data folder with %% or spaces must open: %v", err)
	}
	s.Close()
}

func TestJobsRoundTrip(t *testing.T) {
	s := open(t)
	s.SaveJob(JobRow{ID: "j1", VideoID: "v", State: "running", SourceTitle: "t", Languages: "es,de"})
	s.SaveJob(JobRow{ID: "j1", State: "done"})
	s.SaveTranslation(TranslationRow{JobID: "j1", Lang: "es", Status: "ready", Title: "x", Seconds: 1.5})
	s.SaveTranslation(TranslationRow{JobID: "j1", Lang: "es", Status: "ready", Title: "y", Edited: true, BackTitle: "back", Added: "quiet,calm"})
	j, ts, ok, err := s.LoadJob("j1")
	if !ok || err != nil || j.State != "done" || j.Languages != "es,de" || len(ts) != 1 || ts[0].Title != "y" || !ts[0].Edited || ts[0].BackTitle != "back" || ts[0].Added != "quiet,calm" {
		t.Fatalf("got %+v %+v %v %v", j, ts, ok, err)
	}
	if _, _, ok, _ := s.LoadJob("nope"); ok {
		t.Fatal("unknown job")
	}
}

func TestUpgradeFromTheFirstSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`CREATE TABLE schema_version (version INTEGER NOT NULL); INSERT INTO schema_version VALUES (1);`)
	db.Exec(migrations[0])
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer s.Close()
	if err := s.SaveTranslation(TranslationRow{JobID: "j", Lang: "es", Status: "ready", BackTitle: "x"}); err != nil {
		t.Fatalf("new columns missing after upgrade: %v", err)
	}
}
