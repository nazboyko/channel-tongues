package translate

import (
	"strings"
	"testing"
)

func spansOf(p *Protected) map[string]Kind {
	out := map[string]Kind{}
	for _, s := range p.Spans {
		out[s.Text] = s.Kind
	}
	return out
}

func TestProtectEveryKind(t *testing.T) {
	src := "Watch at https://www.youtube.com/@theslownighthours. Mail hello@example.com, ask @thelivingpostcard.\n0:00 Rain begins\n1:30:00 Thunder\n#sleepsounds #rainsounds"
	var p Protected
	out := p.Protect(src, nil)
	got := spansOf(&p)
	want := map[string]Kind{
		"https://www.youtube.com/@theslownighthours": KindURL,
		"hello@example.com":                          KindEmail,
		"@thelivingpostcard":                         KindHandle,
		"0:00":                                       KindTimestamp,
		"1:30:00":                                    KindTimestamp,
		"#sleepsounds":                               KindHashtag,
		"#rainsounds":                                KindHashtag,
	}
	for text, kind := range want {
		if got[text] != kind {
			t.Errorf("%q: got %q, want %q (all: %v)", text, got[text], kind, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra spans: %v", got)
	}
	if strings.Contains(out, "http") || strings.Contains(out, "#") || strings.Contains(out, "@") {
		t.Errorf("unprotected text left: %s", out)
	}
	if !strings.Contains(out, "⟦1⟧. Mail") {
		t.Errorf("the sentence-ending dot stays outside the link: %s", out)
	}
	if p.Restore(out) != src {
		t.Fatalf("restore mismatch:\n%s\n%s", p.Restore(out), src)
	}
}

func TestNestedSpansStayWhole(t *testing.T) {
	var p Protected
	src := "Docs: https://example.com/page#part and (https://example.com/a_(b)) done"
	out := p.Protect(src, []string{"part"})
	got := spansOf(&p)
	if got["https://example.com/page#part"] != KindURL || got["https://example.com/a_(b)"] != KindURL {
		t.Fatalf("links with # and brackets must stay whole: %v", got)
	}
	if len(p.Spans) != 2 {
		t.Fatalf("a keep word inside a link is not a separate span: %v", got)
	}
	if !strings.HasSuffix(out, ") done") {
		t.Fatalf("unmatched closing bracket stays outside: %s", out)
	}
	if p.Restore(out) != src {
		t.Fatal("restore")
	}
}

func TestKeepWordInsideHashtag(t *testing.T) {
	var p Protected
	src := "Welcome to The Slow Night Hours. #TheSlowNightHours #sleepsounds"
	out := p.Protect(src, []string{"the slow night hours", "sleepsounds"})
	got := spansOf(&p)
	if got["The Slow Night Hours"] != KindKeep {
		t.Fatalf("keep word matched case-insensitively and restored as written: %v", got)
	}
	if got["#TheSlowNightHours"] != KindHashtag || got["#sleepsounds"] != KindHashtag {
		t.Fatalf("a keep word inside a hashtag leaves the hashtag whole: %v", got)
	}
	if len(p.Spans) != 3 || p.Restore(out) != src {
		t.Fatalf("spans %v, restore %q", got, p.Restore(out))
	}
}

func TestKeepWordBoundaries(t *testing.T) {
	var p Protected
	p.Protect("Lofi Hours and lofihours.", []string{"Lofi"})
	if len(p.Spans) != 1 || p.Spans[0].Text != "Lofi" {
		t.Fatalf("keep word must not match inside another word: %v", p.Spans)
	}
}

func TestTimestampsNeedToStandAlone(t *testing.T) {
	var p Protected
	p.Protect("Ratio 112:304, version 1:2:3:4, chapter 12:40 and 0:00-1:30", nil)
	got := spansOf(&p)
	if got["12:40"] != KindTimestamp || got["0:00"] != KindTimestamp || got["1:30"] != KindTimestamp {
		t.Fatalf("timestamps: %v", got)
	}
	if _, bad := got["12:30"]; bad || len(got) != 3 {
		t.Fatalf("numbers glued to digits or colons are not timestamps: %v", got)
	}
}

func TestSharedNumberingAcrossFields(t *testing.T) {
	var p Protected
	title := p.Protect("Rain #sleep", nil)
	desc := p.Protect("More at https://x.example #sleep", nil)
	if title != "Rain ⟦1⟧" || desc != "More at ⟦2⟧ ⟦3⟧" {
		t.Fatalf("got %q / %q", title, desc)
	}
}

func TestRestoreIsLenientAboutSpacing(t *testing.T) {
	var p Protected
	p.Protect("Go #now", nil)
	if got := p.Restore("Vamos ⟦ 1 ⟧ ya"); got != "Vamos #now ya" {
		t.Fatalf("got %q", got)
	}
	if got := Normalize("a ⟦ 12⟧ b"); got != "a ⟦12⟧ b" {
		t.Fatalf("normalize %q", got)
	}
	if got := p.Restore("invented ⟦9⟧ here"); got != "invented  here" {
		t.Fatalf("unknown placeholders are dropped, got %q", got)
	}
}

func TestEmojiAndPlainTextAreUntouched(t *testing.T) {
	var p Protected
	src := "Rain 🌧 on a cabin · 3 hours"
	if out := p.Protect(src, nil); out != src || len(p.Spans) != 0 {
		t.Fatalf("got %q %v", out, p.Spans)
	}
}

func TestPlaceholderLikeSourceTextRoundTrips(t *testing.T) {
	var p Protected
	src := "Rule ⟦1⟧ and #tag, see ⟦9⟧ here"
	out := p.Protect(src, nil)
	if p.Restore(out) != src {
		t.Fatalf("restore %q", p.Restore(out))
	}
}

func TestUserMessageKeepsAmpersands(t *testing.T) {
	if msg := UserMessage(Output{Title: "Rain & Thunder <3"}); !strings.Contains(msg, "Rain & Thunder <3") {
		t.Fatalf("got %s", msg)
	}
}
