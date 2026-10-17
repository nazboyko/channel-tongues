package translate

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nazboyko/channel-tongues/internal/langs"
)

// Request is one translation call to the model.
type Request struct {
	Lang       string
	SourceLang string
	Kind       string
	Source     Output   // protected source fields
	Previous   *Output  // the answer that failed a check, for a retry
	Problem    *Problem // what was wrong with it
}

// Model is the local model, or a fake one in tests and mock mode.
type Model interface {
	Translate(ctx context.Context, req Request) (Output, Stats, error)
	DetectLanguage(ctx context.Context, text string) (string, error)
	BackTranslate(ctx context.Context, req BackRequest) (Back, Stats, error)
	NewWords(ctx context.Context, req NewWordsRequest) ([]string, error)
}

// Status of one language in a job.
type Status string

const (
	StatusWaiting     Status = "waiting"
	StatusTranslating Status = "translating"
	StatusChecking    Status = "checking"
	StatusReady       Status = "ready"
	StatusCheck       Status = "check" // shown as "Check this one"; never published automatically
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
)

// Source is the text to translate.
type Source struct {
	Title       string
	Description string
	Language    string // the video's language, e.g. "en"
}

// Result is the outcome for one language.
type Result struct {
	Lang        string        `json:"lang"`
	Status      Status        `json:"status"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Note        string        `json:"note,omitempty"` // the plain reason for Check this one
	Retries     int           `json:"retries"`
	Duration    time.Duration `json:"duration"`
	Stats       []Stats       `json:"-"`
	Problems    []string      `json:"-"` // every failed check, for measurements
	Raw         Output        `json:"-"` // the last answer with placeholders, for recording samples

	// The back-translation of the title and the first description line, and
	// the words in it that the source does not have.
	BackTitle    string        `json:"backTitle,omitempty"`
	BackLine     string        `json:"backLine,omitempty"`
	Added        []string      `json:"added,omitempty"`      // every marked word
	AddedTitle   []string      `json:"addedTitle,omitempty"` // marked words of the title: these untick the card
	AddedLine    []string      `json:"addedLine,omitempty"`  // marked words of the description only
	Shared       []string      `json:"-"`                    // dropped as the read-back's own wording, for measurements
	BackDuration time.Duration `json:"-"`
	BackFailed   bool          `json:"-"`
	RawBack      Back          `json:"-"` // with placeholders, for recording samples
}

// Event reports progress for one language.
type Event struct {
	Lang   string  `json:"lang"`
	Status Status  `json:"status"`
	Result *Result `json:"result,omitempty"`
}

// Engine runs protect, translate, check and restore for each language.
type Engine struct {
	Model    Model
	Parallel int // languages at once; default 1 (measured: Ollama is no faster with more)
	Retries  int // retries after a failed check; default 2
	// DetectLatin runs the one-word language check for Latin-script targets.
	DetectLatin bool
	// BackTranslate adds a back-translation to every finished language and
	// marks added words, while the next language is being translated.
	BackTranslate bool
	Log           *slog.Logger
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Run translates src into every language and returns the results in the
// order of langs. Progress goes to events (may be nil), which is not closed.
func (e *Engine) Run(ctx context.Context, src Source, targets []string, keep []string, events chan<- Event) []Result {
	src.Title = strings.TrimSpace(strings.ReplaceAll(src.Title, "\r", ""))
	src.Description = strings.ReplaceAll(src.Description, "\r\n", "\n")
	var prot Protected
	protSrc := Output{
		Title:       prot.Protect(src.Title, keep),
		Description: JoinSoftWraps(prot.Protect(src.Description, keep)),
	}
	kind := VideoKind(protSrc.Title, protSrc.Description)

	send := func(ev Event) {
		if ev.Result != nil {
			c := *ev.Result // the receiver gets its own copy: results change after the run
			ev.Result = &c
		}
		if events != nil {
			select {
			case events <- ev:
			case <-ctx.Done():
			}
		}
	}
	for _, l := range targets {
		send(Event{Lang: l, Status: StatusWaiting})
	}

	parallel := e.Parallel
	if parallel <= 0 {
		parallel = 1
	}
	results := make([]Result, len(targets))
	next := make(chan int)
	go func() {
		defer close(next)
		for i := range targets {
			select {
			case next <- i: // in the order asked for, so progress reads top to bottom
			case <-ctx.Done():
				return
			}
		}
	}()
	var wg, backs sync.WaitGroup
	for w := 0; w < min(parallel, len(targets)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				lang := targets[i]
				r := e.one(ctx, src, protSrc, &prot, kind, lang, send)
				results[i] = r
				if e.BackTranslate && (r.Status == StatusReady || r.Status == StatusCheck) {
					// The row stays "Checking" while the back-translation runs
					// next to the next language's translation.
					backs.Add(1)
					go func(i int) {
						defer backs.Done()
						e.back(ctx, src, protSrc, &prot, &results[i])
						send(Event{Lang: results[i].Lang, Status: results[i].Status, Result: &results[i]})
					}(i)
					continue
				}
				send(Event{Lang: lang, Status: r.Status, Result: &results[i]})
			}
		}()
	}
	wg.Wait()
	backs.Wait()
	if e.BackTranslate {
		for _, i := range dropSharedAdditions(results) {
			send(Event{Lang: results[i].Lang, Status: results[i].Status, Result: &results[i]})
		}
	}
	for i, lang := range targets {
		if results[i].Lang == "" { // never started: cancelled before its turn
			results[i] = Result{Lang: lang, Status: StatusCancelled}
			send(Event{Lang: lang, Status: StatusCancelled, Result: &results[i]})
		}
	}
	return results
}

// one runs the pipeline for a single language with up to e.Retries retries.
func (e *Engine) one(ctx context.Context, src Source, protSrc Output, prot *Protected, kind, lang string, send func(Event)) Result {
	start := time.Now()
	res := Result{Lang: lang}
	retries := e.Retries
	if retries <= 0 {
		retries = 2
	}
	req := Request{Lang: lang, SourceLang: src.Language, Kind: kind, Source: protSrc}
	var last Output
	var lastProblem *Problem
	answered := false
	for attempt := 0; attempt <= retries; attempt++ {
		if ctx.Err() != nil {
			res.Status = StatusCancelled
			return res
		}
		send(Event{Lang: lang, Status: StatusTranslating})
		out, st, err := e.Model.Translate(ctx, req)
		res.Stats = append(res.Stats, st)
		e.log().Info("model call", "lang", lang, "attempt", attempt+1, "numCtx", st.NumCtx,
			"promptTokens", st.PromptTokens, "outputTokens", st.OutputTokens, "seconds", st.Total.Seconds())
		if err != nil {
			var bad *BadAnswer
			switch {
			case ctx.Err() != nil:
				res.Status = StatusCancelled
				return res
			case errors.As(err, &bad):
				shape := &Problem{
					Reason:   "the model's answer was not in the expected shape",
					Feedback: `Reply with JSON only, in the shape {"title": "...", "lines": ["..."]}.`,
				}
				if !answered {
					lastProblem = shape // otherwise keep the problem of the answer that will be shown
				}
				res.Problems = append(res.Problems, shape.Reason)
				res.Retries = attempt
				continue
			default:
				e.log().Warn("model error", "lang", lang, "err", err)
				res.Status = StatusFailed
				res.Note = plainModelError(err)
				res.Duration = time.Since(start)
				return res
			}
		}
		answered = true
		send(Event{Lang: lang, Status: StatusChecking})
		out.Title = strings.TrimSpace(Normalize(out.Title))
		out.Description = RepairLines(protSrc.Description, Normalize(out.Description))
		last = out
		problem := Check(protSrc, out, prot.Spans, lang)
		if problem == nil && e.DetectLatin {
			problem = e.detect(ctx, out, lang, src.Language)
		}
		res.Retries = attempt
		if problem == nil {
			lastProblem = nil
			break
		}
		res.Problems = append(res.Problems, problem.Reason)
		lastProblem = problem
		prev := out
		req.Previous = &prev
		req.Problem = problem
	}
	if !answered {
		res.Status = StatusFailed
		res.Note = "the model did not give a usable answer; try again"
		res.Duration = time.Since(start)
		return res
	}
	res.Raw = last
	res.Title = prot.Restore(last.Title)
	res.Description = prot.Restore(last.Description)
	res.Duration = time.Since(start)
	if lastProblem != nil {
		res.Status = StatusCheck
		res.Note = lastProblem.Reason
	} else {
		res.Status = StatusReady
	}
	return res
}

// back adds the back-translation and the added-word test to a finished
// result. A failure here leaves the result as it was: the line is an aid,
// the checks before it already decided.
func (e *Engine) back(ctx context.Context, src Source, protSrc Output, prot *Protected, r *Result) {
	start := time.Now()
	defer func() { r.BackDuration = time.Since(start) }()
	bt, st, err := e.Model.BackTranslate(ctx, BackRequest{
		Lang: r.Lang, SourceLang: src.Language, Title: r.Raw.Title, Line: firstLine(r.Raw.Description),
	})
	e.log().Info("back-translation", "lang", r.Lang, "seconds", st.Total.Seconds(), "err", err)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		// The meaning check did not happen: the safe side, as with every check.
		r.BackFailed = true
		r.Problems = append(r.Problems, "could not be read back")
		if r.Status == StatusReady {
			r.Status = StatusCheck
			r.Note = "it could not be read back to check its meaning; read it yourself"
		}
		return
	}
	r.RawBack = bt
	r.BackTitle = strings.TrimSpace(prot.Restore(bt.Title))
	r.BackLine = strings.TrimSpace(prot.Restore(bt.Line))
	// The word test knows English endings and small words only; for other
	// source languages the line is shown and the person judges it.
	if src.Language != "" && !langs.SameBase(src.Language, "en") {
		return
	}
	original := Back{Title: protSrc.Title, Line: firstLine(protSrc.Description)}
	titleWords, lineWords := fieldCandidates(original, bt)
	candidates := append(append([]string(nil), titleWords...), lineWords...)
	if len(candidates) == 0 {
		return
	}
	// The candidates are judged against the whole source: a word the
	// description says is not new to the video. (A title that borrows a whole
	// part from the description is caught by the title-growth check.)
	added, err := e.Model.NewWords(ctx, NewWordsRequest{
		SourceLang: src.Language, Original: protSrc.Title + "\n" + protSrc.Description, Returned: bt, Words: candidates,
	})
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		added = candidates // the confirming call failed: flag every candidate, the safe side
	}
	r.Added = added
	r.AddedTitle = intersect(added, titleWords)
	r.AddedLine = intersect(added, lineWords)
	if len(added) > 0 {
		r.Problems = append(r.Problems, addedReason(added))
	}
	// Only the title unticks the card: it is what viewers see first and what
	// a wrong word costs most in. Description words stay marked in the line.
	if len(r.AddedTitle) > 0 && r.Status == StatusReady {
		r.Status = StatusCheck
		r.Note = addedReason(r.AddedTitle)
	}
}

// intersect keeps the words of a that are in b, in a's order.
func intersect(a, b []string) []string {
	in := map[string]bool{}
	for _, w := range b {
		in[strings.ToLower(w)] = true
	}
	var out []string
	for _, w := range a {
		if in[strings.ToLower(w)] {
			out = append(out, w)
		}
	}
	return out
}

// dropSharedAdditions removes description words that the read-backs of at
// least half the languages (and at least three) mark: the same English word in
// most read-backs comes from how the model reads back ("created" for "made"),
// not from the translations. Title words stay marked, so a card held back for
// its title is never let through by this rule, and a word another check names
// stays marked too. It returns the results that changed.
func dropSharedAdditions(results []Result) []int {
	n := 0
	counts := map[string]int{}
	for _, r := range results {
		if r.BackTitle == "" {
			continue
		}
		n++
		seen := map[string]bool{}
		for _, w := range r.AddedLine {
			if s := stem(strings.ToLower(w)); !seen[s] {
				seen[s] = true
				counts[s]++
			}
		}
	}
	need := max(3, (n+1)/2)
	shared := map[string]bool{}
	for s, c := range counts {
		if c >= need {
			shared[s] = true
		}
	}
	if len(shared) == 0 {
		return nil
	}
	var changed []int
	for i := range results {
		r := &results[i]
		if len(r.AddedLine) == 0 {
			continue
		}
		otherNote := ""
		if !strings.HasPrefix(r.Note, "adds: ") {
			otherNote = strings.ToLower(r.Note)
		}
		keep := func(words []string) []string {
			var out []string
			for _, w := range words {
				lw := strings.ToLower(w)
				if !shared[stem(lw)] || (otherNote != "" && strings.Contains(otherNote, lw)) {
					out = append(out, w)
				} else if !contains(r.Shared, w) {
					r.Shared = append(r.Shared, w)
				}
			}
			return out
		}
		before := len(r.AddedLine)
		r.AddedLine = keep(r.AddedLine)
		if len(r.AddedLine) == before {
			continue
		}
		r.Added = append(append([]string{}, r.AddedTitle...), r.AddedLine...)
		changed = append(changed, i)
	}
	return changed
}

func contains(list []string, w string) bool {
	for _, x := range list {
		if strings.EqualFold(x, w) {
			return true
		}
	}
	return false
}

func addedReason(words []string) string {
	if len(words) == 0 {
		return ""
	}
	return "adds: " + strings.ToLower(strings.Join(words, ", "))
}

// detect is the one-word language check for Latin-script targets, where the
// script alone cannot tell Spanish from English.
func (e *Engine) detect(ctx context.Context, out Output, lang, sourceLang string) *Problem {
	l, ok := langs.Get(lang)
	if !ok || l.Script != langs.Latin {
		return nil
	}
	text := strings.TrimSpace(stripPlaceholders(out.Title + "\n" + out.Description))
	if countLetters(text) < 12 {
		return nil
	}
	code, err := e.Model.DetectLanguage(ctx, text)
	if err != nil || code == "" {
		return nil // the check is a bonus; never block on it
	}
	if langs.SameBase(code, lang) || sameFamily(code, lang) {
		return nil
	}
	if sourceLang != "" && langs.SameBase(code, sourceLang) {
		return &Problem{
			Reason:   "it looks like it is still in " + langs.EnglishName(sourceLang),
			Feedback: "This is still in " + langs.EnglishName(sourceLang) + ". Translate the title and description into " + l.English + ".",
		}
	}
	name := langs.EnglishName(code)
	if name == code { // a code no list names: say what it is not
		return &Problem{
			Reason:   "it does not look like it is written in " + l.English,
			Feedback: "This is not " + l.English + ". Translate the title and description into " + l.English + ".",
		}
	}
	return &Problem{
		Reason:   "it looks like it is written in " + name + ", not " + l.English,
		Feedback: "This is not " + l.English + ". Translate the title and description into " + l.English + ".",
	}
}

// sameFamily accepts answers the detector often confuses: Malay and
// Indonesian, Norwegian variants, Filipino and Tagalog.
func sameFamily(a, b string) bool {
	groups := [][]string{{"id", "ms"}, {"no", "nb", "nn"}, {"fil", "tl"}, {"hr", "bs", "sr"}}
	in := func(g []string, c string) bool {
		for _, x := range g {
			if langs.SameBase(x, c) {
				return true
			}
		}
		return false
	}
	for _, g := range groups {
		if in(g, a) && in(g, b) {
			return true
		}
	}
	return false
}

// plainModelError is the one sentence a person reads; the raw error goes to the log.
func plainModelError(err error) string {
	if errors.Is(err, ErrModelDown) {
		return "the model stopped answering; start Ollama and try again"
	}
	if msg := err.Error(); strings.Contains(msg, "ollama pull") {
		return msg
	}
	return "the model could not translate this one; try again"
}
