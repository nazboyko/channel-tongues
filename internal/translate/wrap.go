package translate

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// sentenceEnd lists what can end a sentence or a line that stands on its own.
const sentenceEnd = ".!?:;…\"”’'»)]」』。！？"

var reListMarker = regexp.MustCompile(`^\s*([-*•·–—]|\d+[.)])\s`)

// JoinSoftWraps joins lines that a person wrapped in the middle of a
// sentence, so the model translates the sentence whole and the line check
// does not force a break into the translation. It runs on the protected
// description, so links, timestamps, hashtags and handles are placeholders.
// A break is kept when the line before ends a sentence, is short (a tagline,
// a list item without a marker) or ends with a placeholder, and when the line
// after is blank, starts with a placeholder or a list marker.
func JoinSoftWraps(desc string) string {
	lines := strings.Split(desc, "\n")
	// A line starting in lowercase only says "this goes on" in a text whose
	// lines otherwise start with capitals; some people write all in lowercase.
	cased := false
	for _, l := range lines {
		if r, _ := utf8.DecodeRuneInString(strings.TrimSpace(l)); unicode.IsUpper(r) {
			cased = true
			break
		}
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if len(out) > 0 && softWrap(out[len(out)-1], l, cased) {
			out[len(out)-1] = strings.TrimRight(out[len(out)-1], " \t") + " " + strings.TrimLeft(l, " \t")
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

func softWrap(prev, next string, cased bool) bool {
	p, n := strings.TrimSpace(prev), strings.TrimSpace(next)
	if p == "" || n == "" {
		return false // a blank line is a paragraph break
	}
	if r, _ := utf8.DecodeLastRuneInString(p); strings.ContainsRune(sentenceEnd, r) {
		return false
	}
	if strings.HasSuffix(p, "⟧") || strings.HasPrefix(n, "⟦") || strings.HasSuffix(n, "⟧") {
		return false // a link, timestamp, hashtag or handle at either end: an item with its link
	}
	if w := strings.Fields(n)[0]; strings.IndexFunc(w[1:], unicode.IsUpper) >= 0 && strings.IndexFunc(w, unicode.IsLower) >= 0 {
		return false // a mixed-case name such as iPhone or YouTube starts the line: an item (AI is fine)
	}
	if reListMarker.MatchString(next) || reListMarker.MatchString(prev) {
		return false
	}
	if len(strings.Fields(p)) < 5 {
		return false // a short line stands on its own (a tagline, a title, an item)
	}
	// Only a line that stops on a word or a comma can go on, and only when
	// the next line clearly continues it: it starts in lowercase, or the line
	// before ends on a small word that cannot end a sentence ("made with").
	last, _ := utf8.DecodeLastRuneInString(p)
	if !(unicode.IsLetter(last) || unicode.IsDigit(last) || last == ',') {
		return false
	}
	first, _ := utf8.DecodeRuneInString(n)
	if cased && unicode.IsLower(first) {
		return true
	}
	// After a small word the next line must be a real part of the sentence,
	// not a short item ("songs I fell asleep to" / "Weightless").
	words := strings.Fields(p)
	return continuers[strings.ToLower(strings.Trim(words[len(words)-1], ","))] && len(strings.Fields(n)) >= 4
}

// continuers are small words a sentence does not end on.
var continuers = map[string]bool{}

func init() {
	for _, w := range strings.Fields("a an the and or but nor with without of to in into on onto at by for from as than that which who whose our your my their his her its this these those is are was were be been has have had not over under about through via per") {
		continuers[w] = true
	}
}
