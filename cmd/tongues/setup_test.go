package main

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

func ollamaStub(t *testing.T, tags string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(tags)) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestSetupFromNothingWithoutGoogle(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: filepath.Join(dir, "data"), OllamaURL: ollamaStub(t, `{"models":[{"name":"gemma4:e4b"}]}`), Port: 7312}
	var out bytes.Buffer
	env := &setupEnv{
		in:   bufio.NewReader(strings.NewReader("n\n")), // no Google project
		out:  &out,
		open: func(string) error { return nil },
	}
	if err := setup(context.Background(), cfg, env); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"Ready: gemma4:e4b.", "Skipped. Translating, copying and saving", "Skipped: it needs step 2.", "The panel asks once for this code:"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.DataDir, "pairing.txt")); err != nil {
		t.Fatal("the pairing code is created")
	}
}

func TestSetupGoogleStepsMoveTheDownloadedFile(t *testing.T) {
	dir := t.TempDir()
	downloads := filepath.Join(dir, "Downloads")
	os.MkdirAll(downloads, 0o755)
	cfg := &config.Config{DataDir: filepath.Join(dir, "data"), OllamaURL: ollamaStub(t, `{"models":[{"name":"gemma4:e4b"}]}`), Port: 7312}
	var opened []string
	var out bytes.Buffer
	// Answers: yes to the Google project, Enter four times, then no more channels.
	env := &setupEnv{
		in:        bufio.NewReader(strings.NewReader("y\n\n\n\n\n\nn\n")),
		out:       &out,
		open:      func(u string) error { opened = append(opened, u); return nil },
		downloads: downloads,
		poll:      10 * time.Millisecond,
		signIn: func(ctx context.Context, a *youtube.Auth) (youtube.Channel, error) {
			return youtube.Channel{ID: "UCmockSlowNightHours01", Title: "The Slow Night Hours"}, nil
		},
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		os.WriteFile(filepath.Join(downloads, "client_secret_123.apps.googleusercontent.com.json"), []byte(`{"installed":{"client_id":"x"}}`), 0o644)
	}()
	if err := setup(context.Background(), cfg, env); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 5 || !strings.Contains(opened[0], "projectcreate") || !strings.Contains(opened[4], "clients/create") {
		t.Fatalf("pages opened: %v", opened)
	}
	fi, err := os.Stat(filepath.Join(cfg.DataDir, "client_secret.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("client file: %v %v", err, fi)
	}
	if left, _ := filepath.Glob(filepath.Join(downloads, "client_secret*.json")); len(left) != 0 {
		t.Fatal("the download is moved, not copied")
	}
	if !strings.Contains(out.String(), "Signed in: The Slow Night Hours.") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestInstallClientFileRefusesWebClients(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "client_secret_web.json")
	os.WriteFile(src, []byte(`{"web":{"client_id":"x"}}`), 0o644)
	if err := installClientFile(src, dir); err == nil || !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("got %v", err)
	}
}

func TestSetupOffersTheLargerModelWithEnoughMemory(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{DataDir: filepath.Join(dir, "data"), OllamaURL: ollamaStub(t, `{"models":[{"name":"gemma4:e4b"}]}`), Port: 7312}
	var pulled []string
	var out bytes.Buffer
	env := &setupEnv{
		in:     bufio.NewReader(strings.NewReader("y\nn\n")), // yes to the larger model, no Google project
		out:    &out,
		open:   func(string) error { return nil },
		memory: func() int { return 64 },
		pull:   func(ctx context.Context, model string) error { pulled = append(pulled, model); return nil },
	}
	if err := setup(context.Background(), cfg, env); err != nil {
		t.Fatal(err)
	}
	if len(pulled) != 1 || pulled[0] != "gemma4:26b" || !strings.Contains(out.String(), "64 GB of memory") {
		t.Fatalf("pulled %v, output:\n%s", pulled, out.String())
	}
	// With little memory there is no question.
	out.Reset()
	env = &setupEnv{in: bufio.NewReader(strings.NewReader("n\n")), out: &out, open: func(string) error { return nil }, memory: func() int { return 16 }}
	setup(context.Background(), cfg, env)
	if strings.Contains(out.String(), "larger") {
		t.Fatalf("no offer on a 16 GB machine:\n%s", out.String())
	}
}
