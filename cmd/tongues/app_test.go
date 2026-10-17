package main

import (
	"flag"
	"strings"
	"testing"

	"github.com/nazboyko/channel-tongues/internal/config"
)

func TestParseArgsInterspersed(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "")
	langs := fs.String("langs", "", "")
	pos, err := parseArgs(fs, []string{"abcdefghijk", "--langs", "es,de", "--apply"})
	if err != nil || len(pos) != 1 || pos[0] != "abcdefghijk" || !*apply || *langs != "es,de" {
		t.Fatalf("got %v %v %v %q", pos, err, *apply, *langs)
	}
}

func TestParseArgsKeepsIDsThatStartWithADash(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "")
	pos, err := parseArgs(fs, []string{"-Abcdefghij", "--apply"})
	if err != nil || len(pos) != 1 || pos[0] != "-Abcdefghij" || !*apply {
		t.Fatalf("got %v %v", pos, err)
	}
}

func TestDisplayWidth(t *testing.T) {
	if displayWidth("日本語") != 6 || displayWidth("abc") != 3 || displayWidth("हिन्दी") >= 6 {
		t.Fatal("width")
	}
}

func TestPickUsedLanguages(t *testing.T) {
	a, err := newApp(&config.Config{DataDir: t.TempDir()}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	got, err := a.pickLanguages("used", "en", "UCa")
	if err != nil || len(got) != 12 || got[0] != "es" {
		t.Fatalf("nothing picked yet: the popular twelve, got %v %v", got, err)
	}
	a.RememberLanguages("UCa", []string{"es", "it", "fr"})
	if got, _ := a.pickLanguages("used", "en", "UCa"); strings.Join(got, ",") != "es,it,fr" {
		t.Fatalf("the channel's last pick, got %v", got)
	}
	if got, _ := a.pickLanguages("used", "es", "UCnew"); strings.Join(got, ",") != "it,fr" {
		t.Fatalf("a new channel uses the last pick on any channel, without its source language, got %v", got)
	}
	if _, err := a.pickLanguages("all", "en", ""); err == nil {
		t.Fatal("there is no all: the list is only languages the model handled")
	}
}
