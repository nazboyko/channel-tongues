package youtube

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBackupWriteReadList(t *testing.T) {
	dir := t.TempDir()
	clock := time.Date(2026, 10, 16, 20, 5, 12, 0, time.UTC)
	b := &Backups{Dir: dir, Now: func() time.Time { return clock }}
	bk := Backup{
		VideoID:       "mockRain001",
		Reason:        "publish",
		JobID:         "job1",
		Snippet:       json.RawMessage(`{"title":"t","defaultLanguage":"en"}`),
		Localizations: map[string]Localization{"de": {Title: "Regen", Description: "d"}},
		Planned:       map[string]Localization{"de": {Title: "Regen"}, "es": {Title: "Lluvia"}},
	}
	id1, err := b.Write(bk)
	if err != nil || id1 != "20261016-200512" {
		t.Fatalf("id %q %v", id1, err)
	}
	id2, _ := b.Write(bk) // same second: a suffix, never an overwrite
	if id2 != "20261016-200512-2" {
		t.Fatalf("second id %q", id2)
	}

	fi, err := os.Stat(filepath.Join(dir, "mockRain001", id1+".json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup file mode %v %v", fi.Mode().Perm(), err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "mockRain001", id1+".json"))
	for _, key := range []string{`"snippet"`, `"localizations"`, `"planned"`, `"jobId"`, `"takenAt"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("backup file lacks %s", key)
		}
	}

	got, err := b.Read("mockRain001", id1)
	if err != nil || got.Localizations["de"].Title != "Regen" || got.Reason != "publish" {
		t.Fatalf("read %+v %v", got, err)
	}
	list, _ := b.List("mockRain001")
	if len(list) != 2 || list[0].ID != id2 || list[1].Languages != 1 {
		t.Fatalf("list %+v", list)
	}
	none, err := b.List("mockPorch02")
	if err != nil || len(none) != 0 {
		t.Fatalf("no backups yet: %v %v", none, err)
	}
}

func TestBackupRefusesBadNames(t *testing.T) {
	b := &Backups{Dir: t.TempDir()}
	if _, err := b.Read("mockRain001", "../../etc/passwd"); err == nil {
		t.Fatal("path traversal must be refused")
	}
	if _, err := b.Read("../x", "20261016-200512"); err == nil {
		t.Fatal("bad video id must be refused")
	}
	if _, err := b.Write(Backup{VideoID: "../../x"}); err == nil {
		t.Fatal("bad video id must be refused on write")
	}
	if _, err := b.Read("mockRain001", "20261016-200512"); err == nil || !strings.Contains(err.Error(), "tongues backups") {
		t.Fatalf("missing backup should point at the listing: %v", err)
	}
}

func TestParseVideoID(t *testing.T) {
	id := "AbC_12-xyz9"
	good := []string{
		id, " " + id + " ",
		"https://studio.youtube.com/video/" + id + "/edit",
		"https://studio.youtube.com/video/" + id + "/translations?c=UC1",
		"https://www.youtube.com/watch?v=" + id + "&t=1",
		"https://youtu.be/" + id,
		"https://www.youtube.com/shorts/" + id,
	}
	for _, s := range good {
		if got, ok := ParseVideoID(s); !ok || got != id {
			t.Errorf("%q: got %q %v", s, got, ok)
		}
	}
	bad := []string{"", "short", "https://example.com/watch?v=" + id, "https://studio.youtube.com/channel/UC1", "test:1"}
	for _, s := range bad {
		if got, ok := ParseVideoID(s); ok {
			t.Errorf("%q: unexpected %q", s, got)
		}
	}
}
