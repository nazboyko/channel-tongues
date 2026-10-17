package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/server"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

// setupEnv is what setup talks to, so it can be tested without a terminal.
type setupEnv struct {
	in        *bufio.Reader
	out       io.Writer
	open      func(url string) error
	downloads string // where the browser saves files
	pull      func(ctx context.Context, model string) error
	signIn    func(ctx context.Context, a *youtube.Auth) (youtube.Channel, error)
	poll      time.Duration
}

func runSetup(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print links instead of opening them")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	env := &setupEnv{
		in:        bufio.NewReader(os.Stdin),
		out:       os.Stdout,
		open:      openBrowser,
		downloads: filepath.Join(home, "Downloads"),
		pull: func(ctx context.Context, model string) error {
			cmd := exec.CommandContext(ctx, "ollama", "pull", model)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			return cmd.Run()
		},
		signIn: func(ctx context.Context, a *youtube.Auth) (youtube.Channel, error) {
			return signIn(ctx, a, *noBrowser)
		},
		poll: 2 * time.Second,
	}
	if *noBrowser {
		env.open = func(string) error { return errors.New("not opening") }
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return setup(ctx, cfg, env)
}

func (e *setupEnv) say(format string, a ...any) { fmt.Fprintf(e.out, format+"\n", a...) }

// ask returns true for yes; an empty answer takes the default.
func (e *setupEnv) ask(question string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Fprintf(e.out, "%s %s ", question, hint)
	line, _ := e.in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	}
	return false
}

func (e *setupEnv) waitEnter() {
	fmt.Fprint(e.out, "Press Enter when that is done. ")
	e.in.ReadString('\n')
}

// step shows one instruction with its page and waits for the person.
func (e *setupEnv) step(n int, text, url string) {
	e.say("\n%d. %s", n, text)
	if url != "" {
		if e.open(url) != nil {
			e.say("   Open: %s", url)
		} else {
			e.say("   Opened: %s", url)
		}
	}
	e.waitEnter()
}

func setup(ctx context.Context, cfg *config.Config, e *setupEnv) error {
	e.say("Channel Tongues setup. Each step is skipped when it is already done.")
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}

	// 1. The model.
	e.say("\nStep 1 of 4: the translation model")
	o := translate.NewOllama(cfg.OllamaURL, cfg.Model)
	for {
		state := translate.ModelReady
		if cfg.Model == "" {
			state = o.PickModel(ctx)
		} else {
			state, _ = o.Health(ctx)
		}
		if state == translate.ModelReady {
			e.say("Ready: %s.", o.Model)
			break
		}
		if state == translate.ModelDown {
			e.say("Ollama is not running. Install it from https://ollama.com/download or start the Ollama app.")
			e.waitEnter()
			continue
		}
		if !e.ask(fmt.Sprintf("The model %s is not downloaded yet (about 7 GB, once). Download it now?", o.Model), true) {
			e.say("Skipped. Translating needs it; run: ollama pull %s", o.Model)
			break
		}
		if err := e.pull(ctx, o.Model); err != nil {
			return fmt.Errorf("the download did not finish: %w", err)
		}
	}

	// 2. The person's own Google project, needed only to publish.
	e.say("\nStep 2 of 4: your Google project (only needed to publish for you)")
	auth := &youtube.Auth{DataDir: cfg.DataDir}
	if auth.HasClientFile() {
		e.say("Done: the sign-in file is in place.")
	} else if !e.ask("Publishing needs your own free Google project (ten minutes, once). Set it up now?", true) {
		e.say("Skipped. Translating, copying and saving to a folder work without it.")
	} else {
		e.step(1, "Create a project. Any name works, for example Channel Tongues. No billing is needed.", "https://console.cloud.google.com/projectcreate")
		e.step(2, "Turn on the YouTube Data API v3 for that project: click Enable.", "https://console.cloud.google.com/apis/library/youtube.googleapis.com")
		e.step(3, "Set up sign-in: click Get started, give it a name, pick External, and finish.", "https://console.cloud.google.com/auth/overview")
		e.step(4, "Under Audience, keep Testing and add your own Google e-mail as a test user.", "https://console.cloud.google.com/auth/audience")
		e.say("\n5. Create a client: type Desktop app, any name, then Create and Download JSON.")
		e.open("https://console.cloud.google.com/auth/clients/create")
		e.say("   Waiting for client_secret...json in %s (Ctrl+C to stop)...", config.Display(e.downloads))
		file, err := waitForClientFile(ctx, e.downloads, e.poll)
		if err != nil {
			return err
		}
		if err := installClientFile(file, cfg.DataDir); err != nil {
			return err
		}
		e.say("   Got it. Moved it into %s.", config.Display(cfg.DataDir))
	}

	// 3. Sign in each channel.
	e.say("\nStep 3 of 4: sign in your channel")
	if !auth.HasClientFile() {
		e.say("Skipped: it needs step 2.")
	} else {
		app, err := newApp(cfg, false)
		if err != nil {
			return err
		}
		defer app.Close()
		chans, _ := auth.Channels()
		for _, c := range chans {
			e.say("Signed in: %s.", c.Title)
		}
		wantMore := len(chans) == 0
		if len(chans) > 0 {
			wantMore = e.ask("Sign in another channel?", false)
		} else {
			e.say("Google may say it hasn't verified this app. It is your own project, so click Continue.")
			wantMore = e.ask("Sign in now?", true)
		}
		for wantMore {
			ch, err := e.signIn(ctx, auth)
			if err != nil {
				e.say("Sign-in did not finish: %v", err)
				if !e.ask("Try again?", true) {
					break
				}
				continue
			}
			app.SignedIn(ch)
			e.say("Signed in: %s.", ch.Title)
			wantMore = e.ask("Sign in another channel?", false)
		}
	}

	// 4. The extension and the helper.
	e.say("\nStep 4 of 4: the side panel in Chrome")
	code, err := server.LoadPairing(cfg.DataDir)
	if err != nil {
		return err
	}
	ext := extensionDir()
	e.say("1. Open chrome://extensions and switch on Developer mode (top right).")
	if ext != "" {
		e.say("2. Click Load unpacked and pick this folder: %s", config.Display(ext))
	} else {
		e.say("2. Click Load unpacked and pick the extension/dist folder of Channel Tongues (run make build first).")
	}
	e.say("3. Pin Channel Tongues in the toolbar, open a video in Studio and click the icon.")
	e.say("   The panel asks once for this code: %s", code)
	e.say("\nFrom now on, start the helper when you work in Studio: tongues serve")
	return nil
}

// waitForClientFile waits until a client_secret*.json appears in dir.
func waitForClientFile(ctx context.Context, dir string, every time.Duration) (string, error) {
	start := time.Now()
	for {
		matches, _ := filepath.Glob(filepath.Join(dir, "client_secret*.json"))
		for _, m := range matches {
			if fi, err := os.Stat(m); err == nil && fi.ModTime().After(start.Add(-24*time.Hour)) {
				return m, nil
			}
		}
		select {
		case <-ctx.Done():
			return "", errors.New("stopped waiting; run tongues setup again when the file is downloaded")
		case <-time.After(every):
		}
	}
}

// installClientFile checks the file is a Desktop app client and moves it
// into the data directory with owner-only permissions.
func installClientFile(src, dataDir string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	var kind struct {
		Installed json.RawMessage `json:"installed"`
	}
	if json.Unmarshal(b, &kind) != nil || len(kind.Installed) == 0 {
		return errors.New("that file is not for a Desktop app; create the client again with type Desktop app")
	}
	dst := filepath.Join(dataDir, "client_secret.json")
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return err
	}
	return os.Remove(src)
}

// extensionDir finds extension/dist next to the binary (bin/tongues in a checkout).
func extensionDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	for _, dir := range []string{filepath.Dir(exe), filepath.Dir(filepath.Dir(exe))} {
		p := filepath.Join(dir, "extension", "dist")
		if _, err := os.Stat(filepath.Join(p, "manifest.json")); err == nil {
			return p
		}
	}
	return ""
}
