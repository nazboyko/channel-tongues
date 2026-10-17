// Package fake is an in-memory stand-in for the parts of the YouTube Data API
// that Channel Tongues uses. Tests and `tongues serve --mock` run the real
// client code against it. Its data is synthetic.
package fake

import (
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed data/*.json
var data embed.FS

// Request is one call the fake received, for assertions in tests.
type Request struct {
	Method string
	Path   string
	Part   string
	// BodyKeys are the top-level keys of a JSON body (for updates).
	BodyKeys []string
}

// API is the fake. It is safe for concurrent use.
type API struct {
	mu       sync.Mutex
	channels []map[string]any
	videos   map[string]map[string]any
	order    []string // video ids in fixture order
	requests []Request
	// Mine is the channel id returned for channels.list mine=true.
	Mine string
	// FailUpdate, when set, makes the next videos.update fail with this reason.
	FailUpdate string
	// DropOnUpdate, when set, makes the next update silently drop this language
	// so a read-back mismatch can be tested.
	DropOnUpdate string
	// StaleReads makes this many reads after the next write return the
	// state from before it, the way YouTube's reads lag behind its writes.
	StaleReads int
	stale      map[string]map[string]any
	staleLeft  int
	// path, when set, keeps the videos in a file so that separate runs of the
	// command line see each other's changes in mock mode.
	path string
}

// NewPersistent is New with the videos kept in a JSON file at path.
func NewPersistent(path string) *API {
	a := New()
	a.path = path
	if b, err := os.ReadFile(path); err == nil {
		var saved map[string]map[string]any
		if json.Unmarshal(b, &saved) == nil {
			for id, v := range saved {
				if _, known := a.videos[id]; known {
					a.videos[id] = v
				}
			}
		}
	}
	return a
}

// save writes the videos to the file; the caller holds a.mu.
func (a *API) save() {
	if a.path == "" {
		return
	}
	if b, err := json.Marshal(a.videos); err == nil {
		os.WriteFile(a.path, b, 0o600)
	}
}

// New loads the bundled fixtures.
func New() *API {
	a := &API{videos: map[string]map[string]any{}}
	mustLoad("data/channels.json", &a.channels)
	var vids []map[string]any
	mustLoad("data/videos.json", &vids)
	for _, v := range vids {
		id := v["id"].(string)
		a.videos[id] = v
		a.order = append(a.order, id)
	}
	if len(a.channels) > 0 {
		a.Mine = a.channels[0]["id"].(string)
	}
	return a
}

func mustLoad(name string, v any) {
	b, err := data.ReadFile(name)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		panic(fmt.Sprintf("%s: %v", name, err))
	}
}

// Requests returns the calls received so far.
func (a *API) Requests() []Request {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Request(nil), a.requests...)
}

// VideoIDs lists the fixture videos.
func (a *API) VideoIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.order...)
}

// Localizations returns a copy of a video's translations.
func (a *API) Localizations(id string) map[string]map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string]map[string]string{}
	v, ok := a.videos[id]
	if !ok {
		return out
	}
	locs, _ := v["localizations"].(map[string]any)
	for code, l := range locs {
		m, _ := l.(map[string]any)
		title, _ := m["title"].(string)
		desc, _ := m["description"].(string)
		out[code] = map[string]string{"title": title, "description": desc}
	}
	return out
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/youtube/v3")
	rec := Request{Method: r.Method, Path: path, Part: strings.Join(multi(r, "part"), ",")}
	var body map[string]any
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			a.record(rec)
			apiErr(w, 400, "parseError", "bad JSON")
			return
		}
		for k := range body {
			rec.BodyKeys = append(rec.BodyKeys, k)
		}
		sort.Strings(rec.BodyKeys)
	}
	a.record(rec)

	switch {
	case path == "/videos" && r.Method == http.MethodGet:
		a.listVideos(w, r)
	case path == "/videos" && r.Method == http.MethodPut:
		a.updateVideo(w, r, body)
	case path == "/channels" && r.Method == http.MethodGet:
		a.listChannels(w, r)
	case path == "/playlistItems" && r.Method == http.MethodGet:
		a.listPlaylist(w, r)
	case path == "/i18nLanguages" && r.Method == http.MethodGet:
		a.listLanguages(w)
	default:
		apiErr(w, 404, "notFound", "no such endpoint in the fake")
	}
}

func (a *API) record(r Request) {
	a.mu.Lock()
	a.requests = append(a.requests, r)
	a.mu.Unlock()
}

// multi reads a parameter that may be repeated and comma-separated.
func multi(r *http.Request, key string) []string {
	var out []string
	for _, v := range r.URL.Query()[key] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func parts(r *http.Request) map[string]bool {
	out := map[string]bool{}
	for _, p := range multi(r, "part") {
		out[p] = true
	}
	return out
}

func pick(v map[string]any, want map[string]bool) map[string]any {
	out := map[string]any{"kind": v["kind"], "id": v["id"]}
	for k, val := range v {
		if want[k] {
			out[k] = deepCopy(val)
		}
	}
	return out
}

func deepCopy(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

func (a *API) listVideos(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	want := parts(r)
	var items []any
	for _, id := range multi(r, "id") {
		v, ok := a.videos[id]
		if old, isStale := a.stale[id]; isStale && a.staleLeft > 0 {
			v, ok = old, true
		}
		if ok {
			items = append(items, pick(v, want))
		}
	}
	if a.staleLeft > 0 {
		a.staleLeft--
	}
	writeJSON(w, map[string]any{"kind": "youtube#videoListResponse", "items": orEmpty(items)})
}

func (a *API) updateVideo(w http.ResponseWriter, r *http.Request, body map[string]any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	want := parts(r)
	id, _ := body["id"].(string)
	v, ok := a.videos[id]
	if !ok {
		apiErr(w, 404, "videoNotFound", "video not found")
		return
	}
	if a.FailUpdate != "" {
		reason := a.FailUpdate
		a.FailUpdate = ""
		apiErr(w, 400, reason, reason)
		return
	}
	if a.StaleReads > 0 {
		a.stale = map[string]map[string]any{id: deepCopy(v).(map[string]any)}
		a.staleLeft = a.StaleReads
		a.StaleReads = 0
	}
	snippet, _ := v["snippet"].(map[string]any)
	if want["snippet"] {
		newSnip, _ := body["snippet"].(map[string]any)
		if newSnip == nil || newSnip["title"] == nil || newSnip["categoryId"] == nil {
			apiErr(w, 400, "invalidRequest", "snippet updates need title and categoryId")
			return
		}
		snippet = deepCopy(newSnip).(map[string]any)
		v["snippet"] = snippet
	}
	if want["localizations"] {
		locs, _ := body["localizations"].(map[string]any)
		if len(locs) > 0 {
			if lang, _ := snippet["defaultLanguage"].(string); lang == "" {
				apiErr(w, 400, "defaultLanguageNotSet", "The request is trying to add localized video details without specifying the default language of the video details.")
				return
			}
		}
		for code, l := range locs {
			m, _ := l.(map[string]any)
			title, _ := m["title"].(string)
			desc, _ := m["description"].(string)
			if utf8.RuneCountInString(title) > 100 || strings.ContainsAny(title, "<>") {
				apiErr(w, 400, "invalidTitle", "invalid title for "+code)
				return
			}
			if utf8.RuneCountInString(desc) > 5000 || strings.ContainsAny(desc, "<>") {
				apiErr(w, 400, "invalidDescription", "invalid description for "+code)
				return
			}
		}
		stored := deepCopy(locs)
		if a.DropOnUpdate != "" {
			if m, ok := stored.(map[string]any); ok {
				delete(m, a.DropOnUpdate)
			}
			a.DropOnUpdate = ""
		}
		if stored == nil {
			stored = map[string]any{}
		}
		v["localizations"] = stored
	}
	a.save()
	writeJSON(w, pick(v, want))
}

func (a *API) listChannels(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	want := parts(r)
	q := r.URL.Query()
	var items []any
	for _, ch := range a.channels {
		id := ch["id"].(string)
		if (q.Get("mine") == "true" && id == a.Mine) || containsID(multi(r, "id"), id) {
			items = append(items, pick(ch, want))
		}
	}
	writeJSON(w, map[string]any{"kind": "youtube#channelListResponse", "items": orEmpty(items)})
}

func containsID(list []string, id string) bool {
	for _, p := range list {
		if p == id {
			return true
		}
	}
	return false
}

// listPlaylist serves an uploads playlist, newest first, in pages.
func (a *API) listPlaylist(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	q := r.URL.Query()
	channel := "UC" + strings.TrimPrefix(q.Get("playlistId"), "UU")
	var ids []string
	for _, id := range a.order {
		snip, _ := a.videos[id]["snippet"].(map[string]any)
		if snip["channelId"] == channel {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		apiErr(w, 404, "playlistNotFound", "playlist not found")
		return
	}
	sort.SliceStable(ids, func(i, j int) bool {
		pi, _ := a.videos[ids[i]]["snippet"].(map[string]any)["publishedAt"].(string)
		pj, _ := a.videos[ids[j]]["snippet"].(map[string]any)["publishedAt"].(string)
		return pi > pj
	})
	size, _ := strconv.Atoi(q.Get("maxResults"))
	if size <= 0 {
		size = 5
	}
	start, _ := strconv.Atoi(q.Get("pageToken"))
	end := min(start+size, len(ids))
	var items []any
	for _, id := range ids[start:end] {
		items = append(items, map[string]any{"kind": "youtube#playlistItem", "contentDetails": map[string]any{"videoId": id}})
	}
	resp := map[string]any{"kind": "youtube#playlistItemListResponse", "items": orEmpty(items)}
	if end < len(ids) {
		resp["nextPageToken"] = strconv.Itoa(end)
	}
	writeJSON(w, resp)
}

func (a *API) listLanguages(w http.ResponseWriter) {
	codes := [][2]string{{"de", "German"}, {"en", "English"}, {"es", "Spanish"}, {"fr", "French"}, {"ja", "Japanese"}, {"pt", "Portuguese (Brazil)"}, {"zh-CN", "Chinese"}}
	var items []any
	for _, c := range codes {
		items = append(items, map[string]any{"kind": "youtube#i18nLanguage", "id": c[0], "snippet": map[string]any{"hl": c[0], "name": c[1]}})
	}
	writeJSON(w, map[string]any{"kind": "youtube#i18nLanguageListResponse", "items": items})
}

func orEmpty(items []any) []any {
	if items == nil {
		return []any{}
	}
	return items
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	json.NewEncoder(w).Encode(v)
}

func apiErr(w http.ResponseWriter, code int, reason, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": msg,
			"errors":  []any{map[string]any{"reason": reason, "message": msg, "domain": "youtube.video"}},
		},
	})
}
