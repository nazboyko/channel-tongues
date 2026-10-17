package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TONGUES_DATA_DIR", dir)
	t.Setenv("TONGUES_PORT", "7999")
	t.Setenv("OLLAMA_URL", "http://127.0.0.1:1/")
	t.Setenv("TONGUES_MODEL", "gemma4:other")
	t.Setenv("TEST_VIDEO_IDS", "aaaaaaaaaaa, bbbbbbbbbbb")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DataDir != dir || c.Port != 7999 || c.OllamaURL != "http://127.0.0.1:1" || c.Model != "gemma4:other" {
		t.Fatalf("unexpected config %+v", c)
	}
	if len(c.TestVideoIDs) != 2 || c.TestVideoIDs[1] != "bbbbbbbbbbb" {
		t.Fatalf("test ids %v", c.TestVideoIDs)
	}
	if c.Addr() != "127.0.0.1:7999" {
		t.Fatalf("addr %s", c.Addr())
	}
}

func TestLoadRejectsBadPort(t *testing.T) {
	t.Setenv("TONGUES_DATA_DIR", t.TempDir())
	t.Setenv("TONGUES_PORT", "seventy")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for a bad port")
	}
}

func TestDotEnvIsReadForTestIDsOnly(t *testing.T) {
	dir := t.TempDir()
	env := "# comment\nOTHER=x\nexport TEST_VIDEO_IDS=\"ccccccccccc,ddddddddddd\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readDotEnv(filepath.Join(dir, ".env"), "TEST_VIDEO_IDS"); got != "ccccccccccc,ddddddddddd" {
		t.Fatalf("got %q", got)
	}
	if got := readDotEnv(filepath.Join(dir, ".env"), "MISSING"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestWriteAllowed(t *testing.T) {
	open := &Config{}
	if err := open.WriteAllowed("anything123"); err != nil {
		t.Fatalf("no allowlist must allow writes: %v", err)
	}
	guarded := &Config{TestVideoIDs: []string{"aaaaaaaaaaa"}}
	if err := guarded.WriteAllowed("aaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if err := guarded.WriteAllowed("zzzzzzzzzzz"); err == nil {
		t.Fatal("a video outside TEST_VIDEO_IDS must be refused")
	}
}

func TestTestAlias(t *testing.T) {
	c := &Config{TestVideoIDs: []string{"aaaaaaaaaaa", "bbbbbbbbbbb"}}
	cases := []struct {
		arg     string
		id      string
		isAlias bool
		wantErr bool
	}{
		{"test:1", "aaaaaaaaaaa", true, false},
		{"test:2", "bbbbbbbbbbb", true, false},
		{"test:3", "", true, true},
		{"test:x", "", true, true},
		{"aaaaaaaaaaa", "", false, false},
	}
	for _, tc := range cases {
		id, ok, err := c.TestAlias(tc.arg)
		if id != tc.id || ok != tc.isAlias || (err != nil) != tc.wantErr {
			t.Errorf("%s: got %q %v %v", tc.arg, id, ok, err)
		}
	}
}

func TestDisplay(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if got := Display(filepath.Join(home, "x", "y")); got != filepath.Join("~", "x", "y") {
		t.Fatalf("got %s", got)
	}
	if got := Display("/elsewhere"); got != "/elsewhere" {
		t.Fatalf("got %s", got)
	}
}
