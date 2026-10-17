package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/store"
	"github.com/nazboyko/channel-tongues/internal/youtube"
	"github.com/nazboyko/channel-tongues/internal/youtube/fake"
)

// app wires the pieces one command needs.
type app struct {
	cfg     *config.Config
	mock    bool
	store   *store.Store
	auth    *youtube.Auth
	backups *youtube.Backups
	log     *slog.Logger
	logFile io.Closer
	fakeAPI *fake.API
}

// newApp opens the data directory. In mock mode it uses <data dir>/mock and
// answers every YouTube call from the bundled synthetic fixtures.
func newApp(cfg *config.Config, mock bool) (*app, error) {
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
	a := &app{
		cfg:     cfg,
		mock:    mock,
		store:   st,
		auth:    &youtube.Auth{DataDir: cfg.DataDir},
		backups: &youtube.Backups{Dir: filepath.Join(dataDir, "backups")},
	}
	if f, err := os.OpenFile(filepath.Join(dataDir, "tongues.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		a.log = slog.New(slog.NewTextHandler(f, nil))
		a.logFile = f
	} else {
		a.log = slog.New(slog.DiscardHandler)
	}
	if mock {
		a.fakeAPI = fake.New()
		a.auth = &youtube.Auth{DataDir: dataDir, APIEndpoint: fake.Endpoint}
	}
	return a, nil
}

func (a *app) close() {
	a.store.Close()
	if a.logFile != nil {
		a.logFile.Close()
	}
}

// channels lists the signed-in channels (in mock mode, the fixture channels).
func (a *app) channels() ([]youtube.Channel, error) {
	if a.mock {
		var out []youtube.Channel
		for _, c := range a.fakeAPI.Channels() {
			out = append(out, youtube.Channel{ID: c[0], Title: c[1]})
		}
		return out, nil
	}
	return a.auth.Channels()
}

// clientFor returns an API client using a channel's sign-in; with an empty
// channelID it uses any signed-in channel (good for reads).
func (a *app) clientFor(ctx context.Context, channelID string) (*youtube.Client, error) {
	if a.mock {
		if channelID == "" {
			channelID = a.fakeAPI.Mine
		}
		return youtube.NewClient(ctx, youtube.ClientOptions{
			HTTP: a.fakeAPI.Client(), Endpoint: fake.Endpoint, Store: a.store, Log: a.log, ChannelID: channelID,
		})
	}
	if channelID == "" {
		chans, err := a.auth.Channels()
		if err != nil {
			return nil, err
		}
		if len(chans) == 0 {
			return nil, youtube.ErrNotSignedIn
		}
		channelID = chans[0].ID
	}
	httpClient, err := a.auth.HTTPClient(ctx, channelID)
	if err != nil {
		return nil, err
	}
	return youtube.NewClient(ctx, youtube.ClientOptions{
		HTTP: httpClient, Store: a.store, Log: a.log, ChannelID: channelID,
	})
}

// getVideo reads a video with any signed-in channel, trying the others when
// one cannot see it (private videos are visible to their own channel only).
func (a *app) getVideo(ctx context.Context, id string) (*youtube.Video, *youtube.Client, error) {
	chans, err := a.channels()
	if err != nil {
		return nil, nil, err
	}
	if len(chans) == 0 {
		return nil, nil, youtube.ErrNotSignedIn
	}
	var lastErr error
	for _, ch := range chans {
		client, err := a.clientFor(ctx, ch.ID)
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

// resolveVideo accepts an id, a video link or test:N.
func (a *app) resolveVideo(arg string) (string, error) {
	if id, ok, err := a.cfg.TestAlias(arg); ok {
		return id, err
	}
	if id, ok := youtube.ParseVideoID(arg); ok {
		return id, nil
	}
	return "", fmt.Errorf("%q is not a video id or link; copy the link from the browser's address bar", arg)
}

// parseArgs parses flags that may appear before, between or after positional
// arguments, and returns the positional ones.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	fs.SetOutput(io.Discard)
	var pos []string
	// A video id may start with "-"; take those out before flag parsing.
	rest := args[:0:0]
	for _, a := range args {
		if _, ok := youtube.ParseVideoID(a); ok && strings.HasPrefix(a, "-") && fs.Lookup(strings.TrimLeft(a, "-")) == nil {
			pos = append(pos, a)
			continue
		}
		rest = append(rest, a)
	}
	args = rest
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return pos, nil
		}
		if args[0] == "--" {
			return append(pos, args[1:]...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// openBrowser opens a URL with the system's default handler.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

// truncate shortens s to n runes with an ellipsis.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// pad right-pads s to n terminal columns.
func pad(s string, n int) string {
	if w := displayWidth(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// displayWidth counts terminal columns: wide CJK runes take two, combining
// marks none.
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), r == 0x200D:
		case width.LookupRune(r).Kind() == width.EastAsianWide, width.LookupRune(r).Kind() == width.EastAsianFullwidth:
			w += 2
		default:
			w++
		}
	}
	return w
}

// friendly turns errors into the sentence a person should read.
func friendly(err error) error {
	if errors.Is(err, youtube.ErrVideoNotFound) {
		return errors.New("that video was not found, or the signed-in channel cannot see it")
	}
	return err
}
