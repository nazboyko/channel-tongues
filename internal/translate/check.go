package translate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nazboyko/channel-tongues/internal/langs"
)

const (
	MaxTitle       = 100  // characters
	MaxDescription = 5000 // bytes of UTF-8
)

// Problem is a failed check: a reason for the person ("the title is 112
// characters, YouTube allows 100") and feedback for the model's retry.
type Problem struct {
	Reason   string
	Feedback string
	// Shorten marks a title that is only too long, so the retry asks for a
	// shorter title and nothing else.
	Shorten bool
}

// Output is a model answer for one language, with placeholders still in it.
type Output struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Check runs every check from the translation design on one answer and
// returns the first problem, or nil. src holds the protected source fields.
func Check(src Output, out Output, spans []Span, lang string) *Problem {
	checks := []func() *Problem{
		func() *Problem { return checkNotEmpty(src, out) },
		func() *Problem { return checkTitleOneLine(out.Title) },
		func() *Problem { return checkPlaceholders("title", src.Title, out.Title, spans) },
		func() *Problem { return checkPlaceholders("description", src.Description, out.Description, spans) },
		func() *Problem { return checkLines(src.Description, out.Description) },
		func() *Problem { return checkLinePlaceholders(src.Description, out.Description) },
		func() *Problem { return checkAddedSpans(out) },
		func() *Problem { return checkBrackets(src, out, spans) },
		func() *Problem { return checkTitleLength(out.Title, spans) },
		func() *Problem { return checkTitleGrowth(src.Title, out.Title, lang) },
		func() *Problem { return checkDescriptionLength(out.Description, spans) },
		func() *Problem { return checkAngles(out, spans) },
		func() *Problem { return checkTranslated(src.Title, out.Title, lang) },
		func() *Problem { return checkDescriptionTranslated(src.Description, out.Description, lang) },
		func() *Problem { return checkScript(out, lang) },
		func() *Problem { return checkAddedLatin(src, out, lang) },
	}
	for _, c := range checks {
		if p := c(); p != nil {
			return p
		}
	}
	return nil
}

// checkPlaceholders: every placeholder of the field is present exactly once,
// and the model invented none.
func checkPlaceholders(field, src, out string, spans []Span) *Problem {
	want := placeholderCounts(src)
	got := placeholderCounts(out)
	byID := map[int]Span{}
	for _, s := range spans {
		byID[s.ID] = s
	}
	ids := make([]int, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		switch n := got[id]; {
		case n == 0:
			return &Problem{
				Reason:   fmt.Sprintf("%s went missing from the %s", byID[id].describe(), field),
				Feedback: fmt.Sprintf("The %s lost the placeholder %s. Put %s back exactly once.", field, Placeholder(id), Placeholder(id)),
			}
		case n > 1:
			return &Problem{
				Reason:   fmt.Sprintf("%s appears %d times in the %s", byID[id].describe(), n, field),
				Feedback: fmt.Sprintf("The %s has the placeholder %s %d times. Use it exactly once.", field, Placeholder(id), n),
			}
		}
	}
	for id := range got {
		if want[id] == 0 {
			return &Problem{
				Reason:   fmt.Sprintf("the %s has something the original does not have", field),
				Feedback: fmt.Sprintf("The %s contains %s, which is not in the original %s. Remove it.", field, Placeholder(id), field),
			}
		}
	}
	return nil
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

func checkLines(src, out string) *Problem {
	if strings.TrimSpace(src) == "" {
		if strings.TrimSpace(out) != "" {
			return &Problem{
				Reason:   "the description has text the original does not have",
				Feedback: "The original description is empty. Leave the description empty.",
			}
		}
		return nil
	}
	want, got := lineCount(src), lineCount(out)
	if want != got {
		return &Problem{
			Reason:   fmt.Sprintf("the description has %d lines instead of %d", got, want),
			Feedback: fmt.Sprintf("The description must have exactly %d lines, one for each line of the original, including the empty ones. Yours has %d.", want, got),
		}
	}
	srcLines, outLines := strings.Split(src, "\n"), strings.Split(out, "\n")
	for i := range srcLines {
		blankSrc, blankOut := strings.TrimSpace(srcLines[i]) == "", strings.TrimSpace(outLines[i]) == ""
		switch {
		case blankSrc && !blankOut:
			return &Problem{
				Reason:   fmt.Sprintf("line %d of the description should be empty", i+1),
				Feedback: fmt.Sprintf("Line %d of the original is empty, so line %d must be empty too. Translate each line in place.", i+1, i+1),
			}
		case !blankSrc && blankOut:
			return &Problem{
				Reason:   fmt.Sprintf("line %d of the description went missing", i+1),
				Feedback: fmt.Sprintf("Line %d is empty but the original line %d has text. Translate it.", i+1, i+1),
			}
		}
	}
	return nil
}

// checkLinePlaceholders: every line keeps its own placeholders, so two links
// cannot swap lines.
func checkLinePlaceholders(src, out string) *Problem {
	srcLines, outLines := strings.Split(src, "\n"), strings.Split(out, "\n")
	if len(srcLines) != len(outLines) {
		return nil // checkLines reports this
	}
	for i := range srcLines {
		want, got := placeholderCounts(srcLines[i]), placeholderCounts(outLines[i])
		if len(want) != len(got) {
			return lineMoved(i)
		}
		for id, n := range want {
			if got[id] != n {
				return lineMoved(i)
			}
		}
	}
	return nil
}

func lineMoved(i int) *Problem {
	return &Problem{
		Reason:   fmt.Sprintf("a link or tag moved to another line (line %d)", i+1),
		Feedback: fmt.Sprintf("Line %d must keep exactly the placeholders of the original line %d, no more and no fewer.", i+1, i+1),
	}
}

// checkAddedSpans: any link, hashtag, handle, e-mail address or timestamp
// still in the answer is new, because every real one became a placeholder.
func checkAddedSpans(out Output) *Problem {
	for field, text := range map[string]string{"title": out.Title, "description": out.Description} {
		var p Protected
		p.Protect(stripPlaceholders(text), nil)
		if len(p.Spans) > 0 {
			sp := p.Spans[0]
			return &Problem{
				Reason:   fmt.Sprintf("the %s adds %s, which the original does not have", field, sp.describe()),
				Feedback: fmt.Sprintf("The %s contains %q, which is not in the original. Remove it; add nothing.", field, sp.Text),
			}
		}
	}
	return nil
}

// checkBrackets: no stray placeholder brackets survive the restore.
func checkBrackets(src, out Output, spans []Span) *Problem {
	p := Protected{Spans: spans}
	count := func(s string) int { return strings.Count(s, "⟦") + strings.Count(s, "⟧") }
	if count(p.Restore(out.Title)) > count(p.Restore(src.Title)) || count(p.Restore(out.Description)) > count(p.Restore(src.Description)) {
		return &Problem{
			Reason:   "it has broken placeholder brackets in it",
			Feedback: "Your answer has stray ⟦ or ⟧ characters. Use each placeholder exactly as given, like ⟦1⟧, and nothing else with those brackets.",
		}
	}
	return nil
}

func checkTitleOneLine(title string) *Problem {
	if !strings.Contains(title, "\n") {
		return nil
	}
	return &Problem{
		Reason:   "the title is split over two lines",
		Feedback: "The title must be a single line.",
	}
}

// restoredLength measures a field the way YouTube will see it, with every
// placeholder replaced by the original text.
func restoredLength(s string, spans []Span) int {
	p := Protected{Spans: spans}
	return utf8.RuneCountInString(p.Restore(s))
}

func checkTitleLength(title string, spans []Span) *Problem {
	n := restoredLength(title, spans)
	if n <= MaxTitle {
		return nil
	}
	return &Problem{
		Reason:   fmt.Sprintf("the title is %d characters, YouTube allows %d", n, MaxTitle),
		Feedback: fmt.Sprintf("The title is %d characters long. Rewrite it in at most %d characters, keeping its meaning and every placeholder.", n, MaxTitle-5),
		Shorten:  true,
	}
}

// checkDescriptionLength: YouTube limits descriptions to 5,000 bytes, so a
// description in Thai or Hindi reaches the limit sooner than in English.
// titleSeparators split a title into parts ("Rain · 3 Hours | Sleep").
const titleSeparators = "·|•—–"

// noSpaceScripts are written without spaces between words.
var noSpaceScripts = map[langs.Script]bool{langs.Japanese: true, langs.Han: true, langs.Thai: true, langs.Lao: true, langs.Khmer: true, langs.Myanmar: true}

// checkTitleGrowth catches a title that borrows from the description: a new
// part after a separator, or far more words than the original has.
func checkTitleGrowth(src, out, lang string) *Problem {
	count := func(s string) int {
		n := 0
		for _, r := range s {
			if strings.ContainsRune(titleSeparators, r) {
				n++
			}
		}
		return n
	}
	if count(out) > count(src) {
		return &Problem{
			Reason:   "the title adds a part the original does not have",
			Feedback: "The title has more parts than the original. Translate only the title, part for part; add nothing from the description.",
		}
	}
	srcText, outText := stripPlaceholders(src), stripPlaceholders(out)
	l, _ := langs.Get(lang)
	var grown bool
	if noSpaceScripts[l.Script] {
		srcN, outN := utf8.RuneCountInString(strings.Join(strings.Fields(srcText), "")), utf8.RuneCountInString(strings.Join(strings.Fields(outText), ""))
		grown = outN > max(srcN, 12)*3/2+8
	} else {
		srcN, outN := len(strings.Fields(srcText)), len(strings.Fields(outText))
		grown = outN > (srcN*8+4)/5+2 // about 1.6 times the words, plus two
	}
	if !grown {
		return nil
	}
	return &Problem{
		Reason:   "the title is much longer than the original, so it may add words",
		Feedback: "The title is much longer than the original. Translate only the words of the title; add nothing from the description.",
	}
}

func checkDescriptionLength(desc string, spans []Span) *Problem {
	p := Protected{Spans: spans}
	if n := len(p.Restore(desc)); n <= MaxDescription {
		return nil
	}
	return &Problem{
		Reason:   "the description is too long for YouTube in this language",
		Feedback: "The description is too long. Translate more concisely so it stays under YouTube's limit; drop nothing.",
	}
}

func checkAngles(out Output, spans []Span) *Problem {
	p := Protected{Spans: spans}
	for field, text := range map[string]string{"title": out.Title, "description": out.Description} {
		if strings.ContainsAny(p.Restore(text), "<>") {
			return &Problem{
				Reason:   fmt.Sprintf("the %s contains < or >, which YouTube does not allow", field),
				Feedback: fmt.Sprintf("The %s contains the characters < or >. YouTube rejects them; use other punctuation.", field),
			}
		}
	}
	return nil
}

func checkNotEmpty(src, out Output) *Problem {
	if strings.TrimSpace(out.Title) == "" {
		return &Problem{Reason: "the title came back empty", Feedback: "The title is empty. Translate the title."}
	}
	if strings.TrimSpace(src.Description) != "" && strings.TrimSpace(out.Description) == "" {
		return &Problem{Reason: "the description came back empty", Feedback: "The description is empty. Translate the description."}
	}
	return nil
}

// checkTranslated: a title identical to the source was not translated
// (unless it has no words to translate, like a title made of a name only).
func checkTranslated(src, out, lang string) *Problem {
	if normalizeSpace(src) != normalizeSpace(out) || countLetters(stripPlaceholders(src)) == 0 {
		return nil
	}
	return &Problem{
		Reason:   "the title was not translated",
		Feedback: fmt.Sprintf("The title is still in the original language. Translate it into %s.", langs.EnglishName(lang)),
	}
}

// checkDescriptionTranslated: a description identical to the original was
// not translated (when it has enough words to translate).
func checkDescriptionTranslated(src, out, lang string) *Problem {
	if countLetters(stripPlaceholders(src)) < 12 || normalizeSpace(src) != normalizeSpace(out) {
		return nil
	}
	return &Problem{
		Reason:   "the description was not translated",
		Feedback: fmt.Sprintf("The description is still in the original language. Translate it into %s.", langs.EnglishName(lang)),
	}
}

func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

func stripPlaceholders(s string) string { return rePlaceholder.ReplaceAllString(s, " ") }

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

// scriptTables maps the expected script to the Unicode tables that count as
// written in it.
var scriptTables = map[langs.Script][]*unicode.RangeTable{
	langs.Latin:      {unicode.Latin},
	langs.Cyrillic:   {unicode.Cyrillic},
	langs.Greek:      {unicode.Greek},
	langs.Armenian:   {unicode.Armenian},
	langs.Georgian:   {unicode.Georgian},
	langs.Hebrew:     {unicode.Hebrew},
	langs.Arabic:     {unicode.Arabic},
	langs.Devanagari: {unicode.Devanagari},
	langs.Bengali:    {unicode.Bengali},
	langs.Gurmukhi:   {unicode.Gurmukhi},
	langs.Gujarati:   {unicode.Gujarati},
	langs.Oriya:      {unicode.Oriya},
	langs.Tamil:      {unicode.Tamil},
	langs.Telugu:     {unicode.Telugu},
	langs.Kannada:    {unicode.Kannada},
	langs.Malayalam:  {unicode.Malayalam},
	langs.Sinhala:    {unicode.Sinhala},
	langs.Thai:       {unicode.Thai},
	langs.Lao:        {unicode.Lao},
	langs.Khmer:      {unicode.Khmer},
	langs.Myanmar:    {unicode.Myanmar},
	langs.Ethiopic:   {unicode.Ethiopic},
	langs.Hangul:     {unicode.Hangul, unicode.Han},
	langs.Japanese:   {unicode.Hiragana, unicode.Katakana, unicode.Han},
	langs.Han:        {unicode.Han},
}

// ScriptShare is the share of letters in text written in the script.
func ScriptShare(text string, script langs.Script) float64 {
	tables := scriptTables[script]
	total, in := 0, 0
	for _, r := range stripPlaceholders(text) {
		if !unicode.IsLetter(r) {
			continue
		}
		total++
		if unicode.IsOneOf(tables, r) {
			in++
		}
	}
	if total == 0 {
		return 1
	}
	return float64(in) / float64(total)
}

// checkScript: most letters are in the script the language is written in.
// Names and brand words may stay in Latin letters, so non-Latin languages
// need half; Latin languages need nearly all.
func checkScript(out Output, lang string) *Problem {
	l, ok := langs.Get(lang)
	if !ok || scriptTables[l.Script] == nil {
		return nil
	}
	need := 0.5
	if l.Script == langs.Latin {
		need = 0.85
	}
	text := out.Title + "\n" + out.Description
	if l.Script == langs.Japanese && !hasKana(text) && countLetters(stripPlaceholders(text)) >= 6 && ScriptShare(text, langs.Han) >= need {
		return &Problem{
			Reason:   "it looks like Chinese, not Japanese",
			Feedback: "This reads as Chinese. Write natural Japanese with kana and kanji.",
		}
	}
	if ScriptShare(text, l.Script) >= need {
		return nil
	}
	reason := fmt.Sprintf("it does not look like it is written in %s", l.English)
	if l.Script != langs.Latin && ScriptShare(text, langs.Latin) > 0.5 {
		reason = "it looks like it is not translated yet"
	}
	return &Problem{
		Reason:   reason,
		Feedback: fmt.Sprintf("Write the title and description in %s, in its usual script (%s).", l.English, l.Script),
	}
}

var reLatinWord = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'’-]*[A-Za-z0-9]`)

// checkAddedLatin catches added keywords in languages not written in Latin
// letters: every Latin word in the answer must come from the original (a
// name, a brand). "ASMR" or "4K Relaxing" appearing from nowhere is flagged.
func checkAddedLatin(src, out Output, lang string) *Problem {
	l, ok := langs.Get(lang)
	if !ok || l.Script == langs.Latin {
		return nil
	}
	original := strings.ToLower(stripPlaceholders(src.Title + "\n" + src.Description))
	for field, text := range map[string]string{"title": out.Title, "description": out.Description} {
		for _, w := range reLatinWord.FindAllString(stripPlaceholders(text), -1) {
			if !strings.ContainsAny(w, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz") {
				continue // plain numbers are fine
			}
			if !strings.Contains(original, strings.ToLower(w)) {
				return &Problem{
					Reason:   fmt.Sprintf("the %s adds \u201c%s\u201d, which the original does not have", field, w),
					Feedback: fmt.Sprintf("The %s contains the word \"%s\", which is not in the original. Remove it; add nothing.", field, w),
				}
			}
		}
	}
	return nil
}

func hasKana(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}

// RepairLines re-inserts empty lines where the source had them when the
// model kept every non-empty line but dropped or added blank ones. Text is
// not changed, only empty lines, so the result stays faithful.
func RepairLines(src, out string) string {
	if lineCount(src) == lineCount(out) {
		return out
	}
	srcLines := strings.Split(src, "\n")
	var outLines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			outLines = append(outLines, l)
		}
	}
	nonEmpty := 0
	for _, l := range srcLines {
		if strings.TrimSpace(l) != "" {
			nonEmpty++
		}
	}
	if nonEmpty != len(outLines) {
		return out
	}
	rebuilt := make([]string, 0, len(srcLines))
	i := 0
	for _, l := range srcLines {
		if strings.TrimSpace(l) == "" {
			rebuilt = append(rebuilt, "")
			continue
		}
		rebuilt = append(rebuilt, outLines[i])
		i++
	}
	return strings.Join(rebuilt, "\n")
}
