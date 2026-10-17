package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

func openMock(t *testing.T) *Service {
	t.Helper()
	s, err := Open(&config.Config{DataDir: t.TempDir(), OllamaURL: "http://127.0.0.1:1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestMockSeedsChannelNamesAsKeepWords(t *testing.T) {
	s := openMock(t)
	words := s.KeepWords()
	if len(words) != 2 || words[0] != "The Slow Night Hours" {
		t.Fatalf("got %v", words)
	}
}

func TestMockSwitches(t *testing.T) {
	s := openMock(t)
	ctx := context.Background()
	if st := s.ModelHealth(ctx); st.State != translate.ModelReady {
		t.Fatalf("got %v", st)
	}
	s.SetMock(MockState{Ollama: "missing", Auth: "none"})
	if st := s.ModelHealth(ctx); st.State != translate.ModelMissing {
		t.Fatalf("got %v", st)
	}
	if chans, _ := s.Channels(); len(chans) != 0 {
		t.Fatal("auth=none means no channel")
	}
}

func TestGetVideoAndWriterOwner(t *testing.T) {
	s := openMock(t)
	ctx := context.Background()
	v, _, err := s.GetVideo(ctx, "mockHarbor3")
	if err != nil || v.ChannelTitle != "The Living Postcard" {
		t.Fatalf("got %+v %v", v, err)
	}
	w, err := s.Writer(ctx, v)
	if err != nil || w.Client.ChannelID != "UCmockLivingPostcard02" {
		t.Fatalf("the writer uses the owner's sign-in: %v", err)
	}
}

func TestSaveWritesOneSectionPerLanguage(t *testing.T) {
	s := openMock(t)
	dir, err := s.Save(SaveRequest{
		SourceTitle: "Rain on a Cabin Window: 3 Hours!",
		Results: []translate.Result{
			{Lang: "es", Status: translate.StatusReady, Title: "Lluvia", Description: "Tres horas.\n#sleep"},
			{Lang: "ja", Status: translate.StatusCheck, Title: "雨", Description: "三時間", Note: "the title is 112 characters, YouTube allows 100"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "rain-on-a-cabin-window-3-hours" || filepath.Base(filepath.Dir(dir)) != "videos" {
		t.Fatalf("folder %s", dir)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "translations.md"))
	text := string(b)
	for _, want := range []string{"## Español (es)\n\nLluvia\n\nTres horas.\n#sleep\n", "## 日本語 (ja). Check this one: the title is 112", "Languages tab"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if err := s.OpenFolder(filepath.Join(dir, "..", "..", "..")); err == nil {
		t.Fatal("folders outside the save root must be refused")
	}
	if err := s.OpenFolder(dir); err != nil {
		t.Fatal(err)
	}
}

func TestSafeNames(t *testing.T) {
	if got := safeName("  ../The: Slow/Night  "); strings.ContainsAny(got, "/:") || strings.HasPrefix(got, ".") {
		t.Fatalf("got %q", got)
	}
	if got := slug("日本語だけ"); !strings.HasPrefix(got, "video-") {
		t.Fatalf("got %q", got)
	}
}
