package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nazboyko/channel-tongues/internal/config"
)

func runKeep(cfg *config.Config, args []string) error {
	a, err := newApp(cfg, false)
	if err != nil {
		return err
	}
	defer a.Close()
	if len(args) == 0 {
		args = []string{"list"}
	}
	word := strings.TrimSpace(strings.Join(args[1:], " "))
	switch args[0] {
	case "list":
		words, err := a.Store.KeepWords()
		if err != nil {
			return err
		}
		if len(words) == 0 {
			fmt.Println("No keep words yet. Add one: tongues keep add \"Your Channel Name\"")
			return nil
		}
		fmt.Println("These stay exactly as written in every translation:")
		for _, w := range words {
			fmt.Printf("  %s\n", w)
		}
	case "add":
		if word == "" {
			return errors.New(`which word? Usage: tongues keep add "The Slow Night Hours"`)
		}
		if err := a.Store.AddKeepWord(word); err != nil {
			return err
		}
		fmt.Printf("Kept as written from now on: %s\n", word)
	case "remove":
		removed, err := a.Store.RemoveKeepWord(word)
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("%q is not in the list; run tongues keep list", word)
		}
		fmt.Printf("Removed: %s\n", word)
	default:
		return fmt.Errorf("unknown keep command %q; use add, remove or list", args[0])
	}
	return nil
}
