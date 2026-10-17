package youtube

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nazboyko/channel-tongues/internal/store"
)

func TestPlanMergeTable(t *testing.T) {
	existing := map[string]Localization{
		"de": {Title: "Regen (theirs)", Description: "d"},
		"fr": {Title: "Pluie (ours, untouched)", Description: "d"},
		"it": {Title: "Pioggia (ours, edited in Studio)", Description: "d2"},
		"pt": {Title: "Chuva (alias key)", Description: "d"},
		"ko": {Title: "same", Description: "same"},
	}
	history := map[string]store.HistoryEntry{
		"fr": {Lang: "fr", Title: "Pluie (ours, untouched)", Description: "d"},
		"it": {Lang: "it", Title: "Pioggia (ours)", Description: "d"},
	}
	ours := map[string]Localization{
		"es":    {Title: "Lluvia", Description: "d"},
		"de":    {Title: "Regen (new)", Description: "d"},
		"fr":    {Title: "Pluie (new)", Description: "d"},
		"it":    {Title: "Pioggia (new)", Description: "d"},
		"pt-BR": {Title: "Chuva (new)", Description: "d"},
		"ko":    {Title: "same", Description: "same"},
	}
	p := PlanMerge("vid", existing, history, ours, map[string]bool{"pt-BR": true})
	want := map[string]Outcome{
		"es": OutcomeAdded, "de": OutcomeKept, "fr": OutcomeUpdated,
		"it": OutcomeKept, "pt-BR": OutcomeReplaced, "ko": OutcomeSame,
	}
	for _, c := range p.Changes {
		if want[c.Lang] != c.Outcome {
			t.Errorf("%s: got %s, want %s", c.Lang, c.Outcome, want[c.Lang])
		}
		if c.Lang == "pt-BR" && c.Key != "pt" {
			t.Errorf("pt-BR must reuse the video's pt key, got %s", c.Key)
		}
	}
	if p.Merged["de"].Title != "Regen (theirs)" || p.Merged["it"].Title != "Pioggia (ours, edited in Studio)" {
		t.Fatal("kept languages must stay exactly as they were")
	}
	if p.Merged["es"].Title != "Lluvia" || p.Merged["fr"].Title != "Pluie (new)" || p.Merged["pt"].Title != "Chuva (new)" {
		t.Fatalf("merged %+v", p.Merged)
	}
	if _, dup := p.Merged["pt-BR"]; dup || len(p.Merged) != 6 {
		t.Fatalf("the merged map is complete and has no duplicate for an alias: %v", p.Merged)
	}
	if !p.Writes() {
		t.Fatal("this plan writes")
	}
	same := PlanMerge("vid", existing, nil, map[string]Localization{"ko": {"same", "same"}}, nil)
	if same.Writes() {
		t.Fatal("nothing new, nothing to write")
	}
}

func newWriter(t *testing.T) (*Writer, *Client, *fakeAPIRef) {
	t.Helper()
	c, api, st := newTestClient(t)
	w := &Writer{Client: c, Backups: &Backups{Dir: filepath.Join(t.TempDir(), "backups")}, Store: st, ReadBackWaits: []time.Duration{}}
	return w, c, &fakeAPIRef{api}
}

func TestPublishCycleAndRestore(t *testing.T) {
	w, c, api := newWriter(t)
	ctx := context.Background()
	before := api.Localizations("mockRain001")

	ours := map[string]Localization{
		"es": {Title: "Lluvia en la ventana", Description: "Tres horas."},
		"de": {Title: "Regen (new)", Description: "x"}, // the video has someone else's de: kept
		"en": {Title: "Rain", Description: "ignored"},  // the video's own language: never written
	}
	v, plan, err := w.Prepare(ctx, "mockRain001", ours, nil)
	if err != nil {
		t.Fatal(err)
	}
	usedBefore, _ := c.Ledger().UsedToday()
	res, err := w.Publish(ctx, v, plan, "job1")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Wrote || res.BackupID == "" {
		t.Fatalf("result %+v", res)
	}
	after := api.Localizations("mockRain001")
	if after["es"]["title"] != "Lluvia en la ventana" || after["de"]["title"] != before["de"]["title"] || after["fr"]["title"] != before["fr"]["title"] {
		t.Fatalf("after publish %v", after)
	}
	if after["en"]["title"] != before["en"]["title"] || after["en"]["description"] != before["en"]["description"] {
		t.Fatalf("the video's own language (listed by YouTube among the translations) must stay untouched: %v", after["en"])
	}
	used, _ := c.Ledger().UsedToday()
	if used-usedBefore+1 != CostPublish { // +1: Prepare's read happened before usedBefore
		t.Fatalf("a publish costs %d units, ledger moved by %d", CostPublish, used-usedBefore+1)
	}
	for _, r := range api.Requests() {
		if r.Method == "PUT" && (r.Part != "localizations" || strings.Contains(strings.Join(r.BodyKeys, ","), "snippet")) {
			t.Fatalf("an update must carry localizations only: %+v", r)
		}
	}
	h, _ := w.Store.History("mockRain001")
	if h["es"].Title != "Lluvia en la ventana" || h["es"].BackupID != res.BackupID {
		t.Fatalf("history %+v", h)
	}

	// Publishing the same again is a no-op; a newer one updates ours.
	v, plan, _ = w.Prepare(ctx, "mockRain001", map[string]Localization{"es": {Title: "Lluvia nueva", Description: "Tres horas."}}, nil)
	if plan.Changes[0].Outcome != OutcomeUpdated {
		t.Fatalf("ours from last time is updated, got %s", plan.Changes[0].Outcome)
	}
	if _, err := w.Publish(ctx, v, plan, "job2"); err != nil {
		t.Fatal(err)
	}

	// Restore the first backup: back to exactly the state before.
	rres, err := w.Restore(ctx, "mockRain001", res.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	if !rres.Wrote || rres.BackupID == "" {
		t.Fatalf("restore %+v", rres)
	}
	restored := api.Localizations("mockRain001")
	if len(restored) != len(before) || restored["de"]["title"] != before["de"]["title"] {
		t.Fatalf("restore did not return to the state before: %v vs %v", restored, before)
	}
	if _, ok := restored["es"]; ok {
		t.Fatal("es must be gone after restoring the state before the first publish")
	}
	if h, _ := w.Store.History("mockRain001"); len(h) != 0 {
		t.Fatalf("history of a restored-away language is forgotten: %v", h)
	}
	list, _ := w.Backups.List("mockRain001")
	if len(list) != 3 {
		t.Fatalf("two publishes and one restore leave three backups, got %d", len(list))
	}
}

func TestReadBackMismatchIsReported(t *testing.T) {
	w, _, api := newWriter(t)
	api.DropOnUpdate = "es"
	ctx := context.Background()
	v, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"a", "b"}, "de": {"c", "d"}}, nil)
	res, err := w.Publish(ctx, v, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Outcome{}
	for _, c := range res.Changes {
		got[c.Lang] = c.Outcome
	}
	if got["es"] != OutcomeFailed || got["de"] != OutcomeAdded {
		t.Fatalf("got %v", got)
	}
	if h, _ := w.Store.History("mockPorch02"); len(h) != 1 {
		t.Fatalf("only the language that read back is recorded: %v", h)
	}
}

func TestNoDefaultLanguageThenSetIt(t *testing.T) {
	w, _, _ := newWriter(t)
	ctx := context.Background()
	if _, _, err := w.Prepare(ctx, "mockNoLang1", map[string]Localization{"es": {"a", "b"}}, nil); !errors.Is(err, ErrNoDefaultLanguage) {
		t.Fatalf("got %v", err)
	}
	backup, err := w.SetDefaultLanguage(ctx, "mockNoLang1", "en")
	if err != nil || backup == "" {
		t.Fatalf("set default language: %v %q", err, backup)
	}
	v, _, err := w.Prepare(ctx, "mockNoLang1", map[string]Localization{"es": {"a", "b"}}, nil)
	if err != nil || v.DefaultLanguage != "en" || v.Title != "Quiet Library Afternoon · Pages and Soft Rain" {
		t.Fatalf("after: %+v %v", v, err)
	}
}

func TestGuardRefusesOtherVideos(t *testing.T) {
	w, _, api := newWriter(t)
	w.Allowed = func(id string) error {
		if id != "mockRain001" {
			return errors.New("not a test video")
		}
		return nil
	}
	ctx := context.Background()
	v, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"a", "b"}}, nil)
	if _, err := w.Publish(ctx, v, plan, ""); err == nil {
		t.Fatal("publishing to a video outside the list must be refused")
	}
	if _, err := w.SetDefaultLanguage(ctx, "mockPorch02", "en"); err == nil {
		t.Fatal("set-default-language outside the list must be refused")
	}
	for _, r := range api.Requests() {
		if r.Method == "PUT" {
			t.Fatal("no update may reach the API")
		}
	}
	if list, _ := w.Backups.List("mockPorch02"); len(list) != 0 {
		t.Fatal("a refused write leaves no backup")
	}
}

func TestAPIRefusalKeepsTheBackup(t *testing.T) {
	w, _, api := newWriter(t)
	api.FailUpdate = "quotaExceeded"
	ctx := context.Background()
	v, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"a", "b"}}, nil)
	res, err := w.Publish(ctx, v, plan, "")
	var ae *APIError
	if !errors.As(err, &ae) || !strings.Contains(err.Error(), "midnight Pacific") {
		t.Fatalf("got %v", err)
	}
	if res == nil || res.BackupID == "" || res.Wrote {
		t.Fatalf("the backup exists and nothing was written: %+v", res)
	}
}

func TestStalePlanIsRefused(t *testing.T) {
	w, _, _ := newWriter(t)
	ctx := context.Background()
	va, planA, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"Linterna", "d"}}, nil)
	vb, planB, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"de": {"Laterne", "d"}}, nil)
	if _, err := w.Publish(ctx, va, planA, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Publish(ctx, vb, planB, ""); !errors.Is(err, ErrVideoChanged) {
		t.Fatalf("a plan made before another publish must be refused, got %v", err)
	}
	if list, _ := w.Backups.List("mockPorch02"); len(list) != 1 {
		t.Fatalf("the refused publish leaves no backup, got %d", len(list))
	}
}

func TestUndoOfASecondPublishGivesTheFirstBack(t *testing.T) {
	w, _, api := newWriter(t)
	ctx := context.Background()
	publish := func(title string) *PublishResult {
		t.Helper()
		v, plan, err := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {title, "d"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := w.Publish(ctx, v, plan, "")
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	publish("Uno")
	second := publish("Dos")
	if _, err := w.Restore(ctx, "mockPorch02", second.BackupID); err != nil {
		t.Fatal(err)
	}
	if got := api.Localizations("mockPorch02")["es"]["title"]; got != "Uno" {
		t.Fatalf("undo puts the first text back, got %q", got)
	}
	h, _ := w.Store.History("mockPorch02")
	if h["es"].Title != "Uno" {
		t.Fatalf("the first text is ours again: %+v", h)
	}
	_, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"Tres", "d"}}, nil)
	if plan.Changes[0].Outcome != OutcomeUpdated {
		t.Fatalf("a later publish updates it, got %s", plan.Changes[0].Outcome)
	}
}

func TestUndoKeepsALanguageAddedInStudioMeanwhile(t *testing.T) {
	w, c, api := newWriter(t)
	ctx := context.Background()
	v, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"Linterna", "d"}}, nil)
	res, err := w.Publish(ctx, v, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	// Someone adds Italian in Studio after the publish.
	now, _ := c.GetLocalizations(ctx, "mockPorch02")
	now["it"] = Localization{"Lanterna", "d"}
	if err := c.UpdateLocalizations(ctx, "mockPorch02", now); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Restore(ctx, "mockPorch02", res.BackupID); err != nil {
		t.Fatal(err)
	}
	after := api.Localizations("mockPorch02")
	if _, ok := after["es"]; ok {
		t.Fatal("undo removes what the publish added")
	}
	if after["it"]["title"] != "Lanterna" {
		t.Fatalf("undo keeps what was added in Studio since: %v", after)
	}
}

func TestReadBackWaitsForALaggingRead(t *testing.T) {
	w, _, api := newWriter(t)
	api.StaleReads = 2 // the next two reads after a write still show the old state, as YouTube did
	w.ReadBackWaits = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
	ctx := context.Background()
	v, plan, _ := w.Prepare(ctx, "mockPorch02", map[string]Localization{"es": {"Linterna", "d"}}, nil)
	res, err := w.Publish(ctx, v, plan, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Changes[0].Outcome != OutcomeAdded {
		t.Fatalf("a read that lags must not be reported as a failure: %+v", res.Changes)
	}
}
