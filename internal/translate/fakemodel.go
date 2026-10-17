package translate

import (
	"context"
	"embed"
	"encoding/json"
	"hash/fnv"
	"strings"
	"sync"
	"time"
)

//go:embed samples/samples.json
var samplesFS embed.FS

// FakeModel answers from translations recorded with the real model for the
// bundled sample videos, so `serve --mock` and the screenshots show real
// output without Ollama. Unknown text comes back marked with the language
// code, which the checks then flag.
type FakeModel struct {
	Delay   time.Duration // base delay per call, to show progress
	once    sync.Once
	samples map[string]map[string]sample // protected source title -> lang -> answer
}

// sample is one recorded answer, with its back-translation and the words the
// model confirmed as added.
type sample struct {
	Output
	Back *Back    `json:"back,omitempty"`
	New  []string `json:"new,omitempty"`
}

// NewFakeModel returns a fake with the given base delay per call.
func NewFakeModel(delay time.Duration) *FakeModel { return &FakeModel{Delay: delay} }

func (f *FakeModel) load() {
	f.once.Do(func() {
		f.samples = map[string]map[string]sample{}
		if b, err := samplesFS.ReadFile("samples/samples.json"); err == nil {
			json.Unmarshal(b, &f.samples)
		}
	})
}

// Translate returns the recorded answer after a short, language-dependent delay.
func (f *FakeModel) Translate(ctx context.Context, req Request) (Output, Stats, error) {
	f.load()
	if f.Delay > 0 {
		h := fnv.New32a()
		h.Write([]byte(req.Lang))
		d := f.Delay + time.Duration(h.Sum32()%5)*f.Delay/4
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return Output{}, Stats{}, ctx.Err()
		}
	}
	if byLang, ok := f.samples[req.Source.Title]; ok {
		if out, ok := byLang[req.Lang]; ok {
			return out.Output, Stats{NumCtx: 8192}, nil
		}
	}
	return Output{
		Title:       "[" + req.Lang + "] " + req.Source.Title,
		Description: req.Source.Description,
	}, Stats{NumCtx: 8192}, nil
}

// find returns the recorded sample whose translated title is title.
func (f *FakeModel) find(lang, title string) (sample, bool) {
	f.load()
	for _, byLang := range f.samples {
		if s, ok := byLang[lang]; ok && s.Title == title {
			return s, true
		}
	}
	return sample{}, false
}

// BackTranslate returns the recorded back-translation, or the text itself.
func (f *FakeModel) BackTranslate(ctx context.Context, req BackRequest) (Back, Stats, error) {
	if s, ok := f.find(req.Lang, req.Title); ok && s.Back != nil {
		return *s.Back, Stats{NumCtx: 8192}, nil
	}
	return Back{Title: req.Title, Line: req.Line}, Stats{NumCtx: 8192}, nil
}

// NewWords returns the words recorded as added for that answer.
func (f *FakeModel) NewWords(ctx context.Context, req NewWordsRequest) ([]string, error) {
	f.load()
	for _, byLang := range f.samples {
		for _, s := range byLang {
			if s.Back != nil && *s.Back == req.Returned {
				return keepListed(s.New, req.Words), nil
			}
		}
	}
	return nil, nil
}

// DetectLanguage reports the bracketed code of a fallback answer, otherwise
// nothing (which never blocks).
func (f *FakeModel) DetectLanguage(ctx context.Context, text string) (string, error) {
	if strings.HasPrefix(text, "[") {
		if i := strings.Index(text, "]"); i > 1 {
			return "en", nil
		}
	}
	return "", nil
}
