package translate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind is what a protected span is.
type Kind string

const (
	KindURL       Kind = "link"
	KindEmail     Kind = "e-mail address"
	KindHandle    Kind = "handle"
	KindHashtag   Kind = "hashtag"
	KindTimestamp Kind = "timestamp"
	KindKeep      Kind = "kept word"
)

// Span is one protected piece of the source text.
type Span struct {
	ID   int
	Kind Kind
	Text string
}

// Protected is a source text with its protected spans replaced by
// placeholders such as ⟦1⟧.
type Protected struct {
	Text  string
	Spans []Span
}

var (
	reURL       = regexp.MustCompile(`(?i)\b(?:https?://|www\.)[^\s<>"]+`)
	reEmail     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	reHandle    = regexp.MustCompile(`(?:^|[^\w@])(@[A-Za-z0-9._-]{2,})`)
	reHashtag   = regexp.MustCompile(`(?:^|[^\w#&])(#[\p{L}\p{N}_]+)`)
	reTimestamp = regexp.MustCompile(`(?:\d{1,2}:)?\d{1,2}:\d{2}`)
	// rePlaceholder is lenient: models sometimes add spaces inside the brackets.
	rePlaceholder = regexp.MustCompile(`⟦\s*(\d+)\s*⟧`)
)

// Placeholder renders the placeholder for span id.
func Placeholder(id int) string { return "⟦" + strconv.Itoa(id) + "⟧" }

type candidate struct {
	start, end int
	kind       Kind
	prio       int // higher wins when two candidates start at the same place
}

// Protect replaces links, e-mail addresses, handles, hashtags, timestamps and
// keep words with numbered placeholders. Overlaps resolve to the earliest,
// longest span, so a link containing "#" or "@" stays one link and a keep
// word inside a hashtag stays part of the hashtag. Numbering continues from
// the spans already in p, so a title and a description share one map.
func (p *Protected) Protect(text string, keep []string) string {
	var cands []candidate
	add := func(re *regexp.Regexp, group int, kind Kind, prio int) {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			s, e := m[2*group], m[2*group+1]
			if s < 0 {
				continue
			}
			switch kind {
			case KindURL:
				e = s + len(trimURL(text[s:e]))
			case KindHandle:
				e = s + len(strings.TrimRight(text[s:e], ".-"))
			case KindTimestamp:
				if !standsAlone(text, s, e, "0123456789:") {
					continue
				}
			}
			cands = append(cands, candidate{s, e, kind, prio})
		}
	}
	add(rePlaceholder, 0, KindKeep, 7) // text that already looks like a placeholder
	add(reURL, 0, KindURL, 6)
	add(reEmail, 0, KindEmail, 5)
	add(reHandle, 1, KindHandle, 4)
	add(reHashtag, 1, KindHashtag, 3)
	add(reTimestamp, 0, KindTimestamp, 2)
	for _, w := range keep {
		for _, loc := range findWord(text, w) {
			cands = append(cands, candidate{loc[0], loc[1], KindKeep, 1})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.start != b.start {
			return a.start < b.start
		}
		if a.end-a.start != b.end-b.start {
			return a.end-a.start > b.end-b.start
		}
		return a.prio > b.prio
	})

	var b strings.Builder
	pos := 0
	for _, c := range cands {
		if c.start < pos || c.end <= c.start {
			continue // overlaps a span already taken
		}
		b.WriteString(text[pos:c.start])
		id := len(p.Spans) + 1
		p.Spans = append(p.Spans, Span{ID: id, Kind: c.kind, Text: text[c.start:c.end]})
		b.WriteString(Placeholder(id))
		pos = c.end
	}
	b.WriteString(text[pos:])
	return b.String()
}

// standsAlone reports whether text[s:e] is not glued to one of the given
// characters on either side (so 12:30 inside 112:304 is not a timestamp).
func standsAlone(text string, s, e int, glue string) bool {
	if s > 0 {
		if r, _ := utf8.DecodeLastRuneInString(text[:s]); strings.ContainsRune(glue, r) {
			return false
		}
	}
	if e < len(text) {
		if r, _ := utf8.DecodeRuneInString(text[e:]); strings.ContainsRune(glue, r) {
			return false
		}
	}
	return true
}

// trimURL drops trailing punctuation that usually ends a sentence, and an
// unmatched closing bracket.
func trimURL(u string) string {
	for len(u) > 0 {
		r, size := utf8.DecodeLastRuneInString(u)
		switch {
		case strings.ContainsRune(".,;:!?'\"…", r):
			u = u[:len(u)-size]
		case r == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
			u = u[:len(u)-size]
		default:
			return u
		}
	}
	return u
}

// findWord finds w in text case-insensitively, on word boundaries where w
// starts or ends with a letter or digit.
func findWord(text, w string) [][2]int {
	w = strings.TrimSpace(w)
	if w == "" {
		return nil
	}
	pattern := regexp.QuoteMeta(w)
	first, _ := utf8.DecodeRuneInString(w)
	last, _ := utf8.DecodeLastRuneInString(w)
	re, err := regexp.Compile(`(?i)` + pattern)
	if err != nil {
		return nil
	}
	var out [][2]int
	for _, m := range re.FindAllStringIndex(text, -1) {
		if isWordRune(first) && m[0] > 0 {
			if r, _ := utf8.DecodeLastRuneInString(text[:m[0]]); isWordRune(r) {
				continue
			}
		}
		if isWordRune(last) && m[1] < len(text) {
			if r, _ := utf8.DecodeRuneInString(text[m[1]:]); isWordRune(r) {
				continue
			}
		}
		out = append(out, [2]int{m[0], m[1]})
	}
	return out
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// Restore puts the original spans back. Placeholders the model invented are
// removed; the checks report them before this runs.
func (p *Protected) Restore(text string) string {
	byID := map[int]string{}
	for _, s := range p.Spans {
		byID[s.ID] = s.Text
	}
	return rePlaceholder.ReplaceAllStringFunc(text, func(m string) string {
		id, _ := strconv.Atoi(rePlaceholder.FindStringSubmatch(m)[1])
		return byID[id]
	})
}

// Normalize rewrites lenient placeholder spellings (⟦ 1 ⟧) to the exact form.
func Normalize(text string) string {
	return rePlaceholder.ReplaceAllStringFunc(text, func(m string) string {
		return "⟦" + rePlaceholder.FindStringSubmatch(m)[1] + "⟧"
	})
}

// placeholderCounts counts each placeholder id in text.
func placeholderCounts(text string) map[int]int {
	out := map[int]int{}
	for _, m := range rePlaceholder.FindAllStringSubmatch(text, -1) {
		id, _ := strconv.Atoi(m[1])
		out[id]++
	}
	return out
}

// describe names a span for a person: `the link https://...`.
func (s Span) describe() string {
	return fmt.Sprintf("the %s %s", s.Kind, s.Text)
}
