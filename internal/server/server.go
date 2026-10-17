// Package server is the helper's HTTP API for the side panel. It listens on
// 127.0.0.1 only, answers only the extension's origin and requires the
// pairing code on every request.
package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nazboyko/channel-tongues/internal/core"
	"github.com/nazboyko/channel-tongues/internal/jobs"
	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

// ExtensionID is fixed by the "key" in the extension's manifest.
const ExtensionID = "cjbeephejaicfaipmjbingogeihdlggd"

// Version of the helper, reported by /health.
const Version = "0.1.0"

// Server holds the routes and their state.
type Server struct {
	Svc     *core.Service
	Runner  *jobs.Runner
	Origin  string // the only browser origin allowed
	Pairing string
	Port    int

	mu       sync.Mutex
	failures []time.Time
}

// New builds a server around a service.
func New(svc *core.Service, pairing string, port int) *Server {
	s := &Server{
		Svc:     svc,
		Origin:  "chrome-extension://" + ExtensionID,
		Pairing: pairing,
		Port:    port,
	}
	s.Runner = &jobs.Runner{
		Store: svc.Store,
		Engine: func(ctx context.Context) *translate.Engine {
			return &translate.Engine{Model: svc.Model(ctx), Parallel: 1, DetectLatin: true, BackTranslate: true, Log: svc.Log}
		},
		Keep: svc.KeepWords,
		Log:  svc.Log,
	}
	return s
}

// LoadPairing reads the pairing code from dir/pairing.txt, creating one the
// first time. The code is short because a person types it once.
func LoadPairing(dir string) (string, error) {
	path := filepath.Join(dir, "pairing.txt")
	if b, err := os.ReadFile(path); err == nil {
		if code := normalizeCode(string(b)); len(code) == 8 {
			return formatCode(code), nil
		}
	}
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no I, L, O, 0 or 1
	var b strings.Builder
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	code := formatCode(b.String())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(code+"\n"), 0o600); err != nil {
		return "", err
	}
	return code, nil
}

func normalizeCode(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatCode(c string) string {
	if len(c) == 8 {
		return c[:4] + "-" + c[4:]
	}
	return c
}

// Handler returns the full HTTP handler with the security checks in front.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /video/{id}", s.video)
	mux.HandleFunc("GET /channel/videos", s.channelVideos)
	mux.HandleFunc("POST /jobs", s.startJob)
	mux.HandleFunc("GET /jobs/{id}", s.getJob)
	mux.HandleFunc("GET /jobs/{id}/events", s.jobEvents)
	mux.HandleFunc("POST /jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("PUT /jobs/{id}/translations", s.editJob)
	mux.HandleFunc("POST /jobs/{id}/publish", s.publish)
	mux.HandleFunc("POST /jobs/{id}/save", s.save)
	mux.HandleFunc("POST /open-folder", s.openFolder)
	mux.HandleFunc("POST /videos/{id}/restore", s.restore)
	mux.HandleFunc("GET /settings", s.getSettings)
	mux.HandleFunc("PUT /settings", s.putSettings)
	mux.HandleFunc("GET /languages", s.languages)
	mux.HandleFunc("GET /auth/url", s.authURL)
	mux.HandleFunc("POST /auth/signout", s.signOut)
	if s.Svc.Mock {
		mux.HandleFunc("POST /mock/state", s.mockState)
	}
	return s.guard(mux)
}

// guard enforces the Host header, the origin and the pairing code.
func (s *Server) guard(next http.Handler) http.Handler {
	allowedHosts := map[string]bool{
		fmt.Sprintf("127.0.0.1:%d", s.Port): true,
		fmt.Sprintf("localhost:%d", s.Port): true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedHosts[r.Host] {
			writeErr(w, http.StatusForbidden, "host", "This helper only answers on 127.0.0.1.")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" {
			if origin != s.Origin {
				writeErr(w, http.StatusForbidden, "origin", "Only the Channel Tongues extension may call this helper.")
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", s.Origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		given := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(normalizeCode(given)), []byte(normalizeCode(s.Pairing))) == 1 {
			next.ServeHTTP(w, r) // the right code always gets in
			return
		}
		if given != "" {
			if s.blocked() {
				writeErr(w, http.StatusTooManyRequests, "pair", "Too many wrong codes. Wait a minute, then enter the code the helper printed.")
				return
			}
			s.fail() // only wrong codes count, so a page firing bare requests cannot lock the panel out
		}
		writeErr(w, http.StatusUnauthorized, "pair", "Enter the code the helper printed.")
	})
}

func (s *Server) blocked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	cut := time.Now().Add(-time.Minute)
	kept := s.failures[:0]
	for _, t := range s.failures {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	s.failures = kept
	return len(s.failures) >= 20
}

func (s *Server) fail() {
	s.mu.Lock()
	s.failures = append(s.failures, time.Now())
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

// writeErr sends {"error": sentence, "code": machine word}. The sentence is
// what the panel shows; it always says what to do.
func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("the request was not understood")
	}
	return nil
}

// apiFailure maps service errors to a status, a code and a sentence.
func (s *Server) apiFailure(w http.ResponseWriter, err error) {
	var ae *youtube.APIError
	switch {
	case errors.Is(err, youtube.ErrNotSignedIn):
		writeErr(w, http.StatusConflict, "signin", "Sign in so Channel Tongues can see your videos.")
	case errors.Is(err, youtube.ErrSignInExpired):
		writeErr(w, http.StatusConflict, "signin", "Your sign-in has expired. Sign in again.")
	case errors.Is(err, youtube.ErrVideoNotFound):
		writeErr(w, http.StatusNotFound, "notfound", "This video was not found for your signed-in channels.")
	case errors.Is(err, youtube.ErrNoDefaultLanguage):
		writeErr(w, http.StatusConflict, "nolanguage", "Set this video's language first: in Studio, Details > Show more > Title and description language.")
	case errors.Is(err, jobs.ErrNotFound):
		writeErr(w, http.StatusNotFound, "nojob", "That translation is no longer here. Translate again.")
	case errors.Is(err, youtube.ErrVideoChanged):
		writeErr(w, http.StatusConflict, "changed", "The video's translations changed while you were working. Nothing was written; press Publish again.")
	case errors.Is(err, core.ErrNotOwner):
		writeErr(w, http.StatusConflict, "owner", "Sign in with the channel that owns this video: open Keep these words below, then Add another channel.")
	case errors.As(err, &ae) && ae.Code == http.StatusUnauthorized:
		writeErr(w, http.StatusConflict, "signin", "Your sign-in has expired. Sign in again.")
	case errors.As(err, &ae):
		writeErr(w, http.StatusBadGateway, "youtube", ae.Msg)
	default:
		s.Svc.Log.Warn("request failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "error", "Something went wrong. Try again; the details are in the helper's log.")
	}
}

func sentence(msg string) string {
	if msg == "" {
		return "Something went wrong. Try again."
	}
	r, size := utf8.DecodeRuneInString(msg)
	msg = string(unicode.ToUpper(r)) + msg[size:]
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	return msg
}

type healthResponse struct {
	OK         bool              `json:"ok"`
	Version    string            `json:"version"`
	Mock       bool              `json:"mock"`
	Model      core.ModelStatus  `json:"model"`
	SignedIn   bool              `json:"signedIn"`
	CanSignIn  bool              `json:"canSignIn"`
	Channels   []youtube.Channel `json:"channels"`
	KeepWords  []string          `json:"keepWords"`
	UsedToday  int               `json:"usedToday"`
	VideosLeft int               `json:"videosLeftToday"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	chans, _ := s.Svc.Channels()
	ledger := &youtube.Ledger{Store: s.Svc.Store}
	used, _ := ledger.UsedToday()
	left, _ := ledger.VideosLeftToday()
	keep := s.Svc.KeepWords()
	if keep == nil {
		keep = []string{}
	}
	if chans == nil {
		chans = []youtube.Channel{}
	}
	writeJSON(w, http.StatusOK, healthResponse{
		OK: true, Version: Version, Mock: s.Svc.Mock,
		Model:     s.Svc.ModelHealth(r.Context()),
		SignedIn:  len(chans) > 0,
		CanSignIn: s.Svc.Mock || s.Svc.Auth.HasClientFile(),
		Channels:  chans, KeepWords: keep, UsedToday: used, VideosLeft: left,
	})
}

type langRef struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type videoResponse struct {
	ID              string    `json:"id"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	DefaultLanguage string    `json:"defaultLanguage"`
	SourceName      string    `json:"sourceName"`
	ChannelID       string    `json:"channelId"`
	ChannelTitle    string    `json:"channelTitle"`
	Languages       []langRef `json:"languages"`
	Shown           []string  `json:"shown"`     // chips: the popular twelve plus the rest of the last pick
	Suggested       []string  `json:"suggested"` // ticked at first: the last pick (see core.UsedLanguages)
	StudioURL       string    `json:"studioUrl"`
}

func (s *Server) video(w http.ResponseWriter, r *http.Request) {
	id, ok := youtube.ParseVideoID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "badvideo", "That does not look like a video link.")
		return
	}
	v, _, err := s.Svc.GetVideo(r.Context(), id)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	resp := videoResponse{
		ID: v.ID, Title: v.Title, Description: v.Description, DefaultLanguage: v.DefaultLanguage,
		SourceName: langs.EnglishName(v.DefaultLanguage), ChannelID: v.ChannelID, ChannelTitle: v.ChannelTitle,
		Languages: []langRef{}, StudioURL: "https://studio.youtube.com/video/" + v.ID + "/translations",
	}
	for _, code := range v.Languages() {
		c := langs.Canonical(code) // pt and pt-BR are the same language to the panel
		resp.Languages = append(resp.Languages, langRef{Code: c, Name: langs.Name(c)})
	}
	// Ticked: the languages picked last time. Shown: the popular twelve, so
	// one left out is a tap away, then the rest of that pick.
	resp.Suggested = []string{}
	resp.Shown = []string{}
	for _, c := range s.Svc.UsedLanguages(v.ChannelID) {
		if !langs.SameBase(c, v.DefaultLanguage) {
			resp.Suggested = append(resp.Suggested, c)
		}
	}
	seen := map[string]bool{}
	for _, c := range append(append([]string(nil), langs.Popular...), resp.Suggested...) {
		if !seen[c] && !langs.SameBase(c, v.DefaultLanguage) {
			seen[c] = true
			resp.Shown = append(resp.Shown, c)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

type uploadItem struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	ChannelTitle string `json:"channelTitle"`
	Languages    int    `json:"languages"`
	HasLanguage  bool   `json:"hasLanguage"`
	PublishedAt  string `json:"publishedAt"`
}

func (s *Server) channelVideos(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	chans, err := s.Svc.Channels()
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	if len(chans) == 0 {
		s.apiFailure(w, youtube.ErrNotSignedIn)
		return
	}
	items := []uploadItem{}
	for _, ch := range chans {
		client, err := s.Svc.ClientFor(r.Context(), ch.ID)
		if err != nil {
			s.apiFailure(w, err)
			return
		}
		vids, err := client.ListUploads(r.Context(), ch.ID, limit)
		if err != nil {
			s.apiFailure(w, err)
			return
		}
		for _, v := range vids {
			items = append(items, uploadItem{
				ID: v.ID, Title: v.Title, ChannelTitle: v.ChannelTitle, Languages: len(v.Languages()),
				HasLanguage: v.DefaultLanguage != "", PublishedAt: v.PublishedAt,
			})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].PublishedAt > items[j].PublishedAt })
	if len(items) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, map[string]any{"videos": items})
}

type jobRequest struct {
	VideoID        string   `json:"videoId"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	SourceLanguage string   `json:"sourceLanguage"`
	Languages      []string `json:"languages"`
}

func (s *Server) startJob(w http.ResponseWriter, r *http.Request) {
	var req jobRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	var src translate.Source
	var meta jobs.Meta
	if req.VideoID != "" {
		id, ok := youtube.ParseVideoID(req.VideoID)
		if !ok {
			writeErr(w, http.StatusBadRequest, "badvideo", "That does not look like a video link.")
			return
		}
		v, _, err := s.Svc.GetVideo(r.Context(), id)
		if err != nil {
			s.apiFailure(w, err)
			return
		}
		src = translate.Source{Title: v.Title, Description: v.Description, Language: v.DefaultLanguage}
		meta = jobs.Meta{VideoID: v.ID, ChannelID: v.ChannelID, ChannelTitle: v.ChannelTitle}
	} else {
		if strings.TrimSpace(req.Title) == "" {
			writeErr(w, http.StatusBadRequest, "notitle", "Paste the title first.")
			return
		}
		src = translate.Source{Title: req.Title, Description: req.Description, Language: req.SourceLanguage}
		if src.Language == "" {
			src.Language = s.detectSource(r.Context(), req.Title+"\n"+req.Description)
		}
	}
	var codes []string
	seen := map[string]bool{}
	for _, c := range req.Languages {
		l, ok := langs.Get(c)
		if !ok || seen[l.Code] || (src.Language != "" && langs.SameBase(l.Code, src.Language)) {
			continue
		}
		seen[l.Code] = true
		codes = append(codes, l.Code)
	}
	if len(codes) == 0 {
		writeErr(w, http.StatusBadRequest, "nolangs", "Pick at least one language.")
		return
	}
	s.Svc.RememberLanguages(meta.ChannelID, codes)
	id := s.Runner.Start(src, codes, meta)
	writeJSON(w, http.StatusAccepted, map[string]string{"jobId": id})
}

// detectSource asks the model which language pasted text is in; English when unsure.
func (s *Server) detectSource(ctx context.Context, text string) string {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	code, err := s.Svc.Model(ctx).DetectLanguage(ctx, text)
	if err != nil || code == "" {
		return "en"
	}
	if l, ok := langs.Get(code); ok {
		return l.Code
	}
	return "en"
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Runner.Get(r.PathValue("id"))
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// jobEvents streams progress as server-sent events: a snapshot first, then
// one event per language change, then done.
func (s *Server) jobEvents(w http.ResponseWriter, r *http.Request) {
	snap, events, stop, err := s.Runner.Watch(r.PathValue("id"))
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	defer stop()
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "error", "Streaming is not available.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	send := func(event string, v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		flusher.Flush()
	}
	send("snapshot", snap)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case ev, open := <-events:
			if !open {
				final, _ := s.Runner.Get(snap.ID)
				send("done", final)
				return
			}
			if ev.Type == "done" {
				final, _ := s.Runner.Get(snap.ID)
				send("done", final)
				return
			}
			send("lang", ev.Lang)
		case <-tick.C:
			fmt.Fprint(w, ": still working\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	if err := s.Runner.Cancel(r.PathValue("id")); err != nil {
		s.apiFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type editRequest struct {
	Lang        string `json:"lang"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (s *Server) editJob(w http.ResponseWriter, r *http.Request) {
	var req editRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	l, err := s.Runner.Edit(r.PathValue("id"), req.Lang, req.Title, req.Description)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			s.apiFailure(w, err)
			return
		}
		writeErr(w, http.StatusBadRequest, "edit", sentence(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, l)
}

type publishRequest struct {
	Languages []string `json:"languages"`
	Replace   []string `json:"replace"`
}

type publishLine struct {
	Lang    string          `json:"lang"`
	Name    string          `json:"name"`
	Outcome youtube.Outcome `json:"outcome"`
}

type publishResponse struct {
	BackupID  string        `json:"backupId"`
	VideoID   string        `json:"videoId"`
	Results   []publishLine `json:"results"`
	Published int           `json:"published"`
	Kept      int           `json:"kept"`
	Failed    int           `json:"failed"`
	StudioURL string        `json:"studioUrl"`
}

func (s *Server) publish(w http.ResponseWriter, r *http.Request) {
	var req publishRequest
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	snap, err := s.Runner.Get(r.PathValue("id"))
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	if snap.VideoID == "" {
		writeErr(w, http.StatusBadRequest, "novideo", "This text is not from a video. Copy it or save it to a folder instead.")
		return
	}
	want := map[string]bool{}
	for _, c := range req.Languages {
		want[c] = true
	}
	ours := map[string]youtube.Localization{}
	for _, l := range snap.Languages {
		if !want[l.Code] {
			continue
		}
		// Only what the person chose and what has text; failed ones never.
		if (l.Status == translate.StatusReady || l.Status == translate.StatusCheck) && strings.TrimSpace(l.Title) != "" {
			ours[l.Code] = youtube.Localization{Title: l.Title, Description: l.Description}
		}
	}
	if len(ours) == 0 {
		writeErr(w, http.StatusBadRequest, "nolangs", "Tick at least one language to publish.")
		return
	}
	replace := map[string]bool{}
	for _, c := range req.Replace {
		replace[c] = true
	}
	ctx := r.Context()
	// The same job twice (a double click) is refused; another job's publish to
	// this video waits for this one, so neither deletes the other's languages.
	again, ok := s.Svc.TryLock("publish:" + snap.ID)
	if !ok {
		writeBusy(w)
		return
	}
	defer again()
	unlock := s.Svc.LockVideo(snap.VideoID)
	defer unlock()
	v, _, err := s.Svc.GetVideo(ctx, snap.VideoID)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	writer, err := s.Svc.Writer(ctx, v)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	v, plan, err := writer.PrepareFrom(v, ours, replace)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	res, err := writer.Publish(ctx, v, plan, snap.ID)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPublishResponse(res))
}

func toPublishResponse(res *youtube.PublishResult) publishResponse {
	out := publishResponse{BackupID: res.BackupID, VideoID: res.VideoID, Results: []publishLine{},
		StudioURL: "https://studio.youtube.com/video/" + res.VideoID + "/translations"}
	for _, c := range res.Changes {
		out.Results = append(out.Results, publishLine{Lang: c.Lang, Name: langs.Name(c.Lang), Outcome: c.Outcome})
		switch c.Outcome {
		case youtube.OutcomeAdded, youtube.OutcomeUpdated, youtube.OutcomeReplaced, youtube.OutcomeSame:
			out.Published++
		case youtube.OutcomeKept:
			out.Kept++
		case youtube.OutcomeFailed:
			out.Failed++
		}
	}
	return out
}

type restoreRequest struct {
	BackupID string `json:"backupId"`
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	var req restoreRequest
	if err := readJSON(r, &req); err != nil || req.BackupID == "" {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	id, ok := youtube.ParseVideoID(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusBadRequest, "badvideo", "That does not look like a video link.")
		return
	}
	ctx := r.Context()
	again, ok := s.Svc.TryLock("restore:" + id + ":" + req.BackupID)
	if !ok {
		writeBusy(w)
		return
	}
	defer again()
	unlock := s.Svc.LockVideo(id)
	defer unlock()
	v, _, err := s.Svc.GetVideo(ctx, id)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	writer, err := s.Svc.Writer(ctx, v)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	res, err := writer.Restore(ctx, id, req.BackupID)
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toPublishResponse(res))
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Runner.Get(r.PathValue("id"))
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	var results []translate.Result
	for _, l := range snap.Languages {
		if strings.TrimSpace(l.Title) == "" {
			continue
		}
		results = append(results, translate.Result{Lang: l.Code, Status: l.Status, Title: l.Title, Description: l.Description, Note: l.Note})
	}
	if len(results) == 0 {
		writeErr(w, http.StatusBadRequest, "empty", "Nothing is translated yet.")
		return
	}
	dir, err := s.Svc.Save(core.SaveRequest{Channel: snap.ChannelTitle, VideoID: snap.VideoID, SourceTitle: snap.SourceTitle, Results: results})
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	if err := s.Svc.OpenFolder(dir); err != nil {
		s.Svc.Log.Warn("open folder", "err", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"folder": dir})
}

func (s *Server) openFolder(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Folder string `json:"folder"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	if err := s.Svc.OpenFolder(req.Folder); err != nil {
		writeErr(w, http.StatusBadRequest, "folder", sentence(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type settingsBody struct {
	KeepWords []string `json:"keepWords"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	keep := s.Svc.KeepWords()
	if keep == nil {
		keep = []string{}
	}
	writeJSON(w, http.StatusOK, settingsBody{KeepWords: keep})
}

// putSettings replaces the keep words with the list given.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsBody
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	want := map[string]bool{}
	for _, word := range req.KeepWords {
		if word = strings.TrimSpace(word); word != "" {
			want[strings.ToLower(word)] = true
			s.Svc.Store.AddKeepWord(word)
		}
	}
	for _, word := range s.Svc.KeepWords() {
		if !want[strings.ToLower(word)] {
			s.Svc.Store.RemoveKeepWord(word)
		}
	}
	s.getSettings(w, r)
}

func (s *Server) languages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"languages": langs.All(r.URL.Query().Get("source")), "popular": langs.Popular,
		"used": s.Svc.UsedLanguages(""), // for pasted text, which has no channel
	})
}

// authURL starts a sign-in and returns the Google page to open. The helper
// finishes it in the background; /health then lists the new channel.
func (s *Server) authURL(w http.ResponseWriter, r *http.Request) {
	if s.Svc.Mock {
		sw := s.Svc.MockSwitches()
		sw.Auth = ""
		s.Svc.SetMock(sw)
		writeJSON(w, http.StatusOK, map[string]string{"url": "about:blank"})
		return
	}
	if !s.Svc.Auth.HasClientFile() {
		writeErr(w, http.StatusConflict, "setup", "Run tongues setup in a terminal first; it walks you through connecting your Google project.")
		return
	}
	login, err := s.Svc.Auth.StartLogin(context.Background())
	if err != nil {
		s.apiFailure(w, err)
		return
	}
	go func() {
		ch, err := login.Wait(context.Background())
		if err != nil {
			s.Svc.Log.Info("sign-in did not finish", "err", err)
			return
		}
		s.Svc.SignedIn(ch)
		s.Svc.Log.Info("signed in", "channel", ch.ID)
	}()
	writeJSON(w, http.StatusOK, map[string]string{"url": login.URL})
}

func (s *Server) signOut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ChannelID string `json:"channelId"`
	}
	if err := readJSON(r, &req); err != nil || req.ChannelID == "" {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	unlock, ok := s.Svc.TryLock("signout:" + req.ChannelID)
	if !ok {
		writeBusy(w)
		return
	}
	defer unlock()
	if s.Svc.Mock {
		sw := s.Svc.MockSwitches()
		sw.Auth = "none"
		s.Svc.SetMock(sw)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	if err := s.Svc.Auth.SignOut(r.Context(), req.ChannelID); err != nil {
		writeErr(w, http.StatusOK, "partial", sentence(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) mockState(w http.ResponseWriter, r *http.Request) {
	var m core.MockState
	if err := readJSON(r, &m); err != nil {
		writeErr(w, http.StatusBadRequest, "bad", "The request was not understood.")
		return
	}
	s.Svc.SetMock(m)
	writeJSON(w, http.StatusOK, m)
}

// writeBusy refuses a second Publish, Undo or Sign out while the first runs.
// The panel never sends one (its button waits), so this is the second guard.
func writeBusy(w http.ResponseWriter) {
	writeErr(w, http.StatusConflict, "busy", "That is already running. Wait for it to finish.")
}

// Listen binds 127.0.0.1 only.
func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.Port))
}
