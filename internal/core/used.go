package core

import (
	"strings"

	"github.com/nazboyko/channel-tongues/internal/langs"
)

// The last pick is kept per channel and once more for the whole helper, so
// a channel with no pick of its own starts from the last pick anywhere.
const (
	usedKey        = "lastLanguages"
	usedKeyChannel = "lastLanguages:"
	// A saved pick this long is the old "All languages" chip, from before the
	// list held only languages the model handled: it starts from the next step.
	usedLeftoverAll = 100
)

// UsedLanguages is the set to tick first: the channel's last pick, else the
// last pick on any channel, else the popular twelve. Codes the list no longer
// offers are left out. An empty channel ID skips the first step.
func (s *Service) UsedLanguages(channelID string) []string {
	keys := []string{usedKey}
	if channelID != "" {
		keys = []string{usedKeyChannel + channelID, usedKey}
	}
	for _, key := range keys {
		saved, ok, err := s.Store.Setting(key)
		if err != nil {
			s.Log.Warn("used languages", "err", err)
		}
		if !ok || strings.Count(saved, ",") >= usedLeftoverAll {
			continue
		}
		if codes := offered(strings.Split(saved, ",")); len(codes) > 0 {
			return codes
		}
	}
	return append([]string(nil), langs.Popular...)
}

// RememberLanguages keeps a pick for the channel and as the last pick on any
// channel. Text pasted without a video has no channel and sets only the last.
func (s *Service) RememberLanguages(channelID string, codes []string) {
	codes = offered(codes)
	if len(codes) == 0 {
		return
	}
	value := strings.Join(codes, ",")
	if channelID != "" {
		if err := s.Store.SetSetting(usedKeyChannel+channelID, value); err != nil {
			s.Log.Warn("remember languages", "err", err)
		}
	}
	if err := s.Store.SetSetting(usedKey, value); err != nil {
		s.Log.Warn("remember languages", "err", err)
	}
}

// offered keeps the codes that are in the list, once each, in table form.
func offered(codes []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range codes {
		l, ok := langs.Get(c)
		if !ok || seen[l.Code] {
			continue
		}
		seen[l.Code] = true
		out = append(out, l.Code)
	}
	return out
}
