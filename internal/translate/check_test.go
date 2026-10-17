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
		{"English for Japanese", "ja", Output{"Rain on a cabin window ⟦1⟧", "Three hours of rain.\n\nMore at ⟦2⟧\n⟦3⟧"}, "not translated"},
		{"Chinese for Japanese", "ja", Output{"小屋窗上的雨声 ⟦1⟧", "三小时的雨声。\n\n更多 ⟦2⟧\n⟦3⟧"}, "it looks like Chinese, not Japanese"},
		{"good Japanese", "ja", Output{"山小屋の窓に降る雨 ⟦1⟧", "3時間の雨の音。\n\nもっと見る ⟦2⟧\n⟦3⟧"}, ""},
		{"good Thai", "th", Output{"ฝนตกที่หน้าต่างกระท่อม ⟦1⟧", "เสียงฝนสามชั่วโมง\n\nดูเพิ่มเติม ⟦2⟧\n⟦3⟧"}, ""},
		{"Latin letters for Hindi", "hi", Output{"Barish ki awaaz ⟦1⟧", "Teen ghante ki barish.\n\nAur dekhein ⟦2⟧\n⟦3⟧"}, "not translated yet"},
		{"Cyrillic for Ukrainian", "uk", Output{cyrillic(12) + " ⟦1⟧", cyrillic(20) + "\n\n" + cyrillic(5) + " ⟦2⟧\n⟦3⟧"}, ""},
		{"Latin for Ukrainian", "uk", Output{"Doshch na vikni ⟦1⟧", "Try hodyny doshchu.\n\nBilshe ⟦2⟧\n⟦3⟧"}, "not translated yet"},
		{"links swapped between lines", "es", Output{good.Title, "Tres horas de lluvia.\n\nMás en ⟦3⟧\n⟦2⟧"}, "moved to another line"},
		{"added hashtag in Spanish", "es", Output{"Lluvia en la cabaña #ASMR ⟦1⟧", good.Description}, "adds the hashtag #ASMR"},
		{"added link", "es", Output{good.Title, "Tres horas de lluvia. https://spam.example\n\nMás en ⟦2⟧\n⟦3⟧"}, "adds the link https://spam.example"},
		{"blank line filled", "es", Output{good.Title, "Tres horas de lluvia.\nAñadido nuevo.\nMás en ⟦2⟧\n⟦3⟧"}, "line 2 of the description should be empty"},
		{"stray bracket", "es", Output{"Lluvia ⟦1⟧⟧ ⟦", good.Description}, "broken placeholder brackets"},
		{"title on two lines", "es", Output{"Lluvia en la ventana\nde una cabaña ⟦1⟧", good.Description}, "split over two lines"},
		{"added 4K in Korean", "ko", Output{"오두막 창문의 빗소리 4K ⟦1⟧", "세 시간의 빗소리.\n\n더 보기 ⟦2⟧\n⟦3⟧"}, "adds \u201c4K\u201d"},
		{"untranslated description", "es", Output{good.Title, src.Description}, "the description was not translated"},
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

func TestDescriptionLimitIsInBytes(t *testing.T) {
	if p := checkDescriptionLength(strings.Repeat("a", 5001), nil); p == nil {
		t.Fatal("5001 bytes is too long")
	}
	if p := checkDescriptionLength(strings.Repeat("a", 5000), nil); p != nil {
		t.Fatal("5000 bytes is fine")
	}
	if p := checkDescriptionLength(strings.Repeat("ก", 2000), nil); p == nil {
		t.Fatal("2000 Thai characters are 6000 bytes, over the limit")
	}
}

func TestEmptySourceDescriptionMustStayEmpty(t *testing.T) {
	if p := checkLines("", "Suscríbete para más videos."); p == nil {
		t.Fatal("an invented description must be flagged")
	}
	if p := checkLines("", ""); p != nil {
		t.Fatal("empty stays empty")
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

func TestTitleThatBorrowsFromTheDescription(t *testing.T) {
	src := Output{Title: "night cabin 30s", Description: "The Slow Night Hours - quiet night scenes and soft music for deep sleep."}
	cases := []struct {
		lang, title, reason string
	}{
		// Seen on a real test video: the model copied the description's first sentence into the title.
		{"es", "cabina nocturna 30s · escenas nocturnas tranquilas y música suave para dormir profundamente.", "adds a part"},
		{"de", "Nacht in der Hütte 30s ruhige Nachtszenen und sanfte Musik für tiefen Schlaf", "much longer"},
		{"ja", "夜の山小屋 30秒 静かな夜の風景と深い眠りのための穏やかな音楽をお届けします", "much longer"},
		{"es", "cabina nocturna 30s", ""},
		{"de", "Nachthütte 30s", ""},
		{"ja", "夜の山小屋 30秒", ""},
	}
	for _, tc := range cases {
		p := checkTitleGrowth(src.Title, tc.title, tc.lang)
		switch {
		case tc.reason == "" && p != nil:
			t.Errorf("%s %q: unexpected %q", tc.lang, tc.title, p.Reason)
		case tc.reason != "" && (p == nil || !strings.Contains(p.Reason, tc.reason)):
			t.Errorf("%s %q: want %q, got %v", tc.lang, tc.title, tc.reason, p)
		}
	}
	// A long title with parts translates fine.
	long := "Rain on a Cabin Window at Night · 3 Hours of Sleep Sounds"
	if p := checkTitleGrowth(long, "Lluvia en la ventana de una cabaña por la noche · 3 horas de sonidos para dormir", "es"); p != nil {
		t.Fatalf("normal Spanish growth flagged: %s", p.Reason)
	}
	if p := checkTitleGrowth(long, "夜の山小屋の窓に降る雨 · 3時間の睡眠用サウンド", "ja"); p != nil {
		t.Fatalf("normal Japanese flagged: %s", p.Reason)
	}
}
