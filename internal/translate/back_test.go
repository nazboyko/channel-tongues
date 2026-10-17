package translate

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/nazboyko/channel-tongues/internal/langs"
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
	if r.Status != StatusCheck || r.Note != "adds: quiet" || !reflect.DeepEqual(r.Added, []string{"Quiet"}) {
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
	if res[0].Status != StatusCheck || !strings.Contains(res[0].Note, "could not be read back") || !res[0].BackFailed {
		t.Fatalf("a failed back-translation means the meaning was not checked: the safe side: %+v", res[0])
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

func (r *recordingNewWords) NewWords(ctx context.Context, req NewWordsRequest) ([]string, Stats, error) {
	r.reqs = append(r.reqs, req)
	return req.Words, Stats{}, nil
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

func TestVouchingNeedsRealOriginalWords(t *testing.T) {
	have := wordSet("A cozy cabin on a quiet night")
	cases := map[string]bool{
		"":                 false, // named nothing
		"the":              false, // only a small word
		"the cabin":        true,
		"cabin":            true,
		"cozy cabin":       true,
		"stunning cabin":   false, // a word the original does not have
		"quiet nights":     true,
		"cabin by the sea": false,
	}
	for named, want := range cases {
		if got := inOriginal(named, have); got != want {
			t.Errorf("inOriginal(%q) = %v, want %v", named, got, want)
		}
	}
}

func TestNoEndingTricksBetweenDifferentWords(t *testing.T) {
	for _, tc := range [][2]string{{"Hug a cat", "Huge cat"}, {"a red car", "a red care"}, {"a plan", "a plane"}, {"fire", "fir tree"}} {
		if got := CandidateWords(Back{Title: tc[0]}, Back{Title: tc[1]}); len(got) == 0 {
			t.Errorf("%q -> %q: the new word must be a candidate", tc[0], tc[1])
		}
	}
	if got := CandidateWords(Back{Line: "Music created with care"}, Back{Line: "Music they create with care"}); len(got) != 0 {
		t.Errorf("created/create is the same word, got %v", got)
	}
	if got := CandidateWords(Back{Line: "Music they create with care"}, Back{Line: "Music created with care"}); len(got) != 0 {
		t.Errorf("create/created is the same word, got %v", got)
	}
	if got := CandidateWords(Back{Title: "Rain"}, Back{Title: "Rain news"}); !reflect.DeepEqual(got, []string{"news"}) {
		t.Errorf("news is a word, got %v", got)
	}
}

func TestWordTestOnlyForEnglishSources(t *testing.T) {
	src := Source{Title: "夜の山小屋の窓に降る雨の音", Description: "三時間の雨。", Language: "ja"}
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){"es": {answer("Lluvia en la ventana de una cabaña de montaña", "Tres horas de lluvia.")}},
		backs:   map[string]Back{"es": {Title: "窓に当たる雨の音", Line: "三時間の雨。"}},
	}
	res := (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), src, []string{"es"}, nil, nil)
	if res[0].Status != StatusReady || len(res[0].Added) != 0 || res[0].BackTitle == "" {
		t.Fatalf("for a Japanese source the line is shown, the word test is skipped: %+v", res[0])
	}
}

// The source of a real test video: "made with AI tools". The read-backs of
// most languages say "created", which the confirming step did not accept.
func lakeRun(t *testing.T, codes []string, backs map[string]Back) []Result {
	t.Helper()
	answers := map[string][]func(Request) (Output, error){}
	for _, c := range codes {
		answers[c] = []func(Request) (Output, error){answer("lago de otoño 30s", "El pueblo es imaginado. La imagen y la música se hicieron con herramientas de IA y se juntaron a mano.\n\n⟦1⟧: lugares tranquilos y piano suave.")}
	}
	m := &scripted{answers: answers, backs: backs}
	src := Source{Title: "autumn lake 30s", Description: wrappedSource, Language: "en"}
	return (&Engine{Model: m, BackTranslate: true}).Run(context.Background(), src, codes, []string{"The Living Postcard"}, nil)
}

func TestReadBackWordInMostLanguagesIsNotFlagged(t *testing.T) {
	codes := []string{"es", "pt-BR", "hi", "id", "ja", "de", "vi", "tr", "ko", "fr", "ar", "th", "pl", "ru", "uk"}
	created := Back{Title: "autumn lake 30s", Line: "The town is imagined, not a real place. The picture and the music were created with AI tools and put together by hand."}
	plain := Back{Title: "autumn lake 30s", Line: "The town is imagined, not a real place. The picture and the music were made with AI tools and put together by hand."}
	backs := map[string]Back{}
	for i, c := range codes {
		backs[c] = plain
		if i < 10 {
			backs[c] = created
		}
	}
	backs["tr"] = Back{Title: "mountain lake 30s", Line: created.Line} // a real change in the title
	res := lakeRun(t, codes, backs)
	for _, r := range res {
		for _, w := range r.Added {
			if strings.EqualFold(w, "created") {
				t.Fatalf("%s: created comes from the read-back wording and must not be marked: %+v", r.Lang, r)
			}
		}
		if r.Lang == "tr" {
			if r.Status != StatusCheck || r.Note != "adds: mountain" {
				t.Fatalf("Turkish mountain must still be caught: %+v", r)
			}
			continue
		}
		// The stub answers Spanish for every language, so non-Latin ones fail
		// their script check; only the read-back reasons matter here.
		if strings.HasPrefix(r.Note, "adds") {
			t.Fatalf("%s must not be flagged by the read-back: %+v", r.Lang, r)
		}
		if strings.HasPrefix(langNameScript(r.Lang), "Latin") && r.Status != StatusReady {
			t.Fatalf("%s should be ready: %+v", r.Lang, r)
		}
	}
}

func langNameScript(code string) string {
	l, _ := langs.Get(code)
	return string(l.Script)
}

func TestSharedWordsNeedThreeLanguages(t *testing.T) {
	backs := map[string]Back{
		"es": {Title: "autumn lake 30s", Line: "The picture and the music were created with AI tools and put together by hand."},
		"de": {Title: "autumn lake 30s", Line: "The picture and the music were created with AI tools and put together by hand."},
	}
	res := lakeRun(t, []string{"es", "de"}, backs)
	for _, r := range res {
		if len(r.AddedLine) != 1 || r.AddedLine[0] != "created" {
			t.Fatalf("with two languages a word is not dropped as shared: %+v", r)
		}
	}
}

func TestDescriptionOnlyAdditionsLeaveTheCardTicked(t *testing.T) {
	res := lakeRun(t, []string{"es"}, map[string]Back{
		"es": {Title: "autumn lake 30s", Line: "The town is imagined, not a real place. The beautiful picture and the music were made with AI tools and put together by hand."},
	})
	r := res[0]
	if r.Status != StatusReady || r.Note != "" || !reflect.DeepEqual(r.AddedLine, []string{"beautiful"}) || len(r.AddedTitle) != 0 {
		t.Fatalf("a word added only in the description stays marked, the card stays ready: %+v", r)
	}
}
