package main

import (
	"errors"
	"flag"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/core"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

// app is the shared service plus what only the command line needs.
type app struct {
	*core.Service
}

func newApp(cfg *config.Config, mock bool) (*app, error) {
	svc, err := core.Open(cfg, mock)
	if err != nil {
		return nil, err
	}
	return &app{svc}, nil
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
