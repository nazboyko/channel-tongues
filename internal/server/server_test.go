package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/core"
)

const testPort = 7399

type harness struct {
	t   *testing.T
	srv *httptest.Server
	s   *Server
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	svc, err := core.Open(&config.Config{DataDir: t.TempDir(), OllamaURL: "http://127.0.0.1:1", TestVideoIDs: []string{"onlyThisOne"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	s := New(svc, "ABCD-EFGH", testPort)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, srv: ts, s: s}
}

func (h *harness) do(method, path, body string, hdr map[string]string) *http.Response {
	h.t.Helper()
	req, _ := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	req.Host = "127.0.0.1:7399"
	req.Header.Set("Authorization", "Bearer abcd-efgh")
	req.Header.Set("Origin", "chrome-extension://"+ExtensionID)
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestSecurityChecks(t *testing.T) {
	h := newHarness(t)
	if r := h.do("GET", "/health", "", map[string]string{"Authorization": ""}); r.StatusCode != 401 {
		t.Fatalf("no code: %d", r.StatusCode)
	}
	if r := h.do("GET", "/health", "", map[string]string{"Authorization": "Bearer WRONG-CODE"}); r.StatusCode != 401 {
		t.Fatalf("wrong code: %d", r.StatusCode)
	}
	r := h.do("GET", "/health", "", map[string]string{"Origin": "https://evil.example"})
	if r.StatusCode != 403 || r.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("foreign origin: %d %q", r.StatusCode, r.Header.Get("Access-Control-Allow-Origin"))
	}
	req, _ := http.NewRequest("GET", h.srv.URL+"/health", nil)
	req.Host = "attacker.example:7399"
	req.Header.Set("Authorization", "Bearer ABCD-EFGH")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("foreign Host header (DNS rebinding): %d", resp.StatusCode)
	}
	pre := h.do("OPTIONS", "/jobs", "", map[string]string{"Authorization": ""})
	if pre.StatusCode != 204 || pre.Header.Get("Access-Control-Allow-Origin") != "chrome-extension://"+ExtensionID {
		t.Fatalf("preflight: %d %v", pre.StatusCode, pre.Header)
	}
	ok := h.do("GET", "/health", "", map[string]string{"Origin": ""}) // the CLI or curl: no Origin
	if ok.StatusCode != 200 {
		t.Fatalf("no origin with the code: %d", ok.StatusCode)
	}
}

func TestTooManyWrongCodes(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 20; i++ {
		h.do("GET", "/health", "", map[string]string{"Authorization": "Bearer NOPE"})
	}
	if r := h.do("GET", "/health", "", nil); r.StatusCode != 429 {
		t.Fatalf("after 20 wrong codes even the right one waits: %d", r.StatusCode)
	}
}

func TestHealthAndMockSwitches(t *testing.T) {
	h := newHarness(t)
	var hr healthResponse
	decode(t, h.do("GET", "/health", "", nil), &hr)
	if !hr.OK || !hr.Mock || hr.Model.State != "ready" || !hr.SignedIn || len(hr.Channels) != 2 || len(hr.KeepWords) != 2 {
		t.Fatalf("health %+v", hr)
	}
	h.do("POST", "/mock/state", `{"ollama":"missing","auth":"none"}`, nil).Body.Close()
	decode(t, h.do("GET", "/health", "", nil), &hr)
	if hr.Model.State != "model-missing" || hr.SignedIn {
		t.Fatalf("switched health %+v", hr)
	}
	var e map[string]string
	r := h.do("GET", "/video/mockRain001", "", nil)
	decode(t, r, &e)
	if r.StatusCode != 409 || e["code"] != "signin" {
		t.Fatalf("signed out video read: %d %v", r.StatusCode, e)
	}
}

func TestVideoAndUploads(t *testing.T) {
	h := newHarness(t)
	var v videoResponse
	decode(t, h.do("GET", "/video/mockRain001", "", nil), &v)
	if v.Title == "" || v.SourceName != "English" || len(v.Languages) != 2 || len(v.Suggested) != 12 || !strings.HasSuffix(v.StudioURL, "/translations") {
		t.Fatalf("video %+v", v)
	}
	r := h.do("GET", "/video/notarealid", "", nil)
	if r.StatusCode != 400 {
		t.Fatalf("bad id: %d", r.StatusCode)
	}
	var up struct{ Videos []uploadItem }
	decode(t, h.do("GET", "/channel/videos?limit=4", "", nil), &up)
	if len(up.Videos) != 4 || up.Videos[0].ID != "mockRain001" {
		t.Fatalf("uploads %+v", up.Videos)
	}
}

// readEvents reads server-sent events until "done".
func readEvents(t *testing.T, resp *http.Response) (names []string, last string) {
	t.Helper()
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	ev := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev = strings.TrimPrefix(line, "event: ")
			names = append(names, ev)
		case strings.HasPrefix(line, "data: "):
			last = strings.TrimPrefix(line, "data: ")
			if ev == "done" {
				return names, last
			}
		}
	}
	return names, last
}

func TestTranslatePublishUndo(t *testing.T) {
	h := newHarness(t)
	var job map[string]string
	r := h.do("POST", "/jobs", `{"videoId":"mockRain001","languages":["es","ja","de","en"]}`, nil)
	decode(t, r, &job)
	if r.StatusCode != 202 || job["jobId"] == "" {
		t.Fatalf("start: %d %v", r.StatusCode, job)
	}
	names, last := readEvents(t, h.do("GET", "/jobs/"+job["jobId"]+"/events", "", nil))
	if names[0] != "snapshot" || names[len(names)-1] != "done" || len(names) < 4 {
		t.Fatalf("events %v", names)
	}
	var snap struct {
		Ready, ToCheck int
		Languages      []struct{ Code, Status, Title string }
	}
	json.Unmarshal([]byte(last), &snap)
	if snap.Ready != 2 || snap.ToCheck != 1 || len(snap.Languages) != 3 {
		t.Fatalf("the source language is dropped; ja is to check: %+v", snap)
	}

	// Edit one, publish es and de (de exists on the video and is someone else's: kept).
	r = h.do("PUT", "/jobs/"+job["jobId"]+"/translations", `{"lang":"es","title":"Lluvia editada","description":"Tres horas."}`, nil)
	if r.StatusCode != 200 {
		t.Fatalf("edit %d", r.StatusCode)
	}
	r.Body.Close()
	var pub publishResponse
	r = h.do("POST", "/jobs/"+job["jobId"]+"/publish", `{"languages":["es","de"]}`, nil)
	decode(t, r, &pub)
	if r.StatusCode != 200 || pub.Published != 1 || pub.Kept != 1 || pub.BackupID == "" {
		t.Fatalf("publish %d %+v", r.StatusCode, pub)
	}
	if got := h.s.Svc.Fake.Localizations("mockRain001")["es"]["title"]; got != "Lluvia editada" {
		t.Fatalf("the edited text is what gets published, got %q", got)
	}

	// Undo.
	var undo publishResponse
	r = h.do("POST", "/videos/mockRain001/restore", `{"backupId":"`+pub.BackupID+`"}`, nil)
	decode(t, r, &undo)
	if r.StatusCode != 200 {
		t.Fatalf("undo %d", r.StatusCode)
	}
	if _, ok := h.s.Svc.Fake.Localizations("mockRain001")["es"]; ok {
		t.Fatal("undo removes what was published")
	}
}

func TestPasteAndSave(t *testing.T) {
	h := newHarness(t)
	var job map[string]string
	decode(t, h.do("POST", "/jobs", `{"title":"Rain on a Cabin Window at Night · 3 Hours of Sleep Sounds","description":"Three hours.","sourceLanguage":"en","languages":["es"]}`, nil), &job)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var snap struct{ State string }
		decode(t, h.do("GET", "/jobs/"+job["jobId"], "", nil), &snap)
		if snap.State != "running" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := h.do("POST", "/jobs/"+job["jobId"]+"/publish", `{"languages":["es"]}`, nil)
	if r.StatusCode != 400 {
		t.Fatalf("pasted text cannot be published: %d", r.StatusCode)
	}
	var saved map[string]string
	r = h.do("POST", "/jobs/"+job["jobId"]+"/save", ``, nil)
	decode(t, r, &saved)
	if r.StatusCode != 200 {
		t.Fatalf("save %d %v", r.StatusCode, saved)
	}
	b, err := os.ReadFile(filepath.Join(saved["folder"], "translations.md"))
	if err != nil || !strings.Contains(string(b), "## Español (es)") {
		t.Fatalf("file: %v %s", err, b)
	}
}

func TestSettingsReplaceKeepWords(t *testing.T) {
	h := newHarness(t)
	var got settingsBody
	decode(t, h.do("PUT", "/settings", `{"keepWords":["The Slow Night Hours","Lofi Lantern"]}`, nil), &got)
	if len(got.KeepWords) != 2 || got.KeepWords[1] != "Lofi Lantern" {
		t.Fatalf("got %v", got.KeepWords)
	}
}

func TestErrorsAreSentences(t *testing.T) {
	h := newHarness(t)
	r := h.do("POST", "/jobs", `{"videoId":"mockRain001","languages":[]}`, nil)
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 400 || !strings.Contains(string(body), "Pick at least one language.") {
		t.Fatalf("%d %s", r.StatusCode, body)
	}
	r = h.do("GET", "/jobs/nope", "", nil)
	if r.StatusCode != 404 {
		t.Fatalf("unknown job %d", r.StatusCode)
	}
}

func TestPairingCodeFile(t *testing.T) {
	dir := t.TempDir()
	code, err := LoadPairing(dir)
	if err != nil || len(code) != 9 || code[4] != '-' || strings.ContainsAny(code, "IL01O") {
		t.Fatalf("code %q %v", code, err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "pairing.txt"))
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode().Perm())
	}
	again, _ := LoadPairing(dir)
	if again != code {
		t.Fatal("the code stays the same across restarts")
	}
}
