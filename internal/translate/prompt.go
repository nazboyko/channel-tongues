package translate

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/nazboyko/channel-tongues/internal/langs"
)

// markets names where the viewers of a language live, so the title reads the
// way a creator there would write it.
var markets = map[string]string{
	"es":      "Spanish-speaking countries (Mexico, Spain, Latin America)",
	"es-419":  "Latin America",
	"pt-BR":   "Brazil",
	"pt-PT":   "Portugal",
	"hi":      "India",
	"id":      "Indonesia",
	"ja":      "Japan",
	"de":      "Germany, Austria and Switzerland",
	"vi":      "Vietnam",
	"tr":      "Turkey",
	"ko":      "South Korea",
	"fr":      "France and other French-speaking countries",
	"ar":      "Arabic-speaking countries",
	"th":      "Thailand",
	"it":      "Italy",
	"pl":      "Poland",
	"fil":     "the Philippines",
	"zh-Hant": "Taiwan and Hong Kong",
	"zh-Hans": "mainland China and Singapore",
	"ur":      "Pakistan",
	"nl":      "the Netherlands and Belgium",
	"ms":      "Malaysia",
	"bn":      "Bangladesh and India",
	"ta":      "Tamil Nadu and Sri Lanka",
	"te":      "Andhra Pradesh and Telangana",
}

func market(lang string) string {
	if m, ok := markets[lang]; ok {
		return m
	}
	return langs.EnglishName(lang) + " speakers"
}

// VideoKind describes the video from its own text: the title and the first
// sentence of the description. Nothing is guessed beyond the source.
func VideoKind(title, description string) string {
	first := strings.TrimSpace(firstSentence(stripPlaceholders(description)))
	t := strings.TrimSpace(stripPlaceholders(title))
	switch {
	case first == "":
		return t
	case t == "":
		return first
	}
	return t + ". " + first
}

func firstSentence(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.IndexAny(line, ".!?"); i > 0 {
			line = line[:i+1]
		}
		if utf8.RuneCountInString(line) > 200 {
			r := []rune(line)
			line = string(r[:200])
		}
		return line
	}
	return ""
}

// SystemPrompt is the fixed, calm style every translation follows. The
// title limit and the original title's length come first, in the characters
// the model sees (a placeholder counts as written), so it shortens a long
// title on the first answer instead of after a retry.
func SystemPrompt(sourceLang, lang, kind string, titleLimit, titleLen int) string {
	target := langs.EnglishName(lang)
	source := langs.EnglishName(sourceLang)
	if sourceLang == "" {
		source = "the original language"
	}
	if titleLimit <= 0 {
		titleLimit = MaxTitle - 5
	}
	return fmt.Sprintf(`You translate a YouTube video's title and description from %s into %s, for viewers in %s.

The title must be at most %d characters; the original title has %d. If your title would be longer, leave out its least important words, never the main subject.

What the video is (for context only; do not copy it into the title): %s

Rules:
- Keep every placeholder such as ⟦1⟧ exactly as written, each one exactly once, in the matching place. They stand for links, hashtags, handles, timestamps and names that must not change.
- The description comes as a list of lines. Translate each line on its own and return exactly as many lines, in the same order. An empty line stays empty.
- Title: translate the title and nothing else, part for part, within that length. Never move words from the description into the title, even when the title is short. Use the words a creator in %s would use, so a viewer searching in %s finds it. Capitalize it the way titles are normally written in %s, not in English title case.
- Translate the meaning, not word by word, in a calm, natural tone.
- Add nothing and drop nothing (only title words left out to fit the length): no new claims, keywords, hashtags or emoji.
- Keep emoji and the separators between title parts (such as · or |) as they are.
- No exclamation marks unless the original has them.
- Reply with JSON only: {"title": "...", "lines": ["...", "..."]}`,
		source, target, market(lang), titleLimit, titleLen, kind, market(lang), target, target)
}

// wire is the shape sent to and received from the model: the description as
// a list of lines, so the line count can be held by the schema itself.
type wire struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
}

func toWire(o Output) wire {
	return wire{Title: o.Title, Lines: strings.Split(o.Description, "\n")}
}

func (w wire) output() Output {
	return Output{Title: w.Title, Description: strings.Join(w.Lines, "\n")}
}

// UserMessage carries the protected source fields as JSON.
func UserMessage(src Output) string { return marshalPlain(toWire(src)) }

// marshalPlain encodes JSON without HTML escaping, so "&" stays "&" for the model.
func marshalPlain(v any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimRight(b.String(), "\n")
}

// schemaFor constrains the answer to a title and exactly n lines.
func schemaFor(n int) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"title":{"type":"string"},"lines":{"type":"array","items":{"type":"string"},"minItems":%d,"maxItems":%d}},"required":["title","lines"]}`, n, n))
}

// RetryMessage asks for a corrected answer with the exact problem.
func RetryMessage(p *Problem) string {
	if p.Shorten {
		return p.Feedback + ` Change only the title. Reply with the full JSON again.`
	}
	return "That answer has a problem: " + p.Feedback + " Fix it and reply with the full corrected JSON."
}

var detectSchema = json.RawMessage(`{"type":"object","properties":{"code":{"type":"string"}},"required":["code"]}`)

const detectSystem = `Identify the language of the text. Reply with JSON only: {"code": "<ISO 639-1 code>"}, for example {"code": "es"}.`
