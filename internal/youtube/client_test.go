package youtube

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/nazboyko/channel-tongues/internal/store"
	"github.com/nazboyko/channel-tongues/internal/youtube/fake"
)

func newTestClient(t *testing.T) (*Client, *fake.API, *store.Store) {
	t.Helper()
	api := fake.New()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	st, err := store.Open(filepath.Join(t.TempDir(), "tongues.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := NewClient(context.Background(), ClientOptions{HTTP: srv.Client(), Endpoint: srv.URL, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	return c, api, st
}

func TestGetVideo(t *testing.T) {
	c, _, _ := newTestClient(t)
	v, err := c.GetVideo(context.Background(), "mockRain001")
	if err != nil {
		t.Fatal(err)
	}
	if v.Title == "" || v.DefaultLanguage != "en" || v.ChannelID != "UCmockSlowNightHours01" {
		t.Fatalf("unexpected video %+v", v)
	}
	if got := v.Languages(); len(got) != 2 || got[0] != "de" || got[1] != "fr" {
		t.Fatalf("languages %v", got)
	}
	if len(v.Snippet) == 0 {
		t.Fatal("the raw snippet is kept for backups")
	}
	if !v.Has("de") || v.Has("es") {
		t.Fatal("Has")
	}
	used, _ := c.Ledger().UsedToday()
	if used != 1 {
		t.Fatalf("videos.list costs 1 unit, ledger shows %d", used)
	}
}

func TestGetVideoNotFound(t *testing.T) {
	c, _, _ := newTestClient(t)
	if _, err := c.GetVideo(context.Background(), "doesNotExis"); !errors.Is(err, ErrVideoNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestOwnLanguageIsNotCountedAsATranslation(t *testing.T) {
	// The real API lists the default language inside localizations.
	v := &Video{DefaultLanguage: "en", Localizations: map[string]Localization{"en": {Title: "x"}, "es": {Title: "y"}}}
	if got := v.Languages(); len(got) != 1 || got[0] != "es" {
		t.Fatalf("got %v", got)
	}
}

func TestAliasLookup(t *testing.T) {
	v := &Video{Localizations: map[string]Localization{"pt": {Title: "x"}}}
	if l, ok := v.Localization("pt-BR"); !ok || l.Title != "x" {
		t.Fatal("pt-BR should match a pt translation")
	}
}

func TestListUploadsPagesAndKeepsOrder(t *testing.T) {
	c, _, _ := newTestClient(t)
	vids, err := c.ListUploads(context.Background(), "UCmockLivingPostcard02", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(vids) != 3 || vids[0].ID != "mockHarbor3" || vids[2].ID != "mockNoLang1" {
		ids := []string{}
		for _, v := range vids {
			ids = append(ids, v.ID)
		}
		t.Fatalf("got %v", ids)
	}
	limited, err := c.ListUploads(context.Background(), "UCmockLivingPostcard02", 2)
	if err != nil || len(limited) != 2 {
		t.Fatalf("limit: %d %v", len(limited), err)
	}
}

func TestMyChannel(t *testing.T) {
	c, api, _ := newTestClient(t)
	api.Mine = "UCmockLivingPostcard02"
	ch, err := c.MyChannel(context.Background())
	if err != nil || ch.Title != "The Living Postcard" {
		t.Fatalf("got %+v %v", ch, err)
	}
}

func TestI18nLanguagesAreCached(t *testing.T) {
	c, api, _ := newTestClient(t)
	first, err := c.I18nLanguages(context.Background())
	if err != nil || len(first) == 0 {
		t.Fatalf("got %v %v", first, err)
	}
	second, _ := c.I18nLanguages(context.Background())
	calls := 0
	for _, r := range api.Requests() {
		if r.Path == "/i18nLanguages" {
			calls++
		}
	}
	if calls != 1 || len(second) != len(first) {
		t.Fatalf("expected one API call, got %d", calls)
	}
}

func TestAPIErrorsBecomeSentences(t *testing.T) {
	cases := []struct {
		code   int
		reason string
		want   string
	}{
		{400, "defaultLanguageNotSet", "Title and description language"},
		{403, "quotaExceeded", "midnight Pacific"},
		{403, "forbidden", "channel that owns it"},
		{401, "authError", "tongues auth"},
		{400, "invalidTitle", "100 characters"},
	}
	for _, tc := range cases {
		err := apiError(&googleapi.Error{Code: tc.code, Message: "raw", Errors: []googleapi.ErrorItem{{Reason: tc.reason}}})
		var ae *APIError
		if !errors.As(err, &ae) || ae.Reason != tc.reason || !strings.Contains(ae.Error(), tc.want) {
			t.Errorf("%s: got %v", tc.reason, err)
		}
	}
	plain := errors.New("network down")
	if apiError(plain) != plain {
		t.Fatal("non-API errors pass through")
	}
}

func TestPacificDayRollover(t *testing.T) {
	// 23:59 Pacific on Oct 16 is still Oct 16; one minute later is Oct 17.
	before := time.Date(2026, 10, 17, 6, 59, 0, 0, time.UTC) // PDT is UTC-7
	after := before.Add(time.Minute)
	if PacificDay(before) != "2026-10-16" || PacificDay(after) != "2026-10-17" {
		t.Fatalf("got %s and %s", PacificDay(before), PacificDay(after))
	}
	if r := NextReset(before); !r.Equal(after) {
		t.Fatalf("next reset %v, want %v", r, after)
	}
}

func TestLedgerBudget(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	clock := time.Date(2026, 10, 17, 6, 0, 0, 0, time.UTC)
	l := &Ledger{Store: st, Now: func() time.Time { return clock }}
	left, _ := l.VideosLeftToday()
	if left != DailyBudget/CostPublish {
		t.Fatalf("fresh day: %d", left)
	}
	l.Spend(DailyBudget - CostPublish)
	if left, _ := l.VideosLeftToday(); left != 1 {
		t.Fatalf("one left: %d", left)
	}
	l.Spend(CostPublish * 3)
	if left, _ := l.VideosLeftToday(); left != 0 {
		t.Fatalf("never negative: %d", left)
	}
	clock = clock.Add(2 * time.Hour) // past Pacific midnight
	if used, _ := l.UsedToday(); used != 0 {
		t.Fatalf("new Pacific day starts at 0, got %d", used)
	}
}

func TestNilLedgerIsSafe(t *testing.T) {
	var l *Ledger
	if err := l.Spend(5); err != nil {
		t.Fatal(err)
	}
}

// fakeAPIRef gives write tests direct access to the fake's switches.
type fakeAPIRef struct{ *fake.API }
