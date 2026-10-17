package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/translate"
)

// SaveRoot is ~/Channel Tongues (inside the data directory in mock mode, so
// tests and screenshots never write to the person's home folder).
func (s *Service) SaveRoot() (string, error) {
	if s.Mock {
		return filepath.Join(s.DataDir, "Channel Tongues"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Channel Tongues"), nil
}

// SaveRequest is what goes into translations.md.
type SaveRequest struct {
	Channel     string // channel name, or "" for pasted text
	VideoID     string // or "" for pasted text
	SourceTitle string
	Results     []translate.Result
}

// Save writes translations.md (one section per language: title, blank line,
// description) into ~/Channel Tongues/<channel or videos>/<video id or slug>/
// and returns the folder.
func (s *Service) Save(req SaveRequest) (string, error) {
	root, err := s.SaveRoot()
	if err != nil {
		return "", err
	}
	group := safeName(req.Channel)
	if group == "" {
		group = "videos"
	}
	name := req.VideoID
	if name == "" {
		name = slug(req.SourceTitle)
	}
	dir := filepath.Join(root, group, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", req.SourceTitle)
	b.WriteString("Paste each one into the video's Languages tab in YouTube Studio (Subtitles > Add language > Title & description).\n")
	for _, r := range req.Results {
		if r.Title == "" && r.Description == "" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s (%s)", langs.Name(r.Lang), r.Lang)
		if r.Status == translate.StatusCheck && r.Note != "" {
			fmt.Fprintf(&b, ". Check this one: %s", r.Note)
		}
		fmt.Fprintf(&b, "\n\n%s\n\n%s\n", r.Title, r.Description)
	}
	if err := os.WriteFile(filepath.Join(dir, "translations.md"), []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

// OpenFolder shows a folder under the save root in the system file browser.
func (s *Service) OpenFolder(dir string) error {
	root, err := s.SaveRoot()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("only folders inside %s can be opened", root)
	}
	if s.Mock {
		return nil
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", dir)
	case "windows":
		cmd = exec.Command("explorer", dir)
	default:
		cmd = exec.Command("xdg-open", dir)
	}
	return cmd.Start()
}

// safeName keeps a folder name readable and harmless.
func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == 0 || unicode.IsControl(r):
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	s = strings.Trim(s, ". ")
	if len([]rune(s)) > 60 {
		s = string([]rune(s)[:60])
	}
	return s
}

// slug turns a title into a short ASCII folder name.
func slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 50 {
			break
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "video-" + time.Now().Format("2006-01-02-150405")
	}
	return out
}
