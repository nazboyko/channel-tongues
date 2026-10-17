package main

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

func runVideo(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("video", flag.ContinueOnError)
	mock := fs.Bool("mock", false, "use the bundled sample videos")
	apply := fs.Bool("apply", false, "with set-default-language: make the change")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return fmt.Errorf("which video? Usage: tongues video <id or link>")
	}
	a, err := newApp(cfg, *mock)
	if err != nil {
		return err
	}
	defer a.Close()
	if len(pos) >= 2 && pos[1] == "set-default-language" {
		return runSetDefaultLanguage(a, pos, *apply)
	}
	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return err
	}
	v, _, err := a.GetVideo(context.Background(), id)
	if err != nil {
		return friendly(err)
	}
	printVideo(v)
	return nil
}

// printVideo shows the title, the source language and the popular twelve
// with a mark each, then any other language the video has.
func printVideo(v *youtube.Video) {
	fmt.Println(v.Title)
	fmt.Println(videoSummary(v))
	fmt.Println()
	if v.DefaultLanguage == "" {
		fmt.Println("This video has no language set for its title and description, so translations cannot be added yet.")
		fmt.Println("In Studio: open the video > Details > Show more > Title and description language > pick it > Save.")
		fmt.Println()
	}
	shown := map[string]bool{}
	for _, code := range langs.Popular {
		if langs.SameBase(code, v.DefaultLanguage) {
			continue
		}
		printLangLine(v, code)
		shown[langs.Canonical(code)] = true
	}
	var others []string
	for _, code := range v.Languages() {
		if !shown[langs.Canonical(code)] {
			others = append(others, code)
		}
	}
	if len(others) > 0 {
		fmt.Println()
		fmt.Println("Also on this video:")
		for _, code := range others {
			printLangLine(v, code)
		}
	}
}

func printLangLine(v *youtube.Video, code string) {
	name := langs.Name(code)
	if l, ok := v.Localization(code); ok {
		fmt.Printf("  %s %s  ✓ %s\n", pad(name, 18), pad(code, 7), truncate(l.Title, 60))
	} else {
		fmt.Printf("  %s %s  -\n", pad(name, 18), pad(code, 7))
	}
}

// videoSummary is "Channel · English · has 2 languages".
func videoSummary(v *youtube.Video) string {
	parts := []string{}
	if v.ChannelTitle != "" {
		parts = append(parts, v.ChannelTitle)
	}
	if v.DefaultLanguage != "" {
		parts = append(parts, langs.EnglishName(v.DefaultLanguage))
	} else {
		parts = append(parts, "no language set")
	}
	switch n := len(v.Localizations); n {
	case 0:
		parts = append(parts, "no translations yet")
	case 1:
		parts = append(parts, "has 1 language")
	default:
		parts = append(parts, fmt.Sprintf("has %d languages", n))
	}
	return strings.Join(parts, " · ")
}

func runChannel(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: tongues channel scan | tongues channel localize --langs popular [--apply]")
	}
	switch args[0] {
	case "scan":
		return runChannelScan(cfg, args[1:])
	case "localize":
		return runChannelLocalize(cfg, args[1:])
	}
	return fmt.Errorf("unknown channel command %q; use scan or localize", args[0])
}

func runChannelScan(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("channel scan", flag.ContinueOnError)
	mock := fs.Bool("mock", false, "use the bundled sample videos")
	only := fs.String("channel", "", "only this channel (id or name)")
	limit := fs.Int("limit", 0, "only the latest N videos")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := newApp(cfg, *mock)
	if err != nil {
		return err
	}
	defer a.Close()
	chans, err := a.pickChannels(*only)
	if err != nil {
		return err
	}
	ctx := context.Background()
	for i, ch := range chans {
		if i > 0 {
			fmt.Println()
		}
		client, err := a.ClientFor(ctx, ch.ID)
		if err != nil {
			return err
		}
		vids, err := client.ListUploads(ctx, ch.ID, *limit)
		if err != nil {
			return friendly(err)
		}
		printScan(ch, vids)
	}
	return nil
}

func printScan(ch youtube.Channel, vids []*youtube.Video) {
	complete := 0
	for _, v := range vids {
		if len(missingPopular(v)) == 0 {
			complete++
		}
	}
	verb := "have"
	if complete == 1 {
		verb = "has"
	}
	fmt.Printf("%s · %d videos · %d %s all %d popular languages\n", ch.Title, len(vids), complete, verb, len(langs.Popular))
	for _, v := range vids {
		missing := missingPopular(v)
		var state string
		switch {
		case v.DefaultLanguage == "":
			state = "set its language in Studio first"
		case len(missing) == 0:
			state = "all popular languages"
		case len(missing) == len(langs.Popular):
			state = "no translations yet"
		default:
			state = fmt.Sprintf("missing %d: %s", len(missing), strings.Join(missing, " "))
		}
		fmt.Printf("  %s  %s\n", pad(truncate(v.Title, 46), 46), state)
	}
}

// missingPopular lists the popular codes a video lacks (its own language excluded).
func missingPopular(v *youtube.Video) []string {
	var out []string
	for _, code := range langs.Popular {
		if langs.SameBase(code, v.DefaultLanguage) || v.Has(code) {
			continue
		}
		out = append(out, code)
	}
	return out
}

// pickChannels returns the signed-in channels, or the one named by id or title.
func (a *app) pickChannels(only string) ([]youtube.Channel, error) {
	chans, err := a.Channels()
	if err != nil {
		return nil, err
	}
	if len(chans) == 0 {
		return nil, youtube.ErrNotSignedIn
	}
	if only == "" {
		return chans, nil
	}
	for _, c := range chans {
		if c.ID == only || strings.EqualFold(c.Title, only) {
			return []youtube.Channel{c}, nil
		}
	}
	return nil, fmt.Errorf("no signed-in channel called %q; run tongues doctor to see them", only)
}

func runLanguages(cfg *config.Config, args []string) error {
	fmt.Println("Languages, most watched first. The first twelve are picked for you.")
	fmt.Println()
	for _, l := range langs.All("en") {
		mark := " "
		if l.Popular {
			mark = "*"
		}
		fmt.Printf("%s %3d  %s %s %s\n", mark, l.Rank, pad(l.Code, 8), pad(l.Native, 24), l.English)
	}
	return nil
}
