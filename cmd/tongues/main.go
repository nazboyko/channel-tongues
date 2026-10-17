// Command tongues is the Channel Tongues helper and command line: it
// translates a video's title and description with a local model and
// publishes the translations to the video with the person's own sign-in.
package main

import (
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nazboyko/channel-tongues/internal/config"
)

type command struct {
	name    string
	usage   string
	summary string
	run     func(cfg *config.Config, args []string) error
}

var commands = []command{
	{"setup", "tongues setup", "Get everything ready, one step at a time", nil},
	{"serve", "tongues serve [--mock]", "Run the helper for the side panel", nil},
	{"auth", "tongues auth", "Sign in a channel so translations can be published", runAuth},
	{"signout", "tongues signout <channel>", "Sign a channel out and delete its sign-in file", runSignOut},
	{"doctor", "tongues doctor", "Check that everything is in place", runDoctor},
	{"video", "tongues video <id>", "Show a video and the languages it has", runVideo},
	{"translate", "tongues translate <id> --langs popular", "Translate a video's title and description", runTranslate},
	{"publish", "tongues publish <id> --langs popular [--apply]", "Translate and add the translations to a video", nil},
	{"save", "tongues save <id> --langs popular", "Translate and save to a folder, no sign-in needed", nil},
	{"channel", "tongues channel scan | localize --langs popular [--apply]", "Work through a whole channel", runChannel},
	{"backups", "tongues backups <id>", "List the backups taken before each publish", nil},
	{"restore", "tongues restore <id> <timestamp>", "Put a video's translations back the way they were", nil},
	{"languages", "tongues languages", "List the languages, most watched first", runLanguages},
	{"keep", "tongues keep add|remove|list [words]", "Words that stay exactly as written", nil},
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage()
		return 0
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for _, c := range commands {
		if c.name != args[0] {
			continue
		}
		if c.run == nil {
			fmt.Fprintf(os.Stderr, "%s is not built yet.\n", c.name)
			return 1
		}
		if err := c.run(cfg, args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, sentence(err.Error()))
			return 1
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "Unknown command %q. Run tongues help to see them all.\n", args[0])
	return 2
}

// sentence capitalizes the first letter of a message for the terminal.
func sentence(msg string) string {
	r, size := utf8.DecodeRuneInString(msg)
	if r == utf8.RuneError {
		return msg
	}
	return string(unicode.ToUpper(r)) + msg[size:]
}

func printUsage() {
	fmt.Println("Channel Tongues: translate your video titles and descriptions on your own machine.")
	fmt.Println()
	fmt.Println("Start here: tongues setup")
	fmt.Println()
	width := 0
	for _, c := range commands {
		width = max(width, len(c.usage))
	}
	for _, c := range commands {
		fmt.Printf("  %s%s  %s\n", c.usage, strings.Repeat(" ", width-len(c.usage)), c.summary)
	}
}
