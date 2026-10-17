package main

import (
	"errors"

	"github.com/nazboyko/channel-tongues/internal/config"
)

// Built in the publish phase.
func runSetDefaultLanguage(a *app, pos []string) error {
	return errors.New("set-default-language is not built yet")
}

func runChannelLocalize(cfg *config.Config, args []string) error {
	return errors.New("channel localize is not built yet")
}
