// Package config resolves where Channel Tongues keeps its files and which
// helper settings apply. A person never edits any of this; the environment
// overrides exist for tests and development.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const (
	DefaultPort      = 7312
	DefaultOllamaURL = "http://127.0.0.1:11434"
	appDirName       = "channel-tongues"
)

// Config holds the resolved settings for one run of the binary.
type Config struct {
	DataDir   string
	Port      int
	OllamaURL string
	// Model is TONGUES_MODEL; empty means the helper picks the best Gemma 4
	// that is downloaded.
	Model string
	// TestVideoIDs comes from TEST_VIDEO_IDS (environment or ./.env). When it
	// is set, writes to any other video are refused.
	TestVideoIDs []string
}

// Load reads the environment overrides and ./.env (TEST_VIDEO_IDS only).
func Load() (*Config, error) {
	c := &Config{
		DataDir:   os.Getenv("TONGUES_DATA_DIR"),
		Port:      DefaultPort,
		OllamaURL: strings.TrimRight(os.Getenv("OLLAMA_URL"), "/"),
		Model:     os.Getenv("TONGUES_MODEL"),
	}
	if c.DataDir == "" {
		dir, err := DefaultDataDir()
		if err != nil {
			return nil, err
		}
		c.DataDir = dir
	}
	if p := os.Getenv("TONGUES_PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n <= 0 || n > 65535 {
			return nil, fmt.Errorf("TONGUES_PORT must be a port number, got %q", p)
		}
		c.Port = n
	}
	if c.OllamaURL == "" {
		c.OllamaURL = DefaultOllamaURL
	}
	ids := os.Getenv("TEST_VIDEO_IDS")
	if ids == "" {
		for _, path := range dotEnvCandidates() {
			if ids = readDotEnv(path, "TEST_VIDEO_IDS"); ids != "" {
				break
			}
		}
	}
	c.TestVideoIDs = splitList(ids)
	return c, nil
}

// dotEnvCandidates lists where a project .env may be: the working directory
// and its parents up to the module root, and the binary's folder and its
// parent (bin/tongues in a checkout). The write guard must not depend on
// which folder a command is started from.
func dotEnvCandidates() []string {
	var out []string
	if wd, err := os.Getwd(); err == nil {
		dir := wd
		for {
			out = append(out, filepath.Join(dir, ".env"))
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		out = append(out, filepath.Join(dir, ".env"), filepath.Join(filepath.Dir(dir), ".env"))
	}
	return out
}

// GuardLine describes the write guard for doctor, serve and --apply.
func (c *Config) GuardLine() string {
	if len(c.TestVideoIDs) == 0 {
		return ""
	}
	return fmt.Sprintf("Writes are limited to the %d test video(s) in TEST_VIDEO_IDS.", len(c.TestVideoIDs))
}

// DefaultDataDir is ~/Library/Application Support/channel-tongues on macOS
// and $XDG_CONFIG_HOME/channel-tongues (or ~/.config/channel-tongues) elsewhere.
func DefaultDataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", appDirName), nil
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, appDirName), nil
	}
	return filepath.Join(home, ".config", appDirName), nil
}

// Path joins parts under the data directory.
func (c *Config) Path(parts ...string) string {
	return filepath.Join(append([]string{c.DataDir}, parts...)...)
}

// Addr is the loopback address the helper listens on.
func (c *Config) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", c.Port)
}

// WriteAllowed reports whether a write to videoID is allowed. With no
// TEST_VIDEO_IDS every video is allowed.
func (c *Config) WriteAllowed(videoID string) error {
	if len(c.TestVideoIDs) == 0 {
		return nil
	}
	for _, id := range c.TestVideoIDs {
		if id == videoID {
			return nil
		}
	}
	return fmt.Errorf("writes are limited to the videos in TEST_VIDEO_IDS, and %s is not one of them", videoID)
}

// TestAlias resolves "test:N" (1-based) to the Nth id in TEST_VIDEO_IDS.
// ok is false when arg is not an alias.
func (c *Config) TestAlias(arg string) (id string, ok bool, err error) {
	rest, found := strings.CutPrefix(arg, "test:")
	if !found {
		return "", false, nil
	}
	n, convErr := strconv.Atoi(rest)
	if convErr != nil || n < 1 || n > len(c.TestVideoIDs) {
		return "", true, fmt.Errorf("%s does not match an entry in TEST_VIDEO_IDS (%d set)", arg, len(c.TestVideoIDs))
	}
	return c.TestVideoIDs[n-1], true, nil
}

// Display shortens a path under the home directory to ~/... for printing.
func Display(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || strings.HasPrefix(rest, string(filepath.Separator))) {
		return "~" + rest
	}
	return path
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// readDotEnv returns the value of one key from a KEY=VALUE file, or "".
func readDotEnv(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, found := strings.Cut(line, "=")
		if !found || strings.TrimSpace(strings.TrimPrefix(k, "export ")) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return ""
}
