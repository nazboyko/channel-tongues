package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/nazboyko/channel-tongues/internal/youtube/fake"
)

// googleStub fakes Google's token and revoke endpoints.
type googleStub struct {
	mu        sync.Mutex
	verifiers []string
	revoked   []string
	refreshes int
}

func (g *googleStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	g.mu.Lock()
	defer g.mu.Unlock()
	switch r.URL.Path {
	case "/token":
		w.Header().Set("Content-Type", "application/json")
		if r.Form.Get("grant_type") == "refresh_token" {
			g.refreshes++
			if r.Form.Get("refresh_token") == "expired" {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`))
				return
			}
			w.Write(tokenJSON("fresh-access", ""))
			return
		}
		g.verifiers = append(g.verifiers, r.Form.Get("code_verifier"))
		w.Write(tokenJSON("first-access", "first-refresh"))
	case "/revoke":
		g.revoked = append(g.revoked, r.Form.Get("token"))
	default:
		http.NotFound(w, r)
	}
}

// tokenJSON renders a token endpoint answer with fake values.
func tokenJSON(access, refresh string) []byte {
	b, _ := json.Marshal(struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token,omitempty"`
		Type    string `json:"token_type"`
		Expires int    `json:"expires_in"`
	}{access, refresh, "Bearer", 3600})
	return b
}

type installedClient struct {
	Installed struct {
		ClientID     string   `json:"client_id"`
		ClientSecret string   `json:"client_secret"`
		AuthURI      string   `json:"auth_uri"`
		TokenURI     string   `json:"token_uri"`
		RedirectURIs []string `json:"redirect_uris"`
	} `json:"installed"`
}

func setupAuth(t *testing.T) (*Auth, *googleStub, *fake.API) {
	t.Helper()
	g := &googleStub{}
	gsrv := httptest.NewServer(g)
	t.Cleanup(gsrv.Close)
	api := fake.New()
	asrv := httptest.NewServer(api)
	t.Cleanup(asrv.Close)

	dir := t.TempDir()
	var c installedClient
	c.Installed.ClientID = "test-client.apps.googleusercontent.com"
	c.Installed.ClientSecret = "test-only"
	c.Installed.AuthURI = gsrv.URL + "/auth"
	c.Installed.TokenURI = gsrv.URL + "/token"
	c.Installed.RedirectURIs = []string{"http://localhost"}
	b, _ := json.Marshal(c)
	if err := os.WriteFile(filepath.Join(dir, "client_secret.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return &Auth{DataDir: dir, APIEndpoint: asrv.URL, RevokeURL: gsrv.URL + "/revoke"}, g, api
}

func TestLoginSavesTheChosenChannel(t *testing.T) {
	a, g, api := setupAuth(t)
	api.Mine = "UCmockLivingPostcard02"
	ctx := context.Background()
	login, err := a.StartLogin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.URL)
	q := u.Query()
	if q.Get("scope") != Scope || q.Get("code_challenge_method") != "S256" || q.Get("access_type") != "offline" {
		t.Fatalf("auth URL misses scope, PKCE or offline access: %s", login.URL)
	}
	redirect := q.Get("redirect_uri")
	if !strings.HasPrefix(redirect, "http://127.0.0.1:") {
		t.Fatalf("redirect must be loopback, got %s", redirect)
	}

	// A wrong state is ignored.
	resp, err := http.Get(redirect + "/?code=evil&state=wrong")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	resp, err = http.Get(redirect + "/?code=good&state=" + q.Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ch, err := login.Wait(wctx)
	if err != nil {
		t.Fatal(err)
	}
	if ch.ID != "UCmockLivingPostcard02" || ch.Title != "The Living Postcard" {
		t.Fatalf("channel %+v", ch)
	}
	if len(g.verifiers) != 1 || g.verifiers[0] == "" {
		t.Fatalf("the code exchange must carry the PKCE verifier: %v", g.verifiers)
	}

	path := filepath.Join(a.DataDir, "tokens", "UCmockLivingPostcard02.json")
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v %v", fi.Mode().Perm(), err)
	}
	chans, _ := a.Channels()
	if len(chans) != 1 || chans[0].Title != "The Living Postcard" {
		t.Fatalf("channels %+v", chans)
	}
}

func TestCancelledLogin(t *testing.T) {
	a, _, _ := setupAuth(t)
	login, err := a.StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.URL)
	q := u.Query()
	resp, err := http.Get(q.Get("redirect_uri") + "/?error=access_denied&state=" + q.Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("got %v", err)
	}
}

func TestRefreshIsSavedAndExpiryIsPlain(t *testing.T) {
	a, g, _ := setupAuth(t)
	old := &oauth2.Token{AccessToken: "stale", RefreshToken: "first-refresh", Expiry: time.Now().Add(-time.Hour)}
	if err := a.Save(ChannelToken{ChannelID: "UCmockSlowNightHours01", Title: "The Slow Night Hours", Token: old}); err != nil {
		t.Fatal(err)
	}
	client, err := a.HTTPClient(context.Background(), "UCmockSlowNightHours01")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewClient(context.Background(), ClientOptions{HTTP: client, Endpoint: a.APIEndpoint})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetVideo(context.Background(), "mockRain001"); err != nil {
		t.Fatal(err)
	}
	ct, _ := a.Load("UCmockSlowNightHours01")
	if ct.Token.AccessToken != "fresh-access" || ct.Token.RefreshToken != "first-refresh" || g.refreshes != 1 {
		t.Fatalf("refreshed token not saved: %+v refreshes=%d", ct.Token, g.refreshes)
	}

	expired := &oauth2.Token{AccessToken: "stale", RefreshToken: "expired", Expiry: time.Now().Add(-time.Hour)}
	a.Save(ChannelToken{ChannelID: "UCmockLivingPostcard02", Token: expired})
	client, _ = a.HTTPClient(context.Background(), "UCmockLivingPostcard02")
	c, _ = NewClient(context.Background(), ClientOptions{HTTP: client, Endpoint: a.APIEndpoint})
	_, err = c.GetVideo(context.Background(), "mockRain001")
	if err == nil || !strings.Contains(err.Error(), "tongues auth") {
		t.Fatalf("an expired sign-in should say how to sign in again, got %v", err)
	}
}

func TestSignOutRevokesAndDeletes(t *testing.T) {
	a, g, _ := setupAuth(t)
	a.Save(ChannelToken{ChannelID: "UCmockSlowNightHours01", Token: &oauth2.Token{AccessToken: "a", RefreshToken: "r"}})
	if err := a.SignOut(context.Background(), "UCmockSlowNightHours01"); err != nil {
		t.Fatal(err)
	}
	if len(g.revoked) != 1 || g.revoked[0] != "r" {
		t.Fatalf("revoked %v", g.revoked)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "tokens", "UCmockSlowNightHours01.json")); !os.IsNotExist(err) {
		t.Fatal("token file must be deleted")
	}
}

func TestSignOutDeletesEvenWhenGoogleIsUnreachable(t *testing.T) {
	a, _, _ := setupAuth(t)
	a.RevokeURL = "http://127.0.0.1:1/revoke"
	a.Save(ChannelToken{ChannelID: "UCmockSlowNightHours01", Token: &oauth2.Token{AccessToken: "a", RefreshToken: "r"}})
	err := a.SignOut(context.Background(), "UCmockSlowNightHours01")
	if err == nil || !strings.Contains(err.Error(), "myaccount.google.com/permissions") {
		t.Fatalf("should explain how to remove access by hand: %v", err)
	}
	if _, err := os.Stat(filepath.Join(a.DataDir, "tokens", "UCmockSlowNightHours01.json")); !os.IsNotExist(err) {
		t.Fatal("token file must be deleted anyway")
	}
}

func TestMissingClientFile(t *testing.T) {
	a := &Auth{DataDir: t.TempDir()}
	if _, err := a.Config(); err != ErrNoClientFile {
		t.Fatalf("got %v", err)
	}
	if a.HasClientFile() {
		t.Fatal("no file")
	}
	if _, err := a.Load("../escape"); err == nil {
		t.Fatal("bad channel id must be refused")
	}
}

func TestWebClientFileIsRefused(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "client_secret.json"), []byte(`{"web":{"client_id":"x","auth_uri":"https://a","token_uri":"https://t"}}`), 0o600)
	a := &Auth{DataDir: dir}
	if _, err := a.Config(); err == nil || !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("got %v", err)
	}
}

func TestEmptyCodeIsNotASignIn(t *testing.T) {
	a, _, _ := setupAuth(t)
	login, err := a.StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(login.URL)
	q := u.Query()
	resp, err := http.Get(q.Get("redirect_uri") + "/?code=&state=" + q.Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := login.Wait(ctx); err == nil {
		t.Fatal("an empty code must not count as signed in")
	}
}
