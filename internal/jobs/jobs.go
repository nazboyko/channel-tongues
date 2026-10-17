// Package jobs runs translation jobs in the background and reports progress
// per language to anyone watching.
package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/store"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

// State of a whole job.
const (
	Running   = "running"
	Done      = "done"
	Cancelled = "cancelled"
)

// Meta describes where the text came from.
type Meta struct {
	VideoID      string `json:"videoId,omitempty"` // empty for pasted text
	ChannelID    string `json:"channelId,omitempty"`
	ChannelTitle string `json:"channelTitle,omitempty"`
}

// Lang is one language's state in a job.
type Lang struct {
	Code        string           `json:"code"`
	Name        string           `json:"name"`
	English     string           `json:"english"`
	Status      translate.Status `json:"status"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Note        string           `json:"note,omitempty"`
	Edited      bool             `json:"edited"`
	Seconds     float64          `json:"seconds"`
	Retries     int              `json:"retries"`
	// BackTitle and BackLine read the translation back in the video's
	// language; Added are the words in them the source does not have.
	BackTitle string   `json:"backTitle,omitempty"`
	BackLine  string   `json:"backLine,omitempty"`
	Added     []string `json:"added,omitempty"`
}

// Snapshot is a job as the panel sees it.
type Snapshot struct {
	ID string `json:"id"`
	Meta
	State             string `json:"state"`
	SourceTitle       string `json:"sourceTitle"`
	SourceDescription string `json:"sourceDescription"`
	SourceLanguage    string `json:"sourceLanguage"`
	Languages         []Lang `json:"languages"`
	Ready             int    `json:"ready"`
	ToCheck           int    `json:"toCheck"`
	Failed            int    `json:"failed"`
	Finished          int    `json:"finished"`
}

// Event is sent to watchers: one language changed, or the job ended.
type Event struct {
	Type string `json:"type"` // "lang" or "done"
	Lang *Lang  `json:"lang,omitempty"`
	Job  string `json:"job"`
}

type job struct {
	mu       sync.Mutex
	id       string
	meta     Meta
	source   translate.Source
	order    []string
	langs    map[string]*Lang
	state    string
	cancel   context.CancelFunc
	watchers map[chan Event]struct{}
	results  map[string]translate.Result
}

// Runner keeps jobs in memory and in SQLite.
type Runner struct {
	Store *store.Store
	// Engine returns the engine for a new job (the model may change between jobs).
	Engine func(ctx context.Context) *translate.Engine
	Keep   func() []string
	Log    *slog.Logger

	mu   sync.Mutex
	jobs map[string]*job
}

// ErrNotFound means there is no job with that id.
var ErrNotFound = errors.New("that translation is no longer here; translate again")

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (r *Runner) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.New(slog.DiscardHandler)
}

// Start runs a job in the background and returns its id.
func (r *Runner) Start(src translate.Source, codes []string, meta Meta) string {
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		id: newID(), meta: meta, source: src, order: codes, state: Running, cancel: cancel,
		langs: map[string]*Lang{}, watchers: map[chan Event]struct{}{}, results: map[string]translate.Result{},
	}
	for _, c := range codes {
		j.langs[c] = &Lang{Code: c, Name: langs.Name(c), English: langs.EnglishName(c), Status: translate.StatusWaiting}
	}
	r.mu.Lock()
	if r.jobs == nil {
		r.jobs = map[string]*job{}
	}
	r.jobs[j.id] = j
	r.mu.Unlock()
	r.Store.SaveJob(store.JobRow{
		ID: j.id, VideoID: meta.VideoID, State: Running, SourceLang: src.Language,
		SourceTitle: src.Title, SourceDescription: src.Description, Languages: strings.Join(codes, ","),
	})

	go r.run(ctx, j)
	return j.id
}

func (r *Runner) run(ctx context.Context, j *job) {
	defer j.cancel()
	events := make(chan translate.Event, 16)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			j.mu.Lock()
			l := j.langs[ev.Lang]
			if l == nil {
				j.mu.Unlock()
				continue
			}
			l.Status = ev.Status
			if ev.Result != nil {
				if !l.Edited {
					l.Title = ev.Result.Title
					l.Description = ev.Result.Description
				}
				l.Note = ev.Result.Note
				l.Seconds = ev.Result.Duration.Seconds()
				l.Retries = ev.Result.Retries
				l.BackTitle, l.BackLine, l.Added = ev.Result.BackTitle, ev.Result.BackLine, ev.Result.Added
				j.results[ev.Lang] = *ev.Result
			}
			copyL := *l
			j.broadcast(Event{Type: "lang", Lang: &copyL, Job: j.id})
			j.mu.Unlock()
		}
	}()
	keep := []string(nil)
	if r.Keep != nil {
		keep = r.Keep()
	}
	start := time.Now()
	results := r.Engine(ctx).Run(ctx, j.source, j.order, keep, events)
	close(events)
	<-done

	j.mu.Lock()
	j.state = Done
	if ctx.Err() != nil {
		j.state = Cancelled
	}
	for _, res := range results {
		l := j.langs[res.Lang]
		l.Status, l.Note = res.Status, res.Note
		l.Seconds, l.Retries = res.Duration.Seconds(), res.Retries
		l.BackTitle, l.BackLine, l.Added = res.BackTitle, res.BackLine, res.Added
		if !l.Edited { // the person's own text wins over the model's
			l.Title, l.Description = res.Title, res.Description
		}
		j.results[res.Lang] = res
		r.Store.SaveTranslation(store.TranslationRow{
			JobID: j.id, Lang: res.Lang, Status: string(res.Status), Title: l.Title, Description: l.Description,
			Note: res.Note, Seconds: res.Duration.Seconds(), Retries: res.Retries, Edited: l.Edited,
			BackTitle: res.BackTitle, BackLine: res.BackLine, Added: strings.Join(res.Added, ","),
		})
	}
	r.Store.SaveJob(store.JobRow{ID: j.id, State: j.state})
	j.broadcast(Event{Type: "done", Job: j.id})
	for ch := range j.watchers { // the job is over: end every stream
		close(ch)
		delete(j.watchers, ch)
	}
	j.mu.Unlock()
	r.log().Info("job finished", "job", j.id, "video", j.meta.VideoID, "languages", len(j.order), "state", j.state, "seconds", time.Since(start).Seconds())
}

// broadcast sends to every watcher without blocking; the caller holds j.mu.
func (j *job) broadcast(ev Event) {
	for ch := range j.watchers {
		select {
		case ch <- ev:
		default: // a slow watcher misses an update; it reloads the snapshot
		}
	}
}

func (r *Runner) get(id string) (*job, error) {
	r.mu.Lock()
	j, ok := r.jobs[id]
	r.mu.Unlock()
	if ok {
		return j, nil
	}
	row, rows, found, err := r.Store.LoadJob(id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	j = &job{
		id: row.ID, meta: Meta{VideoID: row.VideoID}, state: row.State,
		source: translate.Source{Title: row.SourceTitle, Description: row.SourceDescription, Language: row.SourceLang},
		langs:  map[string]*Lang{}, watchers: map[chan Event]struct{}{}, results: map[string]translate.Result{},
		cancel: func() {},
	}
	if j.state == Running {
		j.state = Cancelled // the helper restarted in the middle
	}
	for _, c := range strings.Split(row.Languages, ",") {
		if c == "" {
			continue
		}
		j.order = append(j.order, c)
		j.langs[c] = &Lang{Code: c, Name: langs.Name(c), English: langs.EnglishName(c), Status: translate.StatusCancelled}
	}
	for _, t := range rows {
		if l := j.langs[t.Lang]; l != nil {
			l.Status, l.Title, l.Description, l.Note = translate.Status(t.Status), t.Title, t.Description, t.Note
			l.Seconds, l.Retries, l.Edited = t.Seconds, t.Retries, t.Edited
			l.BackTitle, l.BackLine = t.BackTitle, t.BackLine
			if t.Added != "" {
				l.Added = strings.Split(t.Added, ",")
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs == nil {
		r.jobs = map[string]*job{}
	}
	if existing, ok := r.jobs[id]; ok { // loaded by another request meanwhile
		return existing, nil
	}
	r.jobs[id] = j
	return j, nil
}

// Get returns a job's current state.
func (r *Runner) Get(id string) (Snapshot, error) {
	j, err := r.get(id)
	if err != nil {
		return Snapshot{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshot(), nil
}

func (j *job) snapshot() Snapshot {
	s := Snapshot{
		ID: j.id, Meta: j.meta, State: j.state,
		SourceTitle: j.source.Title, SourceDescription: j.source.Description, SourceLanguage: j.source.Language,
	}
	for _, c := range j.order {
		l := *j.langs[c]
		s.Languages = append(s.Languages, l)
		switch l.Status {
		case translate.StatusReady:
			s.Ready++
			s.Finished++
		case translate.StatusCheck:
			s.ToCheck++
			s.Finished++
		case translate.StatusFailed:
			s.Failed++
			s.Finished++
		}
	}
	return s
}

// Watch subscribes to a job's events. The returned snapshot is the state at
// the moment of subscribing; stop must be called when done.
func (r *Runner) Watch(id string) (Snapshot, <-chan Event, func(), error) {
	j, err := r.get(id)
	if err != nil {
		return Snapshot{}, nil, nil, err
	}
	ch := make(chan Event, 64)
	j.mu.Lock()
	snap := j.snapshot()
	if j.state == Running {
		j.watchers[ch] = struct{}{}
	} else {
		close(ch)
	}
	j.mu.Unlock()
	stop := func() {
		j.mu.Lock()
		if _, ok := j.watchers[ch]; ok {
			delete(j.watchers, ch)
		}
		j.mu.Unlock()
	}
	return snap, ch, stop, nil
}

// Cancel stops a running job.
func (r *Runner) Cancel(id string) error {
	j, err := r.get(id)
	if err != nil {
		return err
	}
	j.cancel()
	return nil
}

// Edit stores the person's own version of one language. Basic limits are
// checked here; YouTube checks them again on publish.
func (r *Runner) Edit(id, code, title, description string) (Lang, error) {
	j, err := r.get(id)
	if err != nil {
		return Lang{}, err
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	l := j.langs[code]
	if l == nil {
		return Lang{}, errors.New("that language is not part of this translation")
	}
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return Lang{}, errors.New("the title cannot be empty")
	case utf8.RuneCountInString(title) > translate.MaxTitle:
		return Lang{}, errors.New("the title is longer than 100 characters")
	case len(description) > translate.MaxDescription:
		return Lang{}, errors.New("the description is too long for YouTube")
	case strings.ContainsAny(title+description, "<>"):
		return Lang{}, errors.New("YouTube does not allow < or > in titles and descriptions")
	}
	l.Title, l.Description, l.Edited = title, description, true
	r.Store.SaveTranslation(store.TranslationRow{
		JobID: j.id, Lang: code, Status: string(l.Status), Title: title, Description: description,
		Note: l.Note, Seconds: l.Seconds, Retries: l.Retries, Edited: true,
		BackTitle: l.BackTitle, BackLine: l.BackLine, Added: strings.Join(l.Added, ","),
	})
	return *l, nil
}

// Results returns the finished languages of a job for publishing or saving.
func (r *Runner) Results(id string) (Snapshot, error) {
	return r.Get(id)
}
