package translate

import (
	"strings"
	"testing"
)

// cyrillic builds a Cyrillic word from code points so that no source file
// in this repository contains Cyrillic text.
func cyrillic(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteRune(rune(0x0430 + i%32))
	}
	return b.String()
}

func TestCheckTable(t *testing.T) {
	spans := []Span{
		{ID: 1, Kind: KindHashtag, Text: "#sleepsounds"},
		{ID: 2, Kind: KindURL, Text: "https://www.youtube.com/@theslownighthours"},
		{ID: 3, Kind: KindHashtag, Text: "#rain"},
	}
	src := Output{
		Title:       "Rain on a Cabin Window ⟦1⟧",
		Description: "Three hours of rain.\n\nMore at ⟦2⟧\n⟦3⟧",
	}
	good := Output{
		Title:       "Lluvia en la ventana de una cabaña ⟦1⟧",
		Description: "Tres horas de lluvia.\n\nMás en ⟦2⟧\n⟦3⟧",
	}
	longTitle := strings.Repeat("lluvia ", 16) + "⟦1⟧"

	cases := []struct {
		name   string
		lang   string
		out    Output
		reason string // substring; empty means the answer passes
	}{
		{"good Spanish", "es", good, ""},
		{"missing link", "es", Output{good.Title, "Tres horas de lluvia.\n\nMás en el canal\n⟦3⟧"}, "the link https://www.youtube.com/@theslownighthours went missing from the description"},
		{"hashtag twice", "es", Output{"Lluvia ⟦1⟧ ⟦1⟧", good.Description}, "appears 2 times in the title"},
		{"invented placeholder", "es", Output{good.Title, good.Description + " ⟦7⟧"}, "something the original does not have"},
		{"placeholder moved fields", "es", Output{"Lluvia en la cabaña", good.Description + " ⟦1⟧"}, "went missing from the title"},
		{"line count", "es", Output{good.Title, "Tres horas de lluvia. Más en ⟦2⟧\n⟦3⟧"}, "the description has 2 lines instead of 4"},
		{"title too long", "es", Output{longTitle, good.Description}, "the title is 124 characters, YouTube allows 100"},
		{"angle brackets", "es", Output{"Lluvia <3 ⟦1⟧", good.Description}, "contains < or >"},
		{"empty title", "es", Output{" ", good.Description}, "the title came back empty"},
		{"untranslated title", "es", Output{src.Title, good.Description}, "the title was not translated"},
		{"English for Japanese", "ja", Output{"Rain on a cabin window ⟦1⟧", "Three hours of rain.\n\nMore at ⟦2⟧\n⟦3⟧"}, "it looks like it is still English"},
		{"Chinese for Japanese", "ja", Output{"小屋窗上的雨声 ⟦1⟧", "三小时的雨声。\n\n更多 ⟦2⟧\n⟦3⟧"}, "it looks like Chinese, not Japanese"},
		{"good Japanese", "ja", Output{"山小屋の窓に降る雨 ⟦1⟧", "3時間の雨の音。\n\nもっと見る ⟦2⟧\n⟦3⟧"}, ""},
		{"good Thai", "th", Output{"ฝนตกที่หน้าต่างกระท่อม ⟦1⟧", "เสียงฝนสามชั่วโมง\n\nดูเพิ่มเติม ⟦2⟧\n⟦3⟧"}, ""},
		{"Latin letters for Hindi", "hi", Output{"Barish ki awaaz ⟦1⟧", "Teen ghante ki barish.\n\nAur dekhein ⟦2⟧\n⟦3⟧"}, "still English"},
		{"Cyrillic for Ukrainian", "uk", Output{cyrillic(12) + " ⟦1⟧", cyrillic(20) + "\n\n" + cyrillic(5) + " ⟦2⟧\n⟦3⟧"}, ""},
		{"Latin for Ukrainian", "uk", Output{"Doshch na vikni ⟦1⟧", "Try hodyny doshchu.\n\nBilshe ⟦2⟧\n⟦3⟧"}, "still English"},
		{"Arabic with a brand name", "ar", Output{"المطر على نافذة الكوخ Cabin ⟦1⟧", "ثلاث ساعات من المطر.\n\nالمزيد ⟦2⟧\n⟦3⟧"}, ""},
		{"added keyword in Korean", "ko", Output{"오두막 창문의 빗소리 ASMR ⟦1⟧", "세 시간의 빗소리.\n\n더 보기 ⟦2⟧\n⟦3⟧"}, "the title adds \u201cASMR\u201d"},
	}
	for _, tc := range cases {
		p := Check(src, tc.out, spans, tc.lang)
		switch {
		case tc.reason == "" && p != nil:
			t.Errorf("%s: unexpected problem %q", tc.name, p.Reason)
		case tc.reason != "" && p == nil:
			t.Errorf("%s: expected %q, got no problem", tc.name, tc.reason)
		case tc.reason != "" && !strings.Contains(p.Reason, tc.reason):
			t.Errorf("%s: reason %q does not contain %q", tc.name, p.Reason, tc.reason)
		}
		if p != nil && p.Feedback == "" {
			t.Errorf("%s: a problem must carry feedback for the retry", tc.name)
		}
	}
}

func TestTitleLengthCountsRestoredLinks(t *testing.T) {
	spans := []Span{{ID: 1, Kind: KindURL, Text: "https://example.com/" + strings.Repeat("a", 90)}}
	p := checkTitleLength("Mira ⟦1⟧", spans)
	if p == nil || !p.Shorten {
		t.Fatal("the title length must count the restored link")
	}
	if checkTitleLength("Mira ⟦1⟧", nil) != nil {
		t.Fatal("without the link it is short")
	}
}

func TestNameOnlyTitleMayStayTheSame(t *testing.T) {
	if p := checkTranslated("⟦1⟧", "⟦1⟧", "es"); p != nil {
		t.Fatal("a title made of a protected name only cannot be translated")
	}
}

func TestDescriptionLimit(t *testing.T) {
	if p := checkDescriptionLength(strings.Repeat("a", 5001), nil); p == nil {
		t.Fatal("5001 characters is too long")
	}
	if p := checkDescriptionLength(strings.Repeat("ä", 5000), nil); p != nil {
		t.Fatal("5000 characters (not bytes) is fine")
	}
}

func TestRepairLines(t *testing.T) {
	src := "One.\n\nTwo.\nThree."
	if got := RepairLines(src, "Uno.\nDos.\nTres."); got != "Uno.\n\nDos.\nTres." {
		t.Fatalf("dropped blank line not restored: %q", got)
	}
	if got := RepairLines(src, "Uno.\n\n\nDos.\nTres."); got != "Uno.\n\nDos.\nTres." {
		t.Fatalf("extra blank line not removed: %q", got)
	}
	if got := RepairLines(src, "Uno. Dos.\nTres."); got != "Uno. Dos.\nTres." {
		t.Fatalf("merged lines are left for the check to catch: %q", got)
	}
	if got := RepairLines(src, "Uno.\n\nDos.\nTres."); got != "Uno.\n\nDos.\nTres." {
		t.Fatal("a correct answer is untouched")
	}
}

func TestScriptShareIgnoresPlaceholdersAndDigits(t *testing.T) {
	if s := ScriptShare("⟦1⟧ ⟦2⟧ 3:00", "Thai"); s != 1 {
		t.Fatalf("no letters means nothing to judge, got %v", s)
	}
}
