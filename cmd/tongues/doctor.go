package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

// check is one line of the doctor report. fix is empty when all is well.
type check struct {
	name  string
	state string
	fix   string
}

func runDoctor(cfg *config.Config, args []string) error {
	checks := doctorChecks(context.Background(), cfg)
	width := 0
	for _, c := range checks {
		width = max(width, len(c.name))
	}
	problems := 0
	for _, c := range checks {
		mark := "ok "
		if c.fix != "" {
			mark = "!! "
			problems++
		}
		fmt.Printf("%s%s%s  %s\n", mark, c.name, strings.Repeat(" ", width-len(c.name)), c.state)
		if c.fix != "" {
			fmt.Printf("   %s  %s\n", strings.Repeat(" ", width), c.fix)
		}
	}
	fmt.Println()
	if g := cfg.GuardLine(); g != "" {
		fmt.Println(g)
	}
	if problems == 0 {
		fmt.Println("Everything is in place.")
	} else {
		fmt.Printf("%d thing(s) to fix. Each line above says how.\n", problems)
	}
	return nil
}

func doctorChecks(ctx context.Context, cfg *config.Config) []check {
	var out []check

	if fi, err := os.Stat(cfg.DataDir); err == nil && fi.IsDir() {
		out = append(out, check{"Data folder", config.Display(cfg.DataDir), ""})
	} else {
		out = append(out, check{"Data folder", "not created yet", "Run: tongues setup"})
	}

	if _, err := os.Stat(cfg.Path("client_secret.json")); err == nil {
		out = append(out, check{"Google project", "sign-in file found", ""})
	} else {
		out = append(out, check{"Google project", "not set up (only needed to publish)", "Run: tongues setup"})
	}

	chans, _ := (&youtube.Auth{DataDir: cfg.DataDir}).Channels()
	if len(chans) == 0 {
		out = append(out, check{"Signed in", "no channel yet (only needed to publish)", "Run: tongues auth"})
	} else {
		names := make([]string, len(chans))
		for i, c := range chans {
			names[i] = c.Title
		}
		out = append(out, check{"Signed in", strings.Join(names, ", "), ""})
	}

	o := translate.NewOllama(cfg.OllamaURL, cfg.Model)
	var state translate.ModelState
	if cfg.Model == "" {
		state = o.PickModel(ctx)
	} else {
		state, _ = o.Health(ctx)
	}
	switch state {
	case translate.ModelReady:
		out = append(out, check{"Ollama", "running", ""}, check{"Model", o.Model + " ready", ""})
	case translate.ModelMissing:
		out = append(out, check{"Ollama", "running", ""}, check{"Model", o.Model + " is not downloaded", "Run: ollama pull " + o.Model})
	default:
		out = append(out, check{"Ollama", "not running", "Start the Ollama app, or run: ollama serve"})
	}

	if conn, err := net.DialTimeout("tcp", cfg.Addr(), 500*time.Millisecond); err == nil {
		conn.Close()
		out = append(out, check{"Helper", "running on " + cfg.Addr(), ""})
	} else {
		out = append(out, check{"Helper", "not running (the side panel needs it)", "Run: tongues serve"})
	}

	node := "not found"
	if b, err := exec.Command("node", "-v").Output(); err == nil {
		node = strings.TrimSpace(string(b))
	}
	out = append(out, check{"Build tools", fmt.Sprintf("%s, node %s (only needed to build from source)", runtime.Version(), node), ""})
	return out
}
