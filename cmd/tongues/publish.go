package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/core"
	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/translate"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

func runPublish(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	f := addTranslateFlags(fs)
	apply := fs.Bool("apply", false, "write the translations to the video (without it: a dry run)")
	replaceSpec := fs.String("replace", "", "languages whose existing translation may be replaced, e.g. de,fr")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("which video? Usage: tongues publish <id or link> --langs popular [--apply]")
	}
	a, err := newApp(cfg, *f.mock)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return err
	}
	v, _, err := a.GetVideo(ctx, id)
	if err != nil {
		return friendly(err)
	}
	if v.DefaultLanguage == "" {
		return youtube.ErrNoDefaultLanguage
	}
	writer, err := a.Writer(ctx, v)
	if err != nil {
		return err
	}
	if *apply && writer.Allowed != nil {
		if err := writer.Allowed(v.ID); err != nil {
			return err // refuse before spending time on translation
		}
	}
	targets, err := a.pickLanguages(*f.langs, v.DefaultLanguage, v.ChannelID)
	if err != nil {
		return err
	}
	eng, o := a.engine(ctx, f)
	if err := checkModel(ctx, o); err != nil {
		return err
	}
	fmt.Println(v.Title)
	fmt.Println(videoSummary(v))
	fmt.Printf("\nTranslating into %s...\n", plural(len(targets), "language"))
	results := eng.Run(ctx, translate.Source{Title: v.Title, Description: v.Description, Language: v.DefaultLanguage}, targets, a.KeepWords(), nil)

	ours := map[string]youtube.Localization{}
	notes := map[string]string{}
	for _, r := range results {
		switch r.Status {
		case translate.StatusReady:
			ours[r.Lang] = youtube.Localization{Title: r.Title, Description: r.Description}
		default:
			notes[r.Lang] = r.Note
		}
	}
	replace := map[string]bool{}
	for _, c := range strings.Split(*replaceSpec, ",") {
		if c = strings.TrimSpace(c); c != "" {
			replace[langs.Canonical(c)] = true
		}
	}
	unlock := a.LockVideo(v.ID)
	defer unlock()
	v, plan, err := writer.Prepare(ctx, v.ID, ours, replace) // a fresh read: translating took a while
	if err != nil {
		return friendly(err)
	}
	fmt.Println()
	printPlan(plan, results, notes)

	writes := 0
	for _, c := range plan.Changes {
		if c.Outcome == youtube.OutcomeAdded || c.Outcome == youtube.OutcomeUpdated || c.Outcome == youtube.OutcomeReplaced {
			writes++
		}
	}
	if writes == 0 {
		fmt.Println("\nNothing new to publish.")
		return nil
	}
	if !*apply {
		fmt.Printf("\nDry run: nothing was changed. %s would be published.\n", plural(writes, "language"))
		fmt.Println("To publish, run the same command with --apply.")
		return nil
	}
	res, err := writer.Publish(ctx, v, plan, "")
	if err != nil {
		if res != nil && res.BackupID != "" {
			fmt.Printf("A backup was taken first: %s\n", res.BackupID)
		}
		return err
	}
	printOutcome(v.ID, res)
	return nil
}

func outcomeWords(o youtube.Outcome) string {
	switch o {
	case youtube.OutcomeAdded:
		return "add"
	case youtube.OutcomeUpdated:
		return "update (ours from last time)"
	case youtube.OutcomeReplaced:
		return "replace"
	case youtube.OutcomeKept:
		return "keep (the video has its own; --replace to overwrite)"
	case youtube.OutcomeSame:
		return "already there"
	case youtube.OutcomeFailed:
		return "did not read back"
	case youtube.OutcomeRemoved:
		return "remove"
	}
	return string(o)
}

func printPlan(plan *youtube.Plan, results []translate.Result, notes map[string]string) {
	shown := map[string]bool{}
	for _, c := range plan.Changes {
		shown[c.Lang] = true
		fmt.Printf("  %s %s %s\n", pad(langs.Name(c.Lang), 18), pad(c.Lang, 7), outcomeWords(c.Outcome))
	}
	for _, r := range results {
		if shown[r.Lang] {
			continue
		}
		fmt.Printf("  %s %s not published, check this one first: %s\n", pad(langs.Name(r.Lang), 18), pad(r.Lang, 7), notes[r.Lang])
	}
}

func printOutcome(videoID string, res *youtube.PublishResult) {
	fmt.Println()
	done, failed := 0, 0
	for _, c := range res.Changes {
		switch c.Outcome {
		case youtube.OutcomeAdded, youtube.OutcomeUpdated, youtube.OutcomeReplaced:
			done++
			fmt.Printf("  ✓ %s %s\n", pad(langs.Name(c.Lang), 18), c.Lang)
		case youtube.OutcomeFailed:
			failed++
			fmt.Printf("  ✗ %s %s did not read back; run tongues video %s to look\n", pad(langs.Name(c.Lang), 18), c.Lang, videoID)
		}
	}
	fmt.Printf("\nPublished %s. Backup taken first: %s\n", plural(done, "language"), res.BackupID)
	fmt.Printf("Undo: tongues restore %s %s\n", videoID, res.BackupID)
	fmt.Printf("See them in Studio: https://studio.youtube.com/video/%s/translations\n", videoID)
	if failed > 0 {
		fmt.Printf("%s did not show up when read back.\n", plural(failed, "language"))
	}
}

func runBackups(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("backups", flag.ContinueOnError)
	mock := fs.Bool("mock", false, "use the bundled sample videos")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("which video? Usage: tongues backups <id or link>")
	}
	a, err := newApp(cfg, *mock)
	if err != nil {
		return err
	}
	defer a.Close()
	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return err
	}
	list, err := a.Backups.List(id)
	if err != nil {
		return err
	}
	if len(list) == 0 {
		fmt.Println("No backups for this video yet. One is taken before every publish.")
		return nil
	}
	for _, b := range list {
		fmt.Printf("  %s  %s  %s before, %s\n", b.ID, b.TakenAt.Local().Format("Jan 2 15:04"), plural(b.Languages, "language"), b.Reason)
	}
	fmt.Printf("\nPut one back with: tongues restore %s <name>\n", pos[0])
	return nil
}

func runRestore(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	mock := fs.Bool("mock", false, "use the bundled sample videos")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		return errors.New("usage: tongues restore <id or link> <backup name>; tongues backups <id> lists the names")
	}
	a, err := newApp(cfg, *mock)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx := context.Background()
	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return err
	}
	v, _, err := a.GetVideo(ctx, id)
	if err != nil {
		return friendly(err)
	}
	writer, err := a.Writer(ctx, v)
	if err != nil {
		return err
	}
	res, err := writer.Restore(ctx, id, pos[1])
	if err != nil {
		return err
	}
	if !res.Wrote {
		fmt.Println("The video already looks exactly like that backup. Nothing was changed.")
		return nil
	}
	for _, c := range res.Changes {
		if c.Outcome != youtube.OutcomeSame {
			fmt.Printf("  %s %s %s\n", pad(langs.Name(c.Lang), 18), pad(c.Lang, 7), outcomeWords(c.Outcome))
		}
	}
	fmt.Printf("\nRestored backup %s. The state just before this restore is saved as %s.\n", pos[1], res.BackupID)
	return nil
}

func runSave(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("save", flag.ContinueOnError)
	f := addTranslateFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	a, err := newApp(cfg, *f.mock)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	src, err := a.loadSource(ctx, f, pos)
	if err != nil {
		return err
	}
	channelID, channel, videoID := "", "", ""
	if src.video != nil {
		channelID, channel, videoID = src.video.ChannelID, src.video.ChannelTitle, src.video.ID
	}
	targets, err := a.pickLanguages(*f.langs, src.text.Language, channelID)
	if err != nil {
		return err
	}
	eng, o := a.engine(ctx, f)
	if err := checkModel(ctx, o); err != nil {
		return err
	}
	fmt.Printf("Translating into %s...\n\n", plural(len(targets), "language"))
	results := eng.Run(ctx, src.text, targets, a.KeepWords(), nil)
	printResults(results, false)
	dir, err := a.Save(core.SaveRequest{Channel: channel, VideoID: videoID, SourceTitle: src.text.Title, Results: results})
	if err != nil {
		return err
	}
	fmt.Printf("\nSaved: %s\n", dir+string(os.PathSeparator)+"translations.md")
	fmt.Println("Paste each one into the video's Languages tab in YouTube Studio.")
	return nil
}

func runSetDefaultLanguage(a *app, pos []string, apply bool) error {
	if len(pos) < 3 {
		return errors.New("usage: tongues video <id> set-default-language <code> [--apply], for example en")
	}
	ctx := context.Background()
	id, err := a.ResolveVideo(pos[0])
	if err != nil {
		return err
	}
	lang, ok := langs.Get(pos[2])
	if !ok {
		return fmt.Errorf("%q is not a language code this tool knows; run tongues languages", pos[2])
	}
	v, _, err := a.GetVideo(ctx, id)
	if err != nil {
		return friendly(err)
	}
	if v.DefaultLanguage == lang.Code {
		fmt.Printf("The video's language is already %s.\n", lang.English)
		return nil
	}
	writer, err := a.Writer(ctx, v)
	if err != nil {
		return err
	}
	if !apply {
		fmt.Printf("This sets the language of the title and description to %s. The title, description and tags are sent back exactly as they are, after a backup.\n", lang.English)
		fmt.Println("Dry run: nothing was changed. Run the same command with --apply.")
		return nil
	}
	backup, err := writer.SetDefaultLanguage(ctx, id, lang.Code)
	if err != nil {
		return err
	}
	fmt.Printf("Done: the video's language is now %s. Backup taken first: %s\n", lang.English, backup)
	return nil
}

func runChannelLocalize(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("channel localize", flag.ContinueOnError)
	f := addTranslateFlags(fs)
	apply := fs.Bool("apply", false, "write the translations (without it: a dry run that prints the plan)")
	only := fs.String("channel", "", "only this channel (id or name)")
	limit := fs.Int("limit", 0, "only the latest N videos per channel")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	a, err := newApp(cfg, *f.mock)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	chans, err := a.pickChannels(*only)
	if err != nil {
		return err
	}

	type item struct {
		video   *youtube.Video
		missing []string
	}
	var work []item
	skipped := 0
	for _, ch := range chans {
		client, err := a.ClientFor(ctx, ch.ID)
		if err != nil {
			return err
		}
		vids, err := client.ListUploads(ctx, ch.ID, *limit)
		if err != nil {
			return friendly(err)
		}
		for _, v := range vids {
			if v.DefaultLanguage == "" {
				skipped++
				continue
			}
			wanted, err := a.pickLanguages(*f.langs, v.DefaultLanguage, ch.ID)
			if err != nil {
				return err
			}
			var missing []string
			for _, c := range wanted {
				if !v.Has(c) {
					missing = append(missing, c)
				}
			}
			if len(missing) > 0 {
				work = append(work, item{v, missing})
			}
		}
	}
	ledger := &youtube.Ledger{Store: a.Store}
	left, _ := ledger.VideosLeftToday()
	fmt.Printf("%s to translate", plural(len(work), "video"))
	if skipped > 0 {
		fmt.Printf(", %s skipped (no language set in Studio)", plural(skipped, "video"))
	}
	fmt.Printf(". Only missing languages are added; existing translations are never touched.\n\n")
	for _, w := range work {
		fmt.Printf("  %s  adds %d: %s\n", pad(truncate(w.video.Title, 46), 46), len(w.missing), strings.Join(w.missing, " "))
	}
	if len(work) == 0 {
		fmt.Println("Every video already has these languages.")
		return nil
	}
	fmt.Printf("\nYou can publish about %s more today (YouTube's daily limit resets at midnight Pacific).\n", plural(left, "video"))
	if !*apply {
		fmt.Println("Dry run: nothing was changed. To do it, run the same command with --apply.")
		return nil
	}

	eng, o := a.engine(ctx, f)
	if err := checkModel(ctx, o); err != nil {
		return err
	}
	keep := a.KeepWords()
	published, toCheck := 0, 0
	for i, w := range work {
		if ctx.Err() != nil {
			fmt.Println("\nStopped. Run the same command again to continue where it left off.")
			return nil
		}
		if left, _ := ledger.VideosLeftToday(); left == 0 {
			reset := youtube.NextReset(time.Now()).Local().Format("Jan 2 15:04")
			fmt.Printf("\nStopped at today's limit. Run the same command again after %s to continue.\n", reset)
			return nil
		}
		v := w.video
		writer, err := a.Writer(ctx, v)
		if err != nil {
			return err
		}
		if writer.Allowed != nil {
			if err := writer.Allowed(v.ID); err != nil {
				fmt.Printf("  [%d/%d] %s  skipped: %v\n", i+1, len(work), truncate(v.Title, 40), err)
				continue
			}
		}
		results := eng.Run(ctx, translate.Source{Title: v.Title, Description: v.Description, Language: v.DefaultLanguage}, w.missing, keep, nil)
		ours := map[string]youtube.Localization{}
		checks := 0
		for _, r := range results {
			if r.Status == translate.StatusReady {
				ours[r.Lang] = youtube.Localization{Title: r.Title, Description: r.Description}
			} else {
				checks++
			}
		}
		toCheck += checks
		line := fmt.Sprintf("  [%d/%d] %s", i+1, len(work), pad(truncate(v.Title, 40), 40))
		if len(ours) == 0 {
			fmt.Printf("%s  nothing ready; %d to check\n", line, checks)
			continue
		}
		fresh, plan, err := writer.PrepareFrom(v, ours, nil) // Publish re-reads and refuses if the video changed
		if err != nil {
			fmt.Printf("%s  %v\n", line, err)
			continue
		}
		res, err := writer.Publish(ctx, fresh, plan, "")
		if err != nil {
			fmt.Printf("%s  %v\n", line, err)
			var ae *youtube.APIError
			if errors.As(err, &ae) && (ae.Reason == "quotaExceeded" || ae.Reason == "dailyLimitExceeded") {
				fmt.Println("\nStopped at today's limit. Run the same command again tomorrow to continue.")
				return nil
			}
			continue
		}
		added := 0
		for _, c := range res.Changes {
			if c.Outcome == youtube.OutcomeAdded {
				added++
			}
		}
		published++
		fmt.Printf("%s  added %d", line, added)
		if checks > 0 {
			fmt.Printf(", %d to check (tongues publish %s --langs ... to look)", checks, v.ID)
		}
		fmt.Printf(", backup %s\n", res.BackupID)
	}
	fmt.Printf("\nDone: %s updated, %s left to check by hand.\n", plural(published, "video"), plural(toCheck, "language"))
	return nil
}
