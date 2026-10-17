package jobs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nazboyko/channel-tongues/internal/store"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

func newRunner(t *testing.T, delay time.Duration) (*Runner, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &Runner{
		Store: st,
		Engine: func(context.Context) *translate.Engine {
			return &translate.Engine{Model: translate.NewFakeModel(delay)}
		},
	}
	return r, st
}

var rain = translate.Source{
	Title:       "Rain on a Cabin Window at Night · 3 Hours of Sleep Sounds",
	Description: "Three hours of soft rain on a wooden cabin window, recorded on a quiet autumn night. No ads in the middle of the video.\n\n0:00 Rain begins\n1:30:00 Distant thunder\n2:45:00 The rain slows down\n\nMore nights at https://www.youtube.com/@theslownighthours\n#sleepsounds #rainsounds",
	Language:    "en",
}

func waitDone(t *testing.T, r *Runner, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s, err := r.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if s.State != Running {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Snapshot{}
}

func TestJobRunsAndStreams(t *testing.T) {
	r, _ := newRunner(t, 5*time.Millisecond)
	id := r.Start(rain, []string{"es", "ja", "de"}, Meta{VideoID: "mockRain001"})
	snap, events, stop, err := r.Watch(id)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if len(snap.Languages) != 3 {
		t.Fatalf("snapshot %+v", snap)
	}
	sawDone := false
	for ev := range events {
		if ev.Type == "done" {
			sawDone = true
			break
		}
	}
	if !sawDone {
		t.Fatal("no done event")
	}
	s := waitDone(t, r, id)
	if s.State != Done || s.Ready != 2 || s.ToCheck != 1 || s.Finished != 3 {
		t.Fatalf("got %+v", s)
	}
	for _, l := range s.Languages {
		if l.Code == "ja" && !strings.Contains(l.Note, "YouTube allows 100") {
			t.Fatalf("the long Japanese sample is to check: %+v", l)
		}
		if l.Code == "es" && !strings.Contains(l.Description, "https://www.youtube.com/@theslownighthours") {
			t.Fatal("restored text in the snapshot")
		}
	}
}

func TestJobCancel(t *testing.T) {
	r, _ := newRunner(t, 300*time.Millisecond)
	id := r.Start(rain, []string{"es", "de", "fr", "it"}, Meta{})
	time.Sleep(50 * time.Millisecond)
	if err := r.Cancel(id); err != nil {
		t.Fatal(err)
	}
	s := waitDone(t, r, id)
	if s.State != Cancelled {
		t.Fatalf("got %s", s.State)
	}
}

func TestEditAndReload(t *testing.T) {
	r, st := newRunner(t, 0)
	id := r.Start(rain, []string{"es"}, Meta{VideoID: "mockRain001"})
	waitDone(t, r, id)
	if _, err := r.Edit(id, "es", strings.Repeat("x", 101), ""); err == nil {
		t.Fatal("a title over 100 characters is refused")
	}
	if _, err := r.Edit(id, "es", "Lluvia <3", ""); err == nil {
		t.Fatal("< and > are refused")
	}
	if _, err := r.Edit(id, "fr", "x", ""); err == nil {
		t.Fatal("unknown language")
	}
	l, err := r.Edit(id, "es", "Lluvia mía", "desc")
	if err != nil || !l.Edited {
		t.Fatalf("edit %+v %v", l, err)
	}
	// A new runner (the helper restarted) reads the job back from SQLite.
	r2 := &Runner{Store: st}
	s, err := r2.Get(id)
	if err != nil || s.Languages[0].Title != "Lluvia mía" || !s.Languages[0].Edited || s.State != Done {
		t.Fatalf("reload %+v %v", s, err)
	}
	if _, err := r2.Get("unknown"); err != ErrNotFound {
		t.Fatalf("got %v", err)
	}
}

func TestEditDuringARunIsKept(t *testing.T) {
	r, st := newRunner(t, 200*time.Millisecond)
	id := r.Start(rain, []string{"es"}, Meta{VideoID: "mockRain001"})
	time.Sleep(20 * time.Millisecond)
	if _, err := r.Edit(id, "es", "Mi título", "Mi descripción"); err != nil {
		t.Fatal(err)
	}
	s := waitDone(t, r, id)
	if s.Languages[0].Title != "Mi título" || !s.Languages[0].Edited {
		t.Fatalf("the person's edit must survive the end of the job: %+v", s.Languages[0])
	}
	_, rows, _, _ := st.LoadJob(id)
	if rows[0].Title != "Mi título" || !rows[0].Edited {
		t.Fatalf("stored: %+v", rows[0])
	}
}
