package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

// translateFlags are shared by translate, publish, save and channel localize.
type translateFlags struct {
	langs     *string
	mock      *bool
	model     *string
	title     *string
	descFile  *string
	full      *bool
	measure   *bool
	cold      *bool
	noDetect  *bool
	parallel  *int
	fakeModel *bool
	record    *string
	noBack    *bool
}

func addTranslateFlags(fs *flag.FlagSet) *translateFlags {
	return &translateFlags{
		langs:     fs.String("langs", "popular", "popular, used, or a list such as es,pt-BR,ja"),
		mock:      fs.Bool("mock", false, "use the bundled sample videos instead of YouTube"),
		model:     fs.String("model", "", "Ollama model tag (default: gemma4:26b when downloaded, else gemma4:e4b)"),
		title:     fs.String("title", "", "translate this title instead of a video's"),
		descFile:  fs.String("description-file", "", "file with the description to translate"),
		full:      fs.Bool("full", false, "print every title and description in full"),
		measure:   fs.Bool("measure", false, "print timing lines for the notes"),
		cold:      fs.Bool("cold", false, "with --measure: unload the model first to time a cold start"),
		noDetect:  fs.Bool("no-detect", false, "skip the one-word language check for Latin-script languages"),
		parallel:  fs.Int("parallel", 1, "languages at once"),
		fakeModel: fs.Bool("fake-model", false, "use the built-in sample translations instead of Ollama"),
		record:    fs.String("record-samples", "", "add the answers to a sample file for the fake model (development)"),
		noBack:    fs.Bool("no-back", false, "skip the back-translation that checks the meaning"),
	}
}

// source is what will be translated, from a video or from text.
type source struct {
	video *youtube.Video // nil for pasted text
	text  translate.Source
}

func (a *app) loadSource(ctx context.Context, f *translateFlags, pos []string) (*source, error) {
	if *f.title != "" {
		desc := ""
		if *f.descFile != "" {
			b, err := os.ReadFile(*f.descFile)
			if err != nil {
				return nil, err
			}
			desc = strings.TrimRight(string(b), "\n")
		}
		return &source{text: translate.Source{Title: *f.title, Description: desc, Language: "en"}}, nil
	}
	if len(pos) == 0 {
		return nil, errors.New("which video? Give a video id or link, or --title and --description-file")
	}
	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return nil, err
	}
	v, _, err := a.GetVideo(ctx, id)
	if err != nil {
		return nil, friendly(err)
	}
	return &source{video: v, text: translate.Source{Title: v.Title, Description: v.Description, Language: v.DefaultLanguage}}, nil
}

// pickLanguages turns --langs into codes, leaving out the source language.
func (a *app) pickLanguages(spec, sourceLang, channelID string) ([]string, error) {
	var codes []string
	switch strings.TrimSpace(spec) {
	case "", "popular":
		codes = append(codes, langs.Popular...)
	case "used":
		if saved, ok, _ := a.Store.Setting("lastLanguages:" + channelID); ok && saved != "" {
			codes = strings.Split(saved, ",")
		} else {
			codes = append(codes, langs.Popular...)
		}
	default:
		for _, c := range strings.Split(spec, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			l, ok := langs.Get(c)
			if !ok {
				return nil, fmt.Errorf("%q is not a language code this tool knows; run tongues languages to see them", c)
			}
			codes = append(codes, l.Code)
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] || (sourceLang != "" && langs.SameBase(c, sourceLang)) {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, errors.New("no languages left to translate into")
	}
	return out, nil
}

// engine builds the translation engine for this run.
func (a *app) engine(ctx context.Context, f *translateFlags) (*translate.Engine, *translate.Ollama) {
	if *f.fakeModel {
		return &translate.Engine{Model: translate.NewFakeModel(0), Parallel: *f.parallel, DetectLatin: true, BackTranslate: !*f.noBack, Log: a.Log}, nil
	}
	o := a.Ollama(ctx, *f.model)
	return &translate.Engine{Model: o, Parallel: *f.parallel, DetectLatin: !*f.noDetect, BackTranslate: !*f.noBack, Log: a.Log}, o
}

// checkModel stops early with the one command that fixes the model.
func checkModel(ctx context.Context, o *translate.Ollama) error {
	if o == nil {
		return nil
	}
	switch state, _ := o.Health(ctx); state {
	case translate.ModelDown:
		return errors.New("Ollama is not running. Start the Ollama app, or run: ollama serve")
	case translate.ModelMissing:
		return fmt.Errorf("the model is not downloaded yet. Run: ollama pull %s", o.Model)
	}
	return nil
}

func runTranslate(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("translate", flag.ContinueOnError)
	f := addTranslateFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	a, err := newApp(cfg, *f.mock)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	src, err := a.loadSource(ctx, f, pos)
	if err != nil {
		return err
	}
	channelID := ""
	if src.video != nil {
		channelID = src.video.ChannelID
	}
	targets, err := a.pickLanguages(*f.langs, src.text.Language, channelID)
	if err != nil {
		return err
	}
	eng, o := a.engine(ctx, f)
	if err := checkModel(ctx, o); err != nil {
		return err
	}
	if *f.cold && o != nil {
		if err := o.Unload(ctx); err != nil {
			return fmt.Errorf("could not unload the model for a cold start: %w", err)
		}
		time.Sleep(time.Second)
	}
	keep, err := a.Store.KeepWords()
	if err != nil {
		return err
	}

	using := "sample answers"
	if o != nil {
		using = o.Model
	}
	fmt.Printf("Translating into %s with %s: %s\n\n", plural(len(targets), "language"), using, strings.Join(targets, " "))
	start := time.Now()
	results := eng.Run(ctx, src.text, targets, keep, nil)
	wall := time.Since(start)
	printResults(results, *f.full)
	if *f.measure {
		printMeasure(results, wall, eng, *f.cold)
	}
	if *f.record != "" {
		return recordSamples(*f.record, src.text, keep, results)
	}
	return nil
}

// recordSamples stores ready answers, with placeholders, keyed by the
// protected source title, for the fake model used by serve --mock.
func recordSamples(path string, src translate.Source, keep []string, results []translate.Result) error {
	type sample struct {
		translate.Output
		Back *translate.Back `json:"back,omitempty"`
		New  []string        `json:"new,omitempty"`
	}
	samples := map[string]map[string]sample{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &samples); err != nil {
			return err
		}
	}
	var p translate.Protected
	key := p.Protect(src.Title, keep)
	if samples[key] == nil {
		samples[key] = map[string]sample{}
	}
	n := 0
	for _, r := range results {
		if r.Status == translate.StatusReady || len(r.Added) > 0 {
			e := sample{Output: r.Raw, New: r.Added}
			if r.RawBack != (translate.Back{}) {
				b := r.RawBack
				e.Back = &b
			}
			samples[key][r.Lang] = e
			n++
		}
	}
	b, err := json.MarshalIndent(samples, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("Recorded %d answers in %s.\n", n, path)
	return nil
}

func statusWord(s translate.Status) string {
	switch s {
	case translate.StatusReady:
		return "ready"
	case translate.StatusCheck:
		return "check this one"
	case translate.StatusFailed:
		return "failed"
	case translate.StatusCancelled:
		return "cancelled"
	}
	return string(s)
}

func printResults(results []translate.Result, full bool) {
	ready, check := 0, 0
	for _, r := range results {
		switch r.Status {
		case translate.StatusReady:
			ready++
		case translate.StatusCheck:
			check++
		}
		back := ""
		if r.BackTitle != "" {
			back = "reads as: " + truncate(markAdded(r.BackTitle, r.Added), 46)
		}
		fmt.Printf("  %s %s %s %5.1fs  %s  %s\n", pad(langs.Name(r.Lang), 18), pad(r.Lang, 7), pad(statusWord(r.Status), 14), r.Duration.Seconds(), pad(truncate(r.Title, 44), 44), back)
		if r.Note != "" {
			fmt.Printf("  %s %s %s %s\n", pad("", 18), pad("", 7), pad("", 14), "  "+r.Note)
		}
	}
	fmt.Printf("\n%d ready, %d to check.\n", ready, check)
	if full {
		for _, r := range results {
			fmt.Printf("\n=== %s (%s), %s\n%s\n\n%s\n", langs.EnglishName(r.Lang), r.Lang, statusWord(r.Status), r.Title, r.Description)
			if r.BackTitle != "" {
				fmt.Printf("\nReads in English as: %s / %s\n", markAdded(r.BackTitle, r.Added), markAdded(r.BackLine, r.Added))
			}
		}
	}
}

// markAdded puts [brackets] around the added words, for the terminal.
func markAdded(text string, added []string) string {
	for _, w := range added {
		re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(w) + `\b`)
		if err == nil {
			text = re.ReplaceAllString(text, "[$0]")
		}
	}
	return text
}

// printMeasure prints lines ready to paste into the notes.
func printMeasure(results []translate.Result, wall time.Duration, eng *translate.Engine, cold bool) {
	var secs []float64
	retries, check, calls := 0, 0, 0
	var load time.Duration
	maxPrompt, numCtx := 0, 0
	problems := map[string]int{}
	for _, r := range results {
		secs = append(secs, r.Duration.Seconds())
		retries += r.Retries
		if r.Status == translate.StatusCheck {
			check++
		}
		for _, st := range r.Stats {
			calls++
			load = max(load, st.Load)
			maxPrompt = max(maxPrompt, st.PromptTokens+st.OutputTokens)
			numCtx = max(numCtx, st.NumCtx)
		}
		for _, p := range r.Problems {
			problems[p]++
		}
	}
	sort.Float64s(secs)
	sum := 0.0
	for _, s := range secs {
		sum += s
	}
	mode := "warm"
	if cold {
		mode = "cold"
	}
	fmt.Println()
	fmt.Printf("measure: %s, %d languages, parallel %d, wall %.1fs, per language mean %.1fs median %.1fs max %.1fs\n",
		mode, len(results), eng.Parallel, wall.Seconds(), sum/float64(len(secs)), secs[len(secs)/2], secs[len(secs)-1])
	fmt.Printf("measure: model calls %d, retries %d, check-this-one %d of %d, longest model load %.1fs, largest prompt+answer %d tokens of num_ctx %d\n",
		calls, retries, check, len(results), load.Seconds(), maxPrompt, numCtx)
	for p, n := range problems {
		fmt.Printf("measure: failed check x%d: %s\n", n, p)
	}
	var backSum, backMax float64
	backs, flagged := 0, 0
	for _, r := range results {
		if r.BackTitle == "" {
			continue
		}
		backs++
		s := r.BackDuration.Seconds()
		backSum += s
		backMax = max(backMax, s)
		if len(r.Added) > 0 {
			flagged++
			fmt.Printf("measure: back-translation %s adds %s: %s\n", r.Lang, strings.Join(r.Added, ", "), markAdded(r.BackTitle, r.Added)+" / "+markAdded(r.BackLine, r.Added))
		}
	}
	if backs > 0 {
		fmt.Printf("measure: back-translation on %d languages, extra %.2fs mean, %.2fs max per language (runs next to the next language), %d flagged\n", backs, backSum/float64(backs), backMax, flagged)
	}
}

// plural renders "1 language" or "3 languages".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
