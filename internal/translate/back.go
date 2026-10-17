package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/nazboyko/channel-tongues/internal/langs"
)

// Back is a literal translation of a translated title and the first line of
// its description, back into the video's own language. It lets a person
// check the meaning of a language they cannot read.
type Back struct {
	Title string `json:"title"`
	Line  string `json:"line"`
}

// BackRequest asks the model for a back-translation.
type BackRequest struct {
	Lang       string // the language of the translation
	SourceLang string // the language to translate back into
	Title      string // translated title, with placeholders
	Line       string // first line of the translated description, with placeholders
}

// NewWordsRequest asks the model which candidate words add meaning.
type NewWordsRequest struct {
	SourceLang string
	Original   string // the whole source: title and description, with placeholders
	Returned   Back   // the back-translation, with placeholders
	Words      []string
}

const backSystem = `Translate the user's text from %s into %s, plainly and faithfully: every word of it, in natural %s, nothing more.
Keep placeholders such as ⟦1⟧, names and numbers exactly as they are. Do not improve, explain, shorten or add anything.
Reply with JSON only: {"title": "...", "line": "..."}`

var backSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"line":{"type":"string"}},"required":["title","line"]}`)

const newWordsSystem = `For each listed word from a back-translation, find the words in the original that say the same thing: the same word, another form of it, a synonym (hut and cabin, advertisements and ads, calm and quiet, light and soft), a word the original's phrase needs in plain language (rain falling on a window for rain on a window), or a plain name for what the original describes (the sound of gulls for gulls call, the shore for waves rolling in, released or uploaded for new videos).
A word that adds a quality, a feeling, a place or a purpose the original does not state (famous, award-winning, Swiss, sponsored, for kids, free) is not a match.
Reply with JSON only: {"matches": [{"word": "...", "original": "..."}]}, one entry per listed word. Copy the matching words from the original into "original", or leave it "" when the original says nothing like it.`

var newWordsSchema = json.RawMessage(`{"type":"object","properties":{"matches":{"type":"array","items":{"type":"object","properties":{"word":{"type":"string"},"original":{"type":"string"}},"required":["word","original"]}}},"required":["matches"]}`)

// BackTranslate asks the local model for a literal back-translation.
func (o *Ollama) BackTranslate(ctx context.Context, req BackRequest) (Back, Stats, error) {
	source := langs.EnglishName(req.SourceLang)
	if req.SourceLang == "" {
		source = "English"
	}
	messages := []chatMessage{
		{Role: "system", Content: fmt.Sprintf(backSystem, langs.EnglishName(req.Lang), source, source)},
		{Role: "user", Content: marshalPlain(Back{Title: req.Title, Line: req.Line})},
	}
	content, st, err := o.chat(ctx, messages, backSchema, 0)
	if err != nil {
		return Back{}, st, err
	}
	var b Back
	if err := json.Unmarshal([]byte(content), &b); err != nil {
		return Back{}, st, &BadAnswer{Raw: content}
	}
	return b, st, nil
}

// NewWords asks the model, for each candidate, which words of the original
// mean the same. A candidate counts as new when the model names none, or
// names words that are not in the original (it cannot vouch with words the
// original does not have).
func (o *Ollama) NewWords(ctx context.Context, req NewWordsRequest) ([]string, error) {
	original := stripPlaceholders(req.Original)
	payload := map[string]any{
		"original":        original,
		"backTranslation": stripPlaceholders(req.Returned.Title) + " / " + stripPlaceholders(req.Returned.Line),
		"words":           req.Words,
	}
	messages := []chatMessage{
		{Role: "system", Content: newWordsSystem},
		{Role: "user", Content: marshalPlain(payload)},
	}
	content, _, err := o.chat(ctx, messages, newWordsSchema, 0)
	if err != nil {
		return nil, err
	}
	var out struct {
		Matches []struct {
			Word     string `json:"word"`
			Original string `json:"original"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return nil, &BadAnswer{Raw: content}
	}
	vouched := map[string]bool{}
	have := wordSet(original)
	for _, m := range out.Matches {
		if inOriginal(m.Original, have) {
			vouched[strings.ToLower(strings.TrimSpace(m.Word))] = true
		}
	}
	var added []string
	for _, w := range req.Words {
		if !vouched[strings.ToLower(w)] {
			added = append(added, w)
		}
	}
	return added, nil
}

// inOriginal reports whether every meaningful word the model named is in the
// original (and it named at least one): the model cannot vouch with "the",
// or with words the original does not have.
func inOriginal(named string, have map[string]bool) bool {
	meaningful := 0
	for _, w := range reWord.FindAllString(strings.ToLower(named), -1) {
		if stopwords[w] || len([]rune(w)) < 2 {
			continue
		}
		meaningful++
		if !hasWord(have, w) {
			return false
		}
	}
	return meaningful > 0
}

// hasWord looks a word up in a word set, allowing for its ending.
func hasWord(set map[string]bool, w string) bool {
	s := stem(w)
	if set[s] || set[w] {
		return true
	}
	// "created" -> "creat" meets "create"; only for words that had an ending.
	return (strings.HasSuffix(w, "ed") || strings.HasSuffix(w, "ing")) && set[s+"e"]
}

// keepListed returns the answers that are on the candidate list, in its order.
func keepListed(answers, candidates []string) []string {
	said := map[string]bool{}
	for _, a := range answers {
		said[strings.ToLower(strings.TrimSpace(a))] = true
	}
	var out []string
	for _, c := range candidates {
		if said[strings.ToLower(c)] {
			out = append(out, c)
		}
	}
	return out
}

// firstLine is the first line of a description that has text.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// stopwords are left out of the comparison: a literal back-translation picks
// its own small words, and they carry no meaning of their own.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`the and for with from that this these those are was were been being has have had
		its your you our their they them his her him she into onto over under about above below than then
		there here when where what which who whom whose how why all any each every some more most other such
		only own same very just can will would could should may might must shall not nor but also too yet
		out off per via upon while during before after again once both few many much one two three
		videos video watch channel please let make made get got set put take see seen use used`) {
		stopwords[w] = true
	}
}

var reWord = regexp.MustCompile(`[\p{L}\p{N}]+(?:['’][\p{L}]+)?`)
var reUnitNumber = regexp.MustCompile(`^\d+(s|sec|secs|seconds?|m|min|mins|minutes?|h|hr|hrs|hours?)$`)

// stem folds the common English word endings, so "sounds" meets "sound"
// and "stopping" meets "stop".
func stem(w string) string {
	w = strings.TrimSuffix(strings.TrimSuffix(w, "'s"), "’s")
	n := len(w)
	switch {
	case strings.HasSuffix(w, "ies") && n > 4:
		return w[:n-3] + "y"
	case strings.HasSuffix(w, "es") && n > 4 && strings.ContainsAny(w[n-3:n-2], "sxz"):
		return w[:n-2]
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && n > 3:
		return w[:n-1]
	case strings.HasSuffix(w, "ing") && n > 5:
		return undouble(w[:n-3])
	case strings.HasSuffix(w, "ed") && n > 4:
		return undouble(w[:n-2])
	}
	return w
}

// undouble turns "stopp" into "stop" and "runn" into "run", but keeps the
// doubled letters English keeps ("fall", "pass", "buzz", "puff").
func undouble(w string) string {
	n := len(w)
	if n > 3 && w[n-1] == w[n-2] && !strings.ContainsRune("aeioulszf", rune(w[n-1])) {
		return w[:n-1]
	}
	return w
}

// unitStems maps a unit written after a number to the word a translator uses.
func unitStems(token string) []string {
	m := reUnitNumber.FindStringSubmatch(token)
	if m == nil {
		return nil
	}
	switch u := m[1]; {
	case strings.HasPrefix(u, "s"):
		return []string{"second", "sec"}
	case strings.HasPrefix(u, "m"):
		return []string{"minute", "min"}
	default:
		return []string{"hour", "hr"}
	}
}

// wordSet collects the stems of a text, with units spelled out.
func wordSet(text string) map[string]bool {
	set := map[string]bool{}
	for _, w := range reWord.FindAllString(strings.ToLower(stripPlaceholders(text)), -1) {
		s := stem(w)
		set[s] = true
		if strings.HasSuffix(w, "ed") || strings.HasSuffix(w, "ing") {
			set[s+"e"] = true // "created" -> "creat" also stands for "create"
		}
		for _, u := range unitStems(w) {
			set[u] = true
		}
	}
	return set
}

// CandidateWords lists the words of a back-translation that its original
// does not have, field by field, ignoring small words, numbers and word
// endings. It is the first, mechanical half of the added-word test.
func CandidateWords(original, returned Back) []string {
	t, l := fieldCandidates(original, returned)
	return append(t, l...)
}

// fieldCandidates returns the candidates of the title and of the line apart;
// a word already listed for the title is not listed again for the line.
func fieldCandidates(original, returned Back) (title, line []string) {
	seen := map[string]bool{}
	for i, f := range [][2]string{{original.Title, returned.Title}, {original.Line, returned.Line}} {
		have := wordSet(f[0])
		for _, w := range reWord.FindAllString(stripPlaceholders(f[1]), -1) {
			lw := strings.ToLower(w)
			s := stem(lw)
			switch {
			case len([]rune(lw)) < 3, stopwords[lw], stopwords[s] && s+"s" == lw, isNumber(lw), reUnitNumber.MatchString(lw):
				continue
			case hasWord(have, lw):
				continue
			case seen[s]:
				continue
			}
			seen[s] = true
			if i == 0 {
				title = append(title, w)
			} else {
				line = append(line, w)
			}
		}
	}
	return title, line
}

func isNumber(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return w != ""
}
