package core

import (
	"slices"
	"strings"
	"testing"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/langs"
)

func TestUsedLanguagesFallbackOrder(t *testing.T) {
	s := openMock(t)
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, langs.Popular) {
		t.Fatalf("nothing translated yet: the popular twelve, got %v", got)
	}
	s.RememberLanguages("UCa", []string{"es", "ja", "it"})
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, []string{"es", "ja", "it"}) {
		t.Fatalf("the channel's own pick, got %v", got)
	}
	// A channel never translated on starts from the last pick anywhere.
	if got := s.UsedLanguages("UCb"); !slices.Equal(got, []string{"es", "ja", "it"}) {
		t.Fatalf("the last pick on any channel, got %v", got)
	}
	s.RememberLanguages("UCb", []string{"de", "pl"})
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, []string{"es", "ja", "it"}) {
		t.Fatalf("another channel's pick does not change this one, got %v", got)
	}
	if got := s.UsedLanguages("UCc"); !slices.Equal(got, []string{"de", "pl"}) {
		t.Fatalf("the latest pick is the one a new channel starts from, got %v", got)
	}
	// Pasted text has no channel: it sets only the last pick.
	s.RememberLanguages("", []string{"fr"})
	if got := s.UsedLanguages("UCb"); !slices.Equal(got, []string{"de", "pl"}) {
		t.Fatalf("a paste leaves the channel's pick alone, got %v", got)
	}
	if got := s.UsedLanguages(""); !slices.Equal(got, []string{"fr"}) {
		t.Fatalf("the paste screen starts from the last pick, got %v", got)
	}
}

func TestUsedLanguagesSurviveARestart(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), OllamaURL: "http://127.0.0.1:1"}
	s, err := Open(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	s.RememberLanguages("UCa", []string{"pt-BR", "ko", "uk"})
	s.Close()
	s, err = Open(cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, []string{"pt-BR", "ko", "uk"}) {
		t.Fatalf("the pick is read back after a restart, got %v", got)
	}
	if got := s.UsedLanguages("UCnew"); !slices.Equal(got, []string{"pt-BR", "ko", "uk"}) {
		t.Fatalf("and is the start for a new channel, got %v", got)
	}
}

func TestUsedLanguagesLeaveOutCodesNoLongerOffered(t *testing.T) {
	s := openMock(t)
	// Codes the list does not offer (YouTube refuses Cebuano and Chichewa),
	// an old alias and a repeat.
	s.Store.SetSetting(usedKeyChannel+"UCa", "ceb,es,ny,pt,es")
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, []string{"es", "pt-BR"}) {
		t.Fatalf("got %v", got)
	}
	// A saved pick with nothing left in it falls through to the next step.
	s.Store.SetSetting(usedKeyChannel+"UCb", "ceb,ny")
	s.Store.SetSetting(usedKey, "de")
	if got := s.UsedLanguages("UCb"); !slices.Equal(got, []string{"de"}) {
		t.Fatalf("got %v", got)
	}
	s.RememberLanguages("UCc", []string{"ceb"})
	if _, ok, _ := s.Store.Setting(usedKeyChannel + "UCc"); ok {
		t.Fatal("a pick with no offered language is not kept")
	}
}

func TestUsedLanguagesSkipALeftoverAllLanguagesPick(t *testing.T) {
	s := openMock(t)
	var all []string
	for _, l := range langs.All("en") {
		all = append(all, l.Code)
	}
	s.Store.SetSetting(usedKeyChannel+"UCa", strings.Join(all, ","))
	if got := s.UsedLanguages("UCa"); !slices.Equal(got, langs.Popular) {
		t.Fatalf("an old All languages pick must not tick %d languages", len(got))
	}
	// A long hand-made pick from More languages is kept.
	s.RememberLanguages("UCb", all[:40])
	if got := s.UsedLanguages("UCb"); len(got) != 40 {
		t.Fatalf("got %d", len(got))
	}
}
