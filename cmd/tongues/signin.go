package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/nazboyko/channel-tongues/internal/config"
	"github.com/nazboyko/channel-tongues/internal/youtube"
)

func runAuth(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("auth", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print the link instead of opening the browser")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	app, err := newApp(cfg, false)
	if err != nil {
		return err
	}
	defer app.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ch, err := signIn(ctx, app.Auth, *noBrowser)
	if err != nil {
		return err
	}
	app.SignedIn(ch) // the channel name becomes a keep word
	fmt.Printf("Signed in: %s.\n", ch.Title)
	fmt.Println("To add another channel, run tongues auth again and pick it.")
	return nil
}

// signIn runs the browser sign-in and prints what the person will see.
func signIn(ctx context.Context, a *youtube.Auth, noBrowser bool) (youtube.Channel, error) {
	login, err := a.StartLogin(ctx)
	if err != nil {
		return youtube.Channel{}, err
	}
	fmt.Println("Sign in with the Google account that owns your channel, then pick the channel.")
	fmt.Println("Google may say it hasn't verified this app. It is your own Google project, so click Continue.")
	fmt.Println()
	if noBrowser || openBrowser(login.URL) != nil {
		fmt.Println("Open this link in your browser:")
	} else {
		fmt.Println("Your browser is opening. If it did not, open this link:")
	}
	fmt.Println(login.URL)
	fmt.Println()
	fmt.Println("Waiting for the browser...")
	return login.Wait(ctx)
}

func runSignOut(cfg *config.Config, args []string) error {
	a := &youtube.Auth{DataDir: cfg.DataDir}
	chans, err := a.Channels()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		if len(chans) == 0 {
			fmt.Println("No channel is signed in.")
			return nil
		}
		fmt.Println("Which channel? Run one of these:")
		for _, c := range chans {
			fmt.Printf("  tongues signout %s    # %s\n", c.ID, c.Title)
		}
		return nil
	}
	want := strings.Join(args, " ")
	for _, c := range chans {
		if c.ID == want || strings.EqualFold(c.Title, want) {
			if err := a.SignOut(context.Background(), c.ID); err != nil {
				return err
			}
			fmt.Printf("Signed out: %s. Its sign-in file is deleted and Google has been told.\n", c.Title)
			return nil
		}
	}
	if _, err := a.Load(want); err != nil && strings.Contains(err.Error(), "damaged") {
		// A damaged sign-in file is not listed by name; remove it by id.
		if err := a.SignOut(context.Background(), want); err == nil {
			fmt.Println("Removed the damaged sign-in file.")
			return nil
		}
	}
	return fmt.Errorf("no signed-in channel called %q; run tongues signout to see them", want)
}
