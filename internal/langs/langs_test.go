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
		verifiedOnly := false
		for _, c := range verified {
			verifiedOnly = verifiedOnly || c == l.Code
		}
		// A verified code may have an unknown script (the check is skipped);
		// every hand-written entry has one.
		if l.English == "" || l.Native == "" || (l.Script == "" && !verifiedOnly) {
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

func TestDescribeFallsBackToCLDR(t *testing.T) {
	// Codes YouTube refuses are not in the table, but still get CLDR names.
	for code, english := range map[string]string{"ceb": "Cebuano", "nn": "Norwegian Nynorsk", "ast": "Asturian"} {
		l, ok := Describe(code)
		if !ok || l.English != english || l.Native == "" || l.Script != "" {
			t.Errorf("%s: got %+v %v", code, l, ok)
		}
	}
	if l, ok := Describe("es"); !ok || l.Script != Latin {
		t.Fatal("table entries come first")
	}
	if _, ok := Describe("not-a-code!"); ok {
		t.Fatal("garbage is not a language")
	}
	if EnglishName("ceb") != "Cebuano" {
		t.Fatal("EnglishName uses the fallback")
	}
}

func TestVerifiedCodesAreInTheList(t *testing.T) {
	all := All("en")
	byCode := map[string]Language{}
	for _, l := range all {
		byCode[l.Code] = l
	}
	if len(all) < 160 {
		t.Fatalf("expected the verified codes in the list, got %d languages", len(all))
	}
	cases := map[string]Script{"eo": Latin, "haw": Latin, "yi": Hebrew, "ti": Ethiopic, "tt": Cyrillic, "yue": Han, "bo": Tibetan, "chr": Cherokee, "iu": Syllabics, "ie": Latin, "tw": Latin, "sat": OlChiki}
	for code, script := range cases {
		l, ok := byCode[code]
		if !ok || l.English == "" || l.Native == "" || l.Script != script {
			t.Errorf("%s: %+v (in list %v), want script %q", code, l, ok, script)
		}
	}
	// No two languages in the list share a name a person sees.
	english, native := map[string]string{}, map[string]string{}
	for _, l := range All("") {
		if other, ok := english[l.English]; ok {
			t.Errorf("%s and %s are both called %q", other, l.Code, l.English)
		}
		if other, ok := native[l.Native]; ok {
			t.Errorf("%s and %s are both shown as %q", other, l.Code, l.Native)
		}
		english[l.English], native[l.Native] = l.Code, l.Code
	}
	if _, ok := byCode["en-IN"]; ok {
		t.Fatal("English variants are left out for an English source")
	}
	for _, refused := range []string{"ceb", "ny", "nn", "zh"} {
		if _, ok := byCode[refused]; ok {
			t.Errorf("%s must not be offered", refused)
		}
	}
}

func TestEveryVerifiedCodeHasAScript(t *testing.T) {
	for _, l := range All("") {
		if l.Script == "" {
			t.Errorf("%s (%s) has no script, so no script check would run", l.Code, l.English)
		}
	}
}
