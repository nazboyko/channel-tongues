// Package core is what the command line and the helper share: the data
// directory, sign-ins, video reads, the model, guarded publishing, and
// saving translations to a folder.
package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/store"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
	"github.com/nazboyko/channel-tongues/internal/youtube/fake"
)

// ErrNotOwner means no signed-in channel owns the video, so it cannot be written.
var ErrNotOwner = errors.New("sign in with the channel that owns this video first")

// Service holds the pieces one process needs.
type Service struct {
	Cfg     *config.Config
	Mock    bool
	DataDir string // the effective data directory (<data dir>/mock in mock mode)
	Store   *store.Store
	Auth    *youtube.Auth
	Backups *youtube.Backups
	Log     *slog.Logger
	Fake    *fake.API

	logFile io.Closer
	mu      sync.Mutex
	mock    MockState
	writing map[string]*sync.Mutex
}

// LockVideo serializes writes to one video inside this process: a publish
// plans from what it read, and two at once would delete each other's
// languages. (Across processes, the re-read before every write catches it.)
func (s *Service) LockVideo(videoID string) func() {
	s.mu.Lock()
	if s.writing == nil {
		s.writing = map[string]*sync.Mutex{}
	}
	m, ok := s.writing[videoID]
	if !ok {
		m = &sync.Mutex{}
		s.writing[videoID] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// MockState lets tests and screenshots force the states the panel shows.
type MockState struct {
	Ollama string `json:"ollama"` // "", "down", "missing"
	Auth   string `json:"auth"`   // "", "none"
}

// Open prepares the data directory. In mock mode it uses <data dir>/mock,
// answers YouTube calls from the bundled synthetic fixtures and translates
// with the recorded sample answers.
func Open(cfg *config.Config, mock bool) (*Service, error) {
	dataDir := cfg.DataDir
	if mock {
		dataDir = filepath.Join(cfg.DataDir, "mock")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(dataDir, "tongues.db"))
	if err != nil {
		return nil, err
	}
	s := &Service{
		Cfg:     cfg,
		Mock:    mock,
		DataDir: dataDir,
		Store:   st,
		Auth:    &youtube.Auth{DataDir: cfg.DataDir},
		Backups: &youtube.Backups{Dir: filepath.Join(dataDir, "backups")},
		Log:     slog.New(slog.DiscardHandler),
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "tongues.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		s.Log = slog.New(slog.NewTextHandler(f, nil))
		s.logFile = f
	}
	if mock {
		s.Fake = fake.NewPersistent(filepath.Join(dataDir, "fake-youtube.json"))
		s.Auth = &youtube.Auth{DataDir: dataDir, APIEndpoint: fake.Endpoint}
		if seeded, _, _ := st.Setting("mockSeeded"); seeded == "" {
			for _, ch := range s.Fake.Channels() {
				st.AddKeepWord(ch[1])
			}
			st.SetSetting("mockSeeded", "1")
		}
	}
	return s, nil
}

// Close releases the database and the log file.
func (s *Service) Close() {
	s.Store.Close()
	if s.logFile != nil {
		s.logFile.Close()
	}
}

// SetMock changes the forced states (mock mode only).
func (s *Service) SetMock(m MockState) {
	s.mu.Lock()
	s.mock = m
	s.mu.Unlock()
}

// MockSwitches returns the forced states.
func (s *Service) MockSwitches() MockState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mock
}

// Channels lists the signed-in channels (the fixture channels in mock mode).
func (s *Service) Channels() ([]youtube.Channel, error) {
	if s.Mock {
		if s.MockSwitches().Auth == "none" {
			return nil, nil
		}
		var out []youtube.Channel
		for _, c := range s.Fake.Channels() {
			out = append(out, youtube.Channel{ID: c[0], Title: c[1]})
		}
		return out, nil
	}
	return s.Auth.Channels()
}

// ClientFor returns an API client with a channel's sign-in.
func (s *Service) ClientFor(ctx context.Context, channelID string) (*youtube.Client, error) {
	if s.Mock {
		return youtube.NewClient(ctx, youtube.ClientOptions{
			HTTP: s.Fake.Client(), Endpoint: fake.Endpoint, Store: s.Store, Log: s.Log, ChannelID: channelID,
		})
	}
	httpClient, err := s.Auth.HTTPClient(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return youtube.NewClient(ctx, youtube.ClientOptions{HTTP: httpClient, Store: s.Store, Log: s.Log, ChannelID: channelID})
}

// GetVideo reads a video with the signed-in channel that can see it,
// trying each (a private video is visible to its own channel only).
func (s *Service) GetVideo(ctx context.Context, id string) (*youtube.Video, *youtube.Client, error) {
	chans, err := s.Channels()
	if err != nil {
		return nil, nil, err
	}
	if len(chans) == 0 {
		return nil, nil, youtube.ErrNotSignedIn
	}
	var lastErr error
	for _, ch := range chans {
		client, err := s.ClientFor(ctx, ch.ID)
		if err != nil {
			lastErr = err
			continue
		}
		v, err := client.GetVideo(ctx, id)
		if err == nil {
			return v, client, nil
		}
		lastErr = err
		if !errors.Is(err, youtube.ErrVideoNotFound) {
			return nil, nil, err
		}
	}
	return nil, nil, lastErr
}

// Writer returns a guarded writer using the sign-in of the channel that owns
// the video.
func (s *Service) Writer(ctx context.Context, v *youtube.Video) (*youtube.Writer, error) {
	chans, err := s.Channels()
	if err != nil {
		return nil, err
	}
	owner := ""
	for _, ch := range chans {
		if ch.ID == v.ChannelID {
			owner = ch.ID
		}
	}
	if owner == "" {
		return nil, ErrNotOwner
	}
	client, err := s.ClientFor(ctx, owner)
	if err != nil {
		return nil, err
	}
	w := &youtube.Writer{Client: client, Backups: s.Backups, Store: s.Store, Allowed: s.Cfg.WriteAllowed}
	if s.Mock {
		w.Allowed = nil // writes go to the in-memory fake, never to YouTube
	}
	return w, nil
}

// ResolveVideo accepts an id, a video link or test:N.
func (s *Service) ResolveVideo(arg string) (string, error) {
	if id, ok, err := s.Cfg.TestAlias(arg); ok {
		return id, err
	}
	if id, ok := youtube.ParseVideoID(arg); ok {
		return id, nil
	}
	return "", fmt.Errorf("%q is not a video id or link; copy the link from the browser's address bar", arg)
}

// Ollama returns a client for the model: the given tag, then TONGUES_MODEL,
// then the best Gemma 4 that is downloaded.
func (s *Service) Ollama(ctx context.Context, model string) *translate.Ollama {
	if model == "" {
		model = s.Cfg.Model
	}
	o := translate.NewOllama(s.Cfg.OllamaURL, model)
	if model == "" {
		o.PickModel(ctx)
	}
	return o
}

// ModelStatus is what the panel shows about the model.
type ModelStatus struct {
	State translate.ModelState `json:"state"`
	Model string               `json:"model"`
}

// ModelHealth reports the model state (forced in mock mode).
func (s *Service) ModelHealth(ctx context.Context) ModelStatus {
	if s.Mock {
		switch s.MockSwitches().Ollama {
		case "down":
			return ModelStatus{translate.ModelDown, translate.Fallback}
		case "missing":
			return ModelStatus{translate.ModelMissing, translate.Fallback}
		}
		return ModelStatus{translate.ModelReady, "gemma4:26b"}
	}
	o := translate.NewOllama(s.Cfg.OllamaURL, s.Cfg.Model)
	if s.Cfg.Model == "" {
		return ModelStatus{o.PickModel(ctx), o.Model}
	}
	st, _ := o.Health(ctx)
	return ModelStatus{st, o.Model}
}

// Model returns the translation model: the recorded samples in mock mode.
func (s *Service) Model(ctx context.Context) translate.Model {
	if s.Mock {
		return translate.NewFakeModel(350 * time.Millisecond)
	}
	return s.Ollama(ctx, "")
}

// SignedIn records a new channel: its name becomes a keep word by default.
func (s *Service) SignedIn(ch youtube.Channel) {
	if ch.Title != "" {
		s.Store.AddKeepWord(ch.Title)
	}
}

// KeepWords lists the words that stay as written.
func (s *Service) KeepWords() []string {
	words, err := s.Store.KeepWords()
	if err != nil {
		s.Log.Warn("keep words", "err", err)
	}
	return words
}
