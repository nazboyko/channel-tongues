package langs

import (
	"testing"
	"unicode"
)

func TestPopularComesFirstInOrder(t *testing.T) {
	all := All("en")
	for i, code := range Popular {
		if all[i].Code != code || !all[i].Popular || all[i].Rank != i+1 {
			t.Fatalf("position %d: got %+v, want %s", i, all[i], code)
		}
	}
	if all[len(Popular)].Code != "it" || all[len(Popular)].Popular {
		t.Fatalf("the next tier starts with Italian, unchecked; got %+v", all[len(Popular)])
	}
}

func TestRestIsAlphabetical(t *testing.T) {
	all := All("")
	tail := all[len(Popular)+len(next):]
	for i := 1; i < len(tail); i++ {
		if tail[i-1].English > tail[i].English {
			t.Fatalf("%s before %s", tail[i-1].English, tail[i].English)
		}
	}
}

func TestSourceLanguageIsLeftOut(t *testing.T) {
	for _, l := range All("en-US") {
		if l.Code == "en" || l.Code == "en-GB" {
			t.Fatalf("source language %s must be left out", l.Code)
		}
	}
	for _, l := range All("es") {
		if l.Code == "es" || l.Code == "es-419" {
			t.Fatalf("source language %s must be left out", l.Code)
		}
	}
}

func TestAliasesAndCase(t *testing.T) {
	cases := map[string]string{
		"pt": "pt-BR", "zh-CN": "zh-Hans", "zh-tw": "zh-Hant", "iw": "he",
		"PT-br": "pt-BR", "ES": "es", "xx-Unknown": "xx-Unknown",
	}
	for in, want := range cases {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
	if l, ok := Get("zh-cn"); !ok || l.Code != "zh-Hans" {
		t.Fatalf("Get(zh-cn) = %+v %v", l, ok)
	}
}

func TestEveryLanguageHasNamesAndScript(t *testing.T) {
	seen := map[string]bool{}
	for _, l := range All("") {
		if l.English == "" || l.Native == "" || l.Script == "" {
			t.Errorf("incomplete entry %+v", l)
		}
		if seen[l.Code] {
			t.Errorf("duplicate %s", l.Code)
		}
		seen[l.Code] = true
	}
	for _, code := range append(append([]string{}, Popular...), next...) {
		if !seen[code] {
			t.Errorf("%s is ranked but missing from the table", code)
		}
	}
}

func TestCyrillicNamesComeFromCLDR(t *testing.T) {
	for _, code := range []string{"ru", "uk", "bg", "sr"} {
		l, _ := Get(code)
		cyr := 0
		for _, r := range l.Native {
			if unicode.Is(unicode.Cyrillic, r) {
				cyr++
			}
		}
		if cyr == 0 || !unicode.IsUpper([]rune(l.Native)[0]) {
			t.Errorf("%s native name %q should be capitalized Cyrillic", code, l.Native)
		}
	}
}

func TestSortByRank(t *testing.T) {
	got := Sort([]string{"zz", "fr", "es", "uk", "ja"})
	want := []string{"es", "ja", "fr", "uk", "zz"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestNames(t *testing.T) {
	if Name("ja") != "日本語" || EnglishName("pt") != "Portuguese (Brazil)" || Name("xx") != "xx" {
		t.Fatal("names")
	}
}
