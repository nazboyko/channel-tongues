package translate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestCandidateWords(t *testing.T) {
	cases := []struct {
		name     string
		original Back
		returned Back
		want     []string
	}{
		{"same words, other endings and small words", Back{"Rain on a Cabin Window at Night", "Three hours of soft rain."},
			Back{"The rain on the window of a cabin during the night", "Three hours of the soft rains"}, nil},
		{"units written out", Back{"night cabin 30s", ""}, Back{"night cabin 30 seconds", ""}, nil},
		{"hours spelled differently", Back{"Autumn Porch · 3h", ""}, Back{"Autumn porch · 3 hours", ""}, nil},
		{"an added word", Back{"night cabin 30s", ""}, Back{"30 seconds of quiet night at the cabin", ""}, []string{"quiet"}},
		{"an added keyword, case kept", Back{"Rain on a Cabin Window", ""}, Back{"Rain on a cabin window ASMR", ""}, []string{"ASMR"}},
		{"a synonym is only a candidate", Back{"night cabin 30s", ""}, Back{"night cottage 30s", ""}, []string{"cottage"}},
		{"per field: a title word moved from the line", Back{"night cabin 30s", "quiet night scenes"}, Back{"night cabin 30s quiet scenes", "quiet night scenes"}, []string{"quiet", "scenes"}},
		{"placeholders ignored", Back{"Rain ⟦1⟧", "More at ⟦2⟧"}, Back{"Rain ⟦1⟧", "More on ⟦2⟧"}, nil},
		{"created meets create", Back{"", "Music created with care"}, Back{"", "Music that was create with care"}, nil},
		{"each word once", Back{"night", ""}, Back{"calm calm night", ""}, []string{"calm"}},
	}
	for _, tc := range cases {
		got := CandidateWords(tc.original, tc.returned)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestStem(t *testing.T) {
	for in, want := range map[string]string{"sounds": "sound", "stopping": "stop", "falling": "fall", "stories": "story", "boxes": "box", "waves": "wave", "pass": "pass", "relaxed": "relax"} {
		if got := stem(in); got != want {
			t.Errorf("stem(%q) = %q, want %q", in, got, want)
		}
	}
}

func readyAnswer(title string) func(Request) (Output, error) {
	return answer(title, "Lluvia suave toda la noche.\n\n⟦2⟧ Empieza la lluvia\nMás en ⟦3⟧")
}

func TestBackTranslationFlagsAddedWords(t *testing.T) {
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){"es": {readyAnswer("Lluvia tranquila en la ventana de una cabaña · 3 horas ⟦1⟧")}},
		backs:   map[string]Back{"es": {Title: "Quiet rain on the window of a cabin · 3 hours ⟦1⟧", Line: "Soft rain all night."}},
	}
	res := (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	r := res[0]
	if r.Status != StatusCheck || r.Note != "adds: Quiet" || !reflect.DeepEqual(r.Added, []string{"Quiet"}) {
		t.Fatalf("got %+v", r)
	}
	if r.BackTitle != "Quiet rain on the window of a cabin · 3 hours #sleep" || r.BackLine != "Soft rain all night." {
		t.Fatalf("the shown back-translation has the spans restored: %q / %q", r.BackTitle, r.BackLine)
	}
}

func TestBackTranslationConfirmFiltersSynonyms(t *testing.T) {
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){"es": {readyAnswer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧")}},
		backs:   map[string]Back{"es": {Title: "Rain on the window of a cottage · 3 hours ⟦1⟧", Line: "Soft rain all night."}},
		confirm: []string{}, // the model says "cottage" means "cabin"
	}
	res := (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusReady || len(res[0].Added) != 0 || res[0].BackTitle == "" {
		t.Fatalf("a synonym must not flag the card: %+v", res[0])
	}
}

func TestBackTranslationFailedConfirmFlagsEveryCandidate(t *testing.T) {
	m := &scripted{
		answers:     map[string][]func(Request) (Output, error){"es": {readyAnswer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧")}},
		backs:       map[string]Back{"es": {Title: "Rain on the window of a cottage · 3 hours ⟦1⟧", Line: "Soft rain all night."}},
		newWordsErr: errors.New("model gone"),
	}
	res := (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusCheck || !strings.Contains(res[0].Note, "cottage") {
		t.Fatalf("without the confirming call every candidate counts: %+v", res[0])
	}
}

func TestBackTranslationKeepsAnEarlierReason(t *testing.T) {
	long := strings.Repeat("lluvia ", 16) + "⟦1⟧"
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){"es": {readyAnswer(long)}},
		backs:   map[string]Back{"es": {Title: "rain rain endless ⟦1⟧", Line: "Soft rain all night."}},
	}
	res := (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusCheck || !strings.Contains(res[0].Note, "YouTube allows 100") || len(res[0].Added) == 0 {
		t.Fatalf("the first reason stays, the added words are still marked: %+v", res[0])
	}
}

func TestBackTranslationOffAndOnFailure(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){"es": {readyAnswer("Lluvia en la ventana ⟦1⟧")}}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if m.backCalls != 0 || res[0].BackTitle != "" {
		t.Fatal("no back-translation unless asked")
	}
	failing := &failingBack{scripted: m}
	res = (&Engine{Model: failing, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusReady || res[0].BackTitle != "" {
		t.Fatalf("a failed back-translation leaves the result as it was: %+v", res[0])
	}
}

type failingBack struct{ *scripted }

func (f *failingBack) BackTranslate(ctx context.Context, req BackRequest) (Back, Stats, error) {
	return Back{}, Stats{}, errors.New("model gone")
}

func TestBackTranslationEventsComeLast(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {readyAnswer("Lluvia en la ventana ⟦1⟧")},
		"de": {readyAnswer("Regen am Fenster ⟦1⟧")},
	}}
	events := make(chan Event, 50)
	(&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es", "de"}, nil, events)
	close(events)
	final := map[string]*Result{}
	for ev := range events {
		if ev.Result != nil {
			final[ev.Lang] = ev.Result
		}
	}
	for _, l := range []string{"es", "de"} {
		if final[l] == nil || final[l].BackTitle == "" {
			t.Fatalf("the last event for %s carries the back-translation: %+v", l, final[l])
		}
	}
}

type recordingNewWords struct {
	*scripted
	reqs []NewWordsRequest
}

func (r *recordingNewWords) NewWords(ctx context.Context, req NewWordsRequest) ([]string, error) {
	r.reqs = append(r.reqs, req)
	return req.Words, nil
}

func TestCandidatesAreJudgedAgainstTheWholeSource(t *testing.T) {
	m := &recordingNewWords{scripted: &scripted{
		answers: map[string][]func(Request) (Output, error){"es": {readyAnswer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧")}},
		backs:   map[string]Back{"es": {Title: "Rain on the window of a cabin · 3 hours quiet ⟦1⟧", Line: "Soft rain all the calm night."}},
	}}
	(&Engine{Model: m, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if len(m.reqs) != 1 {
		t.Fatalf("one confirming call, got %d", len(m.reqs))
	}
	req := m.reqs[0]
	if !strings.Contains(req.Original, "Rain begins") || !strings.Contains(req.Original, "Rain on a Cabin Window") {
		t.Fatalf("the whole source goes to the confirming call: %q", req.Original)
	}
	if !reflect.DeepEqual(req.Words, []string{"quiet", "calm"}) {
		t.Fatalf("candidates %v", req.Words)
	}
}
