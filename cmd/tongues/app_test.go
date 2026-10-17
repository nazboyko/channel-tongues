package main

import (
	"flag"
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

func TestPickAllLanguages(t *testing.T) {
	a, err := newApp(&config.Config{DataDir: t.TempDir()}, true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	all, err := a.pickLanguages("all", "en", "")
	if err != nil || len(all) < 160 {
		t.Fatalf("got %d languages, %v", len(all), err)
	}
	for _, c := range all {
		if c == "en" || c == "en-GB" || c == "en-IN" {
			t.Fatalf("the source language is left out, got %s", c)
		}
	}
}
