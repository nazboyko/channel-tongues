package youtube

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Scope is the narrowest YouTube scope that allows writing translations.
const Scope = "https://www.googleapis.com/auth/youtube.force-ssl"

const defaultRevokeURL = "https://oauth2.googleapis.com/revoke"

var channelIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

// ErrNotSignedIn means no channel has signed in yet.
var ErrNotSignedIn = errors.New("no channel is signed in yet; run tongues auth")

var errNotDesktopClient = errors.New("the Google sign-in file is not for a Desktop app; create an OAuth client of type Desktop app and download its file again")

// ErrSignInExpired means the saved sign-in no longer works (Testing-mode
// sign-ins expire after seven days).
var ErrSignInExpired = errors.New("the sign-in has expired; run tongues auth to sign in again")

// ErrNoClientFile means the person's Google project file is not in place.
var ErrNoClientFile = errors.New("the Google sign-in file is missing; run tongues setup")

// Auth manages sign-ins: the person's own Google project file and one token
// file per signed-in channel, all in the data directory.
type Auth struct {
	DataDir     string
	APIEndpoint string // YouTube API base URL override (tests, mock mode)
	RevokeURL   string // override for tests
	HTTP        *http.Client
}

// ChannelToken is the content of tokens/<channelId>.json.
type ChannelToken struct {
	ChannelID string        `json:"channelId"`
	Title     string        `json:"title"`
	SavedAt   time.Time     `json:"savedAt"`
	Token     *oauth2.Token `json:"token"`
}

func (a *Auth) clientFile() string { return filepath.Join(a.DataDir, "client_secret.json") }
func (a *Auth) tokenDir() string   { return filepath.Join(a.DataDir, "tokens") }

func (a *Auth) tokenFile(channelID string) (string, error) {
	if !channelIDPattern.MatchString(channelID) {
		return "", fmt.Errorf("%q is not a channel id", channelID)
	}
	return filepath.Join(a.tokenDir(), channelID+".json"), nil
}

var defaultHTTP = &http.Client{Timeout: 60 * time.Second}

func (a *Auth) httpClient() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return defaultHTTP
}

// HasClientFile reports whether the Google project file is in place.
func (a *Auth) HasClientFile() bool {
	_, err := os.Stat(a.clientFile())
	return err == nil
}

// Config reads the person's OAuth client (type Desktop app).
func (a *Auth) Config() (*oauth2.Config, error) {
	data, err := os.ReadFile(a.clientFile())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoClientFile
	}
	if err != nil {
		return nil, err
	}
	var kind struct {
		Installed json.RawMessage `json:"installed"`
	}
	if json.Unmarshal(data, &kind) != nil || len(kind.Installed) == 0 {
		return nil, errNotDesktopClient
	}
	cfg, err := google.ConfigFromJSON(data, Scope)
	if err != nil {
		return nil, errNotDesktopClient
	}
	return cfg, nil
}

// Save writes a channel's token file with owner-only permissions.
func (a *Auth) Save(ct ChannelToken) error {
	path, err := a.tokenFile(ct.ChannelID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.tokenDir(), 0o700); err != nil {
		return err
	}
	if ct.SavedAt.IsZero() {
		ct.SavedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(ct, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.tokenDir(), ".token-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a channel's token file.
func (a *Auth) Load(channelID string) (*ChannelToken, error) {
	path, err := a.tokenFile(channelID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("channel %s is not signed in; run tongues auth", channelID)
	}
	if err != nil {
		return nil, err
	}
	var ct ChannelToken
	if err := json.Unmarshal(data, &ct); err != nil || ct.Token == nil {
		return nil, fmt.Errorf("the sign-in file for channel %s is damaged; run tongues auth again", channelID)
	}
	return &ct, nil
}

// Channels lists the signed-in channels, by name.
func (a *Auth) Channels() ([]Channel, error) {
	paths, err := filepath.Glob(filepath.Join(a.tokenDir(), "*.json"))
	if err != nil {
		return nil, err
	}
	var out []Channel
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".json")
		ct, err := a.Load(id)
		if err != nil {
			continue
		}
		out = append(out, Channel{ID: ct.ChannelID, Title: ct.Title})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// HTTPClient returns an authorized client for a channel. Refreshed tokens are
// written back to the token file.
func (a *Auth) HTTPClient(ctx context.Context, channelID string) (*http.Client, error) {
	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	ct, err := a.Load(channelID)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, a.httpClient())
	path, _ := a.tokenFile(channelID)
	src := &savingSource{
		base: cfg.TokenSource(ctx, ct.Token),
		last: ct.Token,
		save: func(t *oauth2.Token) error {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				return nil // signed out meanwhile; do not bring the file back
			}
			ct.Token = t
			ct.SavedAt = time.Time{}
			return a.Save(*ct)
		},
	}
	client := oauth2.NewClient(ctx, src)
	client.Timeout = 60 * time.Second
	return client, nil
}

type savingSource struct {
	base oauth2.TokenSource
	mu   sync.Mutex
	last *oauth2.Token
	save func(*oauth2.Token) error
}

func (s *savingSource) Token() (*oauth2.Token, error) {
	t, err := s.base.Token()
	if err != nil {
		var rerr *oauth2.RetrieveError
		if errors.As(err, &rerr) && rerr.ErrorCode == "invalid_grant" {
			return nil, ErrSignInExpired
		}
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.last == nil || t.AccessToken != s.last.AccessToken {
		s.last = t
		if err := s.save(t); err != nil {
			return nil, fmt.Errorf("saving the refreshed sign-in: %w", err)
		}
	}
	return t, nil
}

// Login is a sign-in in progress: open URL in a browser, then Wait.
type Login struct {
	URL  string
	done chan loginResult
}

type loginResult struct {
	ch  Channel
	err error
}

// Wait blocks until the browser comes back, the context ends or ten minutes pass.
func (l *Login) Wait(ctx context.Context) (Channel, error) {
	select {
	case r := <-l.done:
		return r.ch, r.err
	case <-ctx.Done():
		return Channel{}, ctx.Err()
	}
}

// StartLogin starts a loopback listener on 127.0.0.1 and returns the Google
// sign-in URL. After consent it looks up which channel was picked and saves
// that channel's token.
func (a *Auth) StartLogin(ctx context.Context) (*Login, error) {
	cfg, err := a.Config()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	cfg.RedirectURL = "http://" + ln.Addr().String()
	state := randomHex(16)
	verifier := oauth2.GenerateVerifier()
	authURL := cfg.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("prompt", "select_account consent"),
	)

	login := &Login{URL: authURL, done: make(chan loginResult, 1)}
	codes := make(chan string, 1)
	failures := make(chan error, 1)
	outcome := make(chan error, 1)
	srv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			q := r.URL.Query()
			if q.Get("state") == "" && q.Get("code") == "" && q.Get("error") == "" {
				http.NotFound(w, r) // favicon and other noise
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			switch {
			case q.Get("state") != state:
				writePage(w, "This sign-in link is out of date. Start the sign-in again.")
				return
			case q.Get("error") != "" || q.Get("code") == "":
				writePage(w, "Sign-in was cancelled. You can close this tab.")
				select {
				case failures <- errors.New("sign-in was cancelled in the browser"):
				default:
				}
				return
			}
			select {
			case codes <- q.Get("code"):
			default:
				writePage(w, "This sign-in is already being finished. You can close this tab.")
				return
			}
			select {
			case err := <-outcome:
				if err != nil {
					writePage(w, "Sign-in did not finish: "+err.Error())
					return
				}
				writePage(w, "Signed in. You can close this tab and go back to Channel Tongues.")
			case <-time.After(30 * time.Second):
				writePage(w, "Almost done. Go back to Channel Tongues to see the result.")
			}
		}),
	}
	go srv.Serve(ln)

	go func() {
		defer func() {
			time.Sleep(200 * time.Millisecond) // let the last page reach the browser
			srv.Close()
		}()
		timeout := time.NewTimer(10 * time.Minute)
		defer timeout.Stop()
		var code string
		select {
		case code = <-codes:
		case err := <-failures:
			login.done <- loginResult{err: err}
			return
		case <-timeout.C:
			login.done <- loginResult{err: errors.New("sign-in timed out; start it again")}
			return
		case <-ctx.Done():
			login.done <- loginResult{err: ctx.Err()}
			return
		}
		ch, err := a.finishLogin(ctx, cfg, code, verifier)
		outcome <- err
		login.done <- loginResult{ch: ch, err: err}
	}()
	return login, nil
}

func (a *Auth) finishLogin(ctx context.Context, cfg *oauth2.Config, code, verifier string) (Channel, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, a.httpClient())
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return Channel{}, fmt.Errorf("Google did not accept the sign-in: %w", err)
	}
	client, err := NewClient(ctx, ClientOptions{HTTP: cfg.Client(ctx, tok), Endpoint: a.APIEndpoint})
	if err != nil {
		return Channel{}, err
	}
	ch, err := client.MyChannel(ctx)
	if err != nil {
		return Channel{}, err
	}
	if err := a.Save(ChannelToken{ChannelID: ch.ID, Title: ch.Title, Token: tok}); err != nil {
		return Channel{}, err
	}
	return ch, nil
}

// SignOut revokes a channel's token at Google and deletes the token file. The
// file is deleted even when Google cannot be reached; the error says so.
func (a *Auth) SignOut(ctx context.Context, channelID string) error {
	path, err := a.tokenFile(channelID)
	if err != nil {
		return err
	}
	ct, loadErr := a.Load(channelID)
	var revokeErr error
	if loadErr == nil && ct.Token != nil {
		revokeErr = a.revoke(ctx, ct.Token)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("the sign-in file is deleted, but Google could not be told (%v); remove access at https://myaccount.google.com/permissions", revokeErr)
	}
	return nil
}

func (a *Auth) revoke(ctx context.Context, t *oauth2.Token) error {
	tok := t.RefreshToken
	if tok == "" {
		tok = t.AccessToken
	}
	endpoint := a.RevokeURL
	if endpoint == "" {
		endpoint = defaultRevokeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(url.Values{"token": {tok}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// 400 invalid_token means it was already revoked or expired: fine.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("Google answered %s", resp.Status)
	}
	return nil
}

func writePage(w http.ResponseWriter, msg string) {
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Channel Tongues</title>`+
		`<body style="font-family:system-ui,sans-serif;background:#0B1220;color:#E8ECF4;display:grid;place-items:center;height:90vh">`+
		`<p style="font-size:18px">%s</p></body>`, html.EscapeString(msg))
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
