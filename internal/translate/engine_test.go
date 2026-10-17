package translate

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// scripted answers per language in order; the last answer repeats.
type scripted struct {
	mu          sync.Mutex
	answers     map[string][]func(Request) (Output, error)
	calls       map[string]int
	requests    []Request
	detect      map[string]string // text prefix -> code
	backs       map[string]Back
	confirm     []string
	newWordsErr error
	backCalls   int
	active      int32
	maxSeen     int32
	delay       time.Duration
}

func (s *scripted) Translate(ctx context.Context, req Request) (Output, Stats, error) {
	n := atomic.AddInt32(&s.active, 1)
	defer atomic.AddInt32(&s.active, -1)
	for {
		m := atomic.LoadInt32(&s.maxSeen)
		if n <= m || atomic.CompareAndSwapInt32(&s.maxSeen, m, n) {
			break
		}
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return Output{}, Stats{}, ctx.Err()
		}
	}
	s.mu.Lock()
	if s.calls == nil {
		s.calls = map[string]int{}
	}
	list := s.answers[req.Lang]
	i := min(s.calls[req.Lang], len(list)-1)
	s.calls[req.Lang]++
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	out, err := list[i](req)
	return out, Stats{NumCtx: 8192}, err
}

// BackTranslate answers from backs (by language), or echoes the text.
func (s *scripted) BackTranslate(ctx context.Context, req BackRequest) (Back, Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.backCalls++
	if b, ok := s.backs[req.Lang]; ok {
		return b, Stats{}, nil
	}
	return Back{Title: req.Title, Line: req.Line}, Stats{}, nil
}

// NewWords confirms the candidates listed in confirm (all of them when nil).
func (s *scripted) NewWords(ctx context.Context, req NewWordsRequest) ([]string, Stats, error) {
	if s.newWordsErr != nil {
		return nil, Stats{}, s.newWordsErr
	}
	if s.confirm == nil {
		return req.Words, Stats{PromptTokens: 300, OutputTokens: 20}, nil
	}
	return keepListed(s.confirm, req.Words), Stats{PromptTokens: 300, OutputTokens: 20}, nil
}

func (s *scripted) DetectLanguage(ctx context.Context, text string) (string, Stats, error) {
	for prefix, code := range s.detect {
		if strings.HasPrefix(text, prefix) {
			return code, Stats{PromptTokens: 90, OutputTokens: 8}, nil
		}
	}
	return "", Stats{PromptTokens: 90, OutputTokens: 8}, nil
}

func answer(title, desc string) func(Request) (Output, error) {
	return func(Request) (Output, error) { return Output{title, desc}, nil }
}

var testSource = Source{
	Title:       "Rain on a Cabin Window · 3 Hours #sleep",
	Description: "Soft rain all night.\n\n0:00 Rain begins\nMore at https://example.com/rain",
	Language:    "en",
}

// In protected form the source is:
//   title: "Rain on a Cabin Window · 3 Hours ⟦1⟧"
//   description: "Soft rain all night.\n\n⟦2⟧ Rain begins\nMore at ⟦3⟧"

func TestEngineHappyPathRestoresSpans(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {answer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧", "Lluvia suave toda la noche.\n\n⟦2⟧ Empieza la lluvia\nMás en ⟦3⟧")},
	}}
	e := &Engine{Model: m}
	res := e.Run(context.Background(), testSource, []string{"es"}, nil, nil)
	r := res[0]
	if r.Status != StatusReady || r.Retries != 0 {
		t.Fatalf("got %+v", r)
	}
	if r.Title != "Lluvia en la ventana de una cabaña · 3 horas #sleep" || !strings.Contains(r.Description, "0:00 Empieza") || !strings.HasSuffix(r.Description, "https://example.com/rain") {
		t.Fatalf("spans not restored: %q / %q", r.Title, r.Description)
	}
	req := m.requests[0]
	if req.SourceLang != "en" || !strings.Contains(req.Kind, "Rain on a Cabin Window") || strings.Contains(req.Source.Description, "https://") {
		t.Fatalf("request %+v", req)
	}
}

func TestEngineRetriesWithTheExactProblem(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"de": {
			answer("Regen am Hüttenfenster · 3 Stunden ⟦1⟧", "Sanfter Regen die ganze Nacht.\n\n⟦2⟧ Der Regen beginnt\nMehr auf dem Kanal"),
			answer("Regen am Hüttenfenster · 3 Stunden ⟦1⟧", "Sanfter Regen die ganze Nacht.\n\n⟦2⟧ Der Regen beginnt\nMehr auf ⟦3⟧"),
		},
	}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"de"}, nil, nil)
	if res[0].Status != StatusReady || res[0].Retries != 1 {
		t.Fatalf("got %+v", res[0])
	}
	retry := m.requests[1]
	if retry.Problem == nil || !strings.Contains(retry.Problem.Feedback, "⟦3⟧") || retry.Previous == nil {
		t.Fatalf("the retry must carry the previous answer and the problem: %+v", retry)
	}
}

func TestEngineMarksCheckThisOneAfterTwoRetries(t *testing.T) {
	long := strings.Repeat("夜の雨", 40) + " ⟦1⟧"
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"ja": {answer(long, "一晩中のやさしい雨。\n\n⟦2⟧ 雨が降り始める\nもっと見る ⟦3⟧")},
	}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"ja"}, nil, nil)
	r := res[0]
	if r.Status != StatusCheck || r.Retries != 2 || !strings.Contains(r.Note, "YouTube allows 100") {
		t.Fatalf("got %+v", r)
	}
	if m.calls["ja"] != 3 {
		t.Fatalf("one try and two retries, got %d calls", m.calls["ja"])
	}
	if !strings.Contains(r.Title, "#sleep") {
		t.Fatal("the text is still restored so the person can edit it")
	}
}

func TestEngineCatchesEnglishWithTheLanguageCheck(t *testing.T) {
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){
			"es": {answer("Rain on a cabin window · three hours ⟦1⟧", "Soft rain the whole night.\n\n⟦2⟧ The rain begins\nMore at ⟦3⟧")},
		},
		detect: map[string]string{"Rain on a cabin": "en"},
	}
	res := (&Engine{Model: m, DetectLatin: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusCheck || res[0].Note != "it looks like it is still in English" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestEngineRetriesABadAnswer(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"fr": {
			func(Request) (Output, error) { return Output{}, &BadAnswer{Raw: "Sure! Here is"} },
			answer("Pluie sur la fenêtre d'un chalet · 3 heures ⟦1⟧", "Pluie douce toute la nuit.\n\n⟦2⟧ La pluie commence\nPlus sur ⟦3⟧"),
		},
	}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"fr"}, nil, nil)
	if res[0].Status != StatusReady || res[0].Retries != 1 {
		t.Fatalf("got %+v", res[0])
	}
}

func TestEngineModelDown(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {func(Request) (Output, error) { return Output{}, ErrModelDown }},
	}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusFailed || !strings.Contains(res[0].Note, "start Ollama") {
		t.Fatalf("got %+v", res[0])
	}
}

func TestEngineEventsAndParallelLimit(t *testing.T) {
	targets := []string{"es", "pt-BR", "de", "fr", "it", "pl"}
	ok := func(Request) (Output, error) {
		return Output{"Chuva na janela ⟦1⟧", "Chuva suave a noite toda.\n\n⟦2⟧ A chuva começa\nMais em ⟦3⟧"}, nil
	}
	answers := map[string][]func(Request) (Output, error){}
	for _, l := range targets {
		answers[l] = []func(Request) (Output, error){ok}
	}
	m := &scripted{answers: answers, delay: 20 * time.Millisecond}
	events := make(chan Event, 100)
	res := (&Engine{Model: m, Parallel: 3}).Run(context.Background(), testSource, targets, nil, events)
	close(events)
	if m.maxSeen > 3 || m.maxSeen < 2 {
		t.Fatalf("at most three at once (and more than one), saw %d", m.maxSeen)
	}
	seen := map[Status]int{}
	for ev := range events {
		seen[ev.Status]++
	}
	if seen[StatusWaiting] != 6 || seen[StatusTranslating] != 6 || seen[StatusChecking] != 6 {
		t.Fatalf("events %v", seen)
	}
	for i, r := range res {
		if r.Lang != targets[i] {
			t.Fatalf("results keep the requested order: %v", res)
		}
	}
}

func TestEngineStartsLanguagesInOrder(t *testing.T) {
	targets := []string{"es", "pt-BR", "de", "fr", "it", "pl", "nl"}
	ok := func(Request) (Output, error) {
		return Output{"Chuva na janela ⟦1⟧", "Chuva suave a noite toda.\n\n⟦2⟧ A chuva começa\nMais em ⟦3⟧"}, nil
	}
	answers := map[string][]func(Request) (Output, error){}
	for _, l := range targets {
		answers[l] = []func(Request) (Output, error){ok}
	}
	m := &scripted{answers: answers}
	(&Engine{Model: m, Parallel: 1}).Run(context.Background(), testSource, targets, nil, nil)
	for i, r := range m.requests {
		if r.Lang != targets[i] {
			t.Fatalf("one at a time means in order; call %d was %s", i, r.Lang)
		}
	}
}

func TestEngineCancel(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {answer("x ⟦1⟧", "y\n\n⟦2⟧ z\n⟦3⟧")},
		"de": {answer("x ⟦1⟧", "y\n\n⟦2⟧ z\n⟦3⟧")},
	}, delay: 200 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	res := (&Engine{Model: m, Parallel: 1}).Run(ctx, testSource, []string{"es", "de"}, nil, nil)
	for _, r := range res {
		if r.Status != StatusCancelled {
			t.Fatalf("got %+v", r)
		}
	}
}

func TestPlainModelError(t *testing.T) {
	if got := plainModelError(errors.New(`ollama answered 500: {"raw":"json"}`)); strings.Contains(got, "raw") {
		t.Fatalf("raw errors must not reach the person: %q", got)
	}
}

func TestEngineAllBadAnswersIsFailed(t *testing.T) {
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {func(Request) (Output, error) { return Output{}, &BadAnswer{Raw: "nope"} }},
	}}
	res := (&Engine{Model: m}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	if res[0].Status != StatusFailed || res[0].Title != "" {
		t.Fatalf("got %+v", res[0])
	}
}

func TestEngineNormalizesWindowsLineEndings(t *testing.T) {
	var got Request
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"es": {func(r Request) (Output, error) { got = r; return Output{"Lluvia", "Uno\n\nDos"}, nil }},
	}}
	(&Engine{Model: m}).Run(context.Background(), Source{Title: "Rain", Description: "One\r\n\r\nTwo", Language: "en"}, []string{"es"}, nil, nil)
	if strings.Contains(got.Source.Description, "\r") {
		t.Fatalf("carriage returns must not reach the model: %q", got.Source.Description)
	}
}

func TestSameFamily(t *testing.T) {
	for _, pair := range [][2]string{{"ms", "id"}, {"nb", "no"}, {"tl", "fil"}, {"sr-Latn", "hr"}, {"ak", "tw"}} {
		if !sameFamily(pair[0], pair[1]) {
			t.Errorf("%s and %s are close enough for the language check", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{{"zu", "nd"}, {"it", "ie"}, {"fr", "wa"}} {
		if sameFamily(pair[0], pair[1]) {
			t.Errorf("%s answering for %s should still be held back", pair[0], pair[1])
		}
	}
}

// Every model call for a language is kept with what it was for, so the
// measurements can show the read-back apart from the translation.
func TestEngineRecordsEveryCall(t *testing.T) {
	m := &scripted{
		answers: map[string][]func(Request) (Output, error){
			"es": {
				answer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧", "Lluvia suave toda la noche.\n\n⟦2⟧ Empieza la lluvia\nMás en el canal"),
				answer("Lluvia en la ventana de una cabaña · 3 horas ⟦1⟧", "Lluvia suave toda la noche.\n\n⟦2⟧ Empieza la lluvia\nMás en ⟦3⟧"),
			},
		},
		detect: map[string]string{"Lluvia": "es"},
		backs:  map[string]Back{"es": {Title: "Rain on a mountain cabin window · 3 hours ⟦1⟧", Line: "Soft rain all night."}},
	}
	res := (&Engine{Model: m, DetectLatin: true, BackTranslate: true}).Run(context.Background(), testSource, []string{"es"}, nil, nil)
	var kinds []string
	for _, c := range res[0].Calls {
		kinds = append(kinds, string(c.Kind))
	}
	want := "translate,translate,language check,read-back,word check"
	if strings.Join(kinds, ",") != want {
		t.Fatalf("calls %v, want %s", kinds, want)
	}
	if !res[0].Calls[4].Kind.ReadBack() || res[0].Calls[2].Kind.ReadBack() || res[0].Calls[4].Stats.PromptTokens != 300 {
		t.Fatalf("got %+v", res[0].Calls)
	}
}
