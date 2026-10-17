// Package langs holds the language list: the popular order, native names and
// the script each language is expected to be written in.
package langs

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// Script is the writing system a translation is expected to use.
type Script string

const (
	Latin      Script = "Latin"
	Cyrillic   Script = "Cyrillic"
	Greek      Script = "Greek"
	Armenian   Script = "Armenian"
	Georgian   Script = "Georgian"
	Hebrew     Script = "Hebrew"
	Arabic     Script = "Arabic"
	Devanagari Script = "Devanagari"
	Bengali    Script = "Bengali"
	Gurmukhi   Script = "Gurmukhi"
	Gujarati   Script = "Gujarati"
	Oriya      Script = "Oriya"
	Tamil      Script = "Tamil"
	Telugu     Script = "Telugu"
	Kannada    Script = "Kannada"
	Malayalam  Script = "Malayalam"
	Sinhala    Script = "Sinhala"
	Thai       Script = "Thai"
	Lao        Script = "Lao"
	Khmer      Script = "Khmer"
	Myanmar    Script = "Myanmar"
	Ethiopic   Script = "Ethiopic"
	Hangul     Script = "Hangul"
	Japanese   Script = "Japanese" // kana and kanji
	Han        Script = "Han"
	Tibetan    Script = "Tibetan"
	Cherokee   Script = "Cherokee"
	Syllabics  Script = "Canadian syllabics"
	OlChiki    Script = "Ol Chiki"
)

// Language is one entry of the picker.
type Language struct {
	Code    string `json:"code"`    // BCP-47, as used for the video's translations
	English string `json:"english"` // English name
	Native  string `json:"native"`  // name in its own script
	Script  Script `json:"script"`
	Rank    int    `json:"rank"` // 1 is the most watched; the popular twelve are 1..12
	Popular bool   `json:"popular"`
}

// Popular is the pre-checked set, in order of YouTube audience size
// (DataReportal country estimates, early 2025). English is the usual source
// language and is not in the list.
var Popular = []string{"es", "pt-BR", "hi", "id", "ja", "de", "vi", "tr", "ko", "fr", "ar", "th"}

// next follows the popular set, unchecked, before the alphabetical rest.
var next = []string{"it", "pl", "fil", "zh-Hant", "zh-Hans", "ru", "ur", "nl", "uk", "ms", "bn", "ta", "te"}

var table = []Language{
	{Code: "af", English: "Afrikaans", Native: "Afrikaans", Script: Latin},
	{Code: "am", English: "Amharic", Native: "አማርኛ", Script: Ethiopic},
	{Code: "ar", English: "Arabic", Native: "العربية", Script: Arabic},
	{Code: "as", English: "Assamese", Native: "অসমীয়া", Script: Bengali},
	{Code: "az", English: "Azerbaijani", Native: "Azərbaycan", Script: Latin},
	{Code: "be", English: "Belarusian", Native: "", Script: Cyrillic},
	{Code: "bg", English: "Bulgarian", Native: "", Script: Cyrillic},
	{Code: "bn", English: "Bengali", Native: "বাংলা", Script: Bengali},
	{Code: "bs", English: "Bosnian", Native: "Bosanski", Script: Latin},
	{Code: "ca", English: "Catalan", Native: "Català", Script: Latin},
	{Code: "cs", English: "Czech", Native: "Čeština", Script: Latin},
	{Code: "cy", English: "Welsh", Native: "Cymraeg", Script: Latin},
	{Code: "da", English: "Danish", Native: "Dansk", Script: Latin},
	{Code: "de", English: "German", Native: "Deutsch", Script: Latin},
	{Code: "el", English: "Greek", Native: "Ελληνικά", Script: Greek},
	{Code: "en", English: "English", Native: "English", Script: Latin},
	{Code: "en-GB", English: "English (UK)", Native: "English (UK)", Script: Latin},
	{Code: "es", English: "Spanish", Native: "Español", Script: Latin},
	{Code: "es-419", English: "Spanish (Latin America)", Native: "Español (Latinoamérica)", Script: Latin},
	{Code: "et", English: "Estonian", Native: "Eesti", Script: Latin},
	{Code: "eu", English: "Basque", Native: "Euskara", Script: Latin},
	{Code: "fa", English: "Persian", Native: "فارسی", Script: Arabic},
	{Code: "fi", English: "Finnish", Native: "Suomi", Script: Latin},
	{Code: "fil", English: "Filipino", Native: "Filipino", Script: Latin},
	{Code: "fr", English: "French", Native: "Français", Script: Latin},
	{Code: "fr-CA", English: "French (Canada)", Native: "Français (Canada)", Script: Latin},
	{Code: "ga", English: "Irish", Native: "Gaeilge", Script: Latin},
	{Code: "gl", English: "Galician", Native: "Galego", Script: Latin},
	{Code: "gu", English: "Gujarati", Native: "ગુજરાતી", Script: Gujarati},
	{Code: "ha", English: "Hausa", Native: "Hausa", Script: Latin},
	{Code: "he", English: "Hebrew", Native: "עברית", Script: Hebrew},
	{Code: "hi", English: "Hindi", Native: "हिन्दी", Script: Devanagari},
	{Code: "hr", English: "Croatian", Native: "Hrvatski", Script: Latin},
	{Code: "hu", English: "Hungarian", Native: "Magyar", Script: Latin},
	{Code: "hy", English: "Armenian", Native: "Հայերեն", Script: Armenian},
	{Code: "id", English: "Indonesian", Native: "Bahasa Indonesia", Script: Latin},
	{Code: "ig", English: "Igbo", Native: "Igbo", Script: Latin},
	{Code: "is", English: "Icelandic", Native: "Íslenska", Script: Latin},
	{Code: "it", English: "Italian", Native: "Italiano", Script: Latin},
	{Code: "ja", English: "Japanese", Native: "日本語", Script: Japanese},
	{Code: "jv", English: "Javanese", Native: "Basa Jawa", Script: Latin},
	{Code: "ka", English: "Georgian", Native: "ქართული", Script: Georgian},
	{Code: "kk", English: "Kazakh", Native: "", Script: Cyrillic},
	{Code: "km", English: "Khmer", Native: "ខ្មែរ", Script: Khmer},
	{Code: "kn", English: "Kannada", Native: "ಕನ್ನಡ", Script: Kannada},
	{Code: "ko", English: "Korean", Native: "한국어", Script: Hangul},
	{Code: "ky", English: "Kyrgyz", Native: "", Script: Cyrillic},
	{Code: "lo", English: "Lao", Native: "ລາວ", Script: Lao},
	{Code: "lt", English: "Lithuanian", Native: "Lietuvių", Script: Latin},
	{Code: "lv", English: "Latvian", Native: "Latviešu", Script: Latin},
	{Code: "mk", English: "Macedonian", Native: "", Script: Cyrillic},
	{Code: "ml", English: "Malayalam", Native: "മലയാളം", Script: Malayalam},
	{Code: "mn", English: "Mongolian", Native: "", Script: Cyrillic},
	{Code: "mr", English: "Marathi", Native: "मराठी", Script: Devanagari},
	{Code: "ms", English: "Malay", Native: "Bahasa Melayu", Script: Latin},
	{Code: "my", English: "Burmese", Native: "မြန်မာ", Script: Myanmar},
	{Code: "ne", English: "Nepali", Native: "नेपाली", Script: Devanagari},
	{Code: "nl", English: "Dutch", Native: "Nederlands", Script: Latin},
	{Code: "no", English: "Norwegian", Native: "Norsk", Script: Latin},
	{Code: "or", English: "Odia", Native: "ଓଡ଼ିଆ", Script: Oriya},
	{Code: "pa", English: "Punjabi", Native: "ਪੰਜਾਬੀ", Script: Gurmukhi},
	{Code: "pl", English: "Polish", Native: "Polski", Script: Latin},
	{Code: "pt-BR", English: "Portuguese (Brazil)", Native: "Português (BR)", Script: Latin},
	{Code: "pt-PT", English: "Portuguese (Portugal)", Native: "Português (PT)", Script: Latin},
	{Code: "ro", English: "Romanian", Native: "Română", Script: Latin},
	{Code: "ru", English: "Russian", Native: "", Script: Cyrillic},
	{Code: "si", English: "Sinhala", Native: "සිංහල", Script: Sinhala},
	{Code: "sk", English: "Slovak", Native: "Slovenčina", Script: Latin},
	{Code: "sl", English: "Slovenian", Native: "Slovenščina", Script: Latin},
	{Code: "so", English: "Somali", Native: "Soomaali", Script: Latin},
	{Code: "sq", English: "Albanian", Native: "Shqip", Script: Latin},
	{Code: "sr", English: "Serbian", Native: "", Script: Cyrillic},
	{Code: "sv", English: "Swedish", Native: "Svenska", Script: Latin},
	{Code: "sw", English: "Swahili", Native: "Kiswahili", Script: Latin},
	{Code: "ta", English: "Tamil", Native: "தமிழ்", Script: Tamil},
	{Code: "te", English: "Telugu", Native: "తెలుగు", Script: Telugu},
	{Code: "th", English: "Thai", Native: "ไทย", Script: Thai},
	{Code: "tr", English: "Turkish", Native: "Türkçe", Script: Latin},
	{Code: "uk", English: "Ukrainian", Native: "", Script: Cyrillic},
	{Code: "ur", English: "Urdu", Native: "اردو", Script: Arabic},
	{Code: "uz", English: "Uzbek", Native: "Oʻzbek", Script: Latin},
	{Code: "vi", English: "Vietnamese", Native: "Tiếng Việt", Script: Latin},
	{Code: "yo", English: "Yoruba", Native: "Yorùbá", Script: Latin},
	{Code: "zh-Hans", English: "Chinese (Simplified)", Native: "简体中文", Script: Han},
	{Code: "zh-Hant", English: "Chinese (Traditional)", Native: "繁體中文", Script: Han},
	{Code: "zu", English: "Zulu", Native: "isiZulu", Script: Latin},
}

// verified are codes YouTube accepted for a video's translations on a test
// video (Oct 16, 2026), outside the table above and YouTube's own
// i18nLanguages list. Their names come from the Unicode CLDR data and their
// script from CLDR's likely script; an unknown script skips the script check.
// YouTube refused, with "invalidVideoMetadata": ae an ast av ce ceb ch ckb
// cu cv dv gv hz ii io kg kj kr kv kw li mh ng nn ny oj os pi se ty za.
var verified = []string{
	"aa", "ab", "ak", "ay", "ba", "bh", "bi", "bm", "bo", "br", "chr", "co", "cr", "dz", "ee", "eo",
	"ff", "fj", "fo", "fy", "gd", "gn", "haw", "ho", "ht", "ia", "ie", "ik", "iu", "ki", "kl", "ks",
	"ku", "la", "lb", "lg", "ln", "lu", "mg", "mi", "mni", "mt", "na", "nd", "nr", "nv", "oc", "om",
	"ps", "qu", "rm", "rn", "rw", "sa", "sat", "sc", "sd", "sg", "sm", "sn", "ss", "st", "su", "tg",
	"ti", "tk", "tn", "to", "ts", "tt", "tw", "ug", "ve", "vo", "wa", "wo", "xh", "yi", "yue",
	"en-IN", "es-US", "sr-Latn",
}

// notTranslated are verified codes the list leaves out because the model did
// not translate into them. Measured with gemma4:26b on Oct 17, 2026 (Friday
// night Pacific), all 165 languages on test video 1, an English video. No
// usable answer in three tries: aa ab chr cr ee haw kl lu mni mt nv sat sg ug
// vo wo yi. A related language instead: Quechua for ay, Nigerian Pidgin for
// ho, Italian for ie, Inuktitut for ik, Zulu for nd and nr, Kinyarwanda for
// rn, Corsican for sc, Akan for tw, Northern Sotho for ve, French for wa.
// Languages that only failed a check (added words, a long title) stay in the
// list: the checks and the read-back hold those back for the person.
var notTranslated = []string{
	"aa", "ab", "chr", "cr", "ee", "haw", "kl", "lu", "mni", "mt", "nv", "sat", "sg", "ug", "vo", "wo", "yi",
	"ay", "ho", "ie", "ik", "nd", "nr", "rn", "sc", "tw", "ve", "wa",
}

// cldrScripts maps CLDR's ISO 15924 script codes to the scripts we check.
var cldrScripts = map[string]Script{
	"Latn": Latin, "Cyrl": Cyrillic, "Grek": Greek, "Armn": Armenian, "Geor": Georgian, "Hebr": Hebrew,
	"Arab": Arabic, "Deva": Devanagari, "Beng": Bengali, "Guru": Gurmukhi, "Gujr": Gujarati, "Orya": Oriya,
	"Taml": Tamil, "Telu": Telugu, "Knda": Kannada, "Mlym": Malayalam, "Sinh": Sinhala, "Thai": Thai,
	"Laoo": Lao, "Khmr": Khmer, "Mymr": Myanmar, "Ethi": Ethiopic, "Hang": Hangul, "Kore": Hangul,
	"Jpan": Japanese, "Hans": Han, "Hant": Han,
	"Tibt": Tibetan, "Cher": Cherokee, "Cans": Syllabics, "Olck": OlChiki,
}

// nameFixes gives distinct names where CLDR repeats another entry's name
// (tw is "Akan" like ak, es-US is "Spanish" like es).
var nameFixes = map[string][2]string{
	"tw":      {"Twi", "Twi"},
	"es-US":   {"Spanish (United States)", "Español (Estados Unidos)"},
	"en-IN":   {"English (India)", "English (India)"},
	"sr-Latn": {"Serbian (Latin)", "Srpski (latinica)"},
}

// scriptFixes covers codes CLDR gives no usable script for.
var scriptFixes = map[string]Script{"ie": Latin, "tw": Latin, "bh": Devanagari, "bo": Tibetan, "dz": Tibetan, "chr": Cherokee, "cr": Syllabics, "iu": Syllabics, "sat": OlChiki}

// fromCLDR builds a table entry for a verified code.
func fromCLDR(code string) (Language, bool) {
	tag, err := language.Parse(code)
	if err != nil {
		return Language{}, false
	}
	english := display.English.Languages().Name(tag)
	if english == "" {
		return Language{}, false
	}
	l := Language{Code: code, English: english, Native: selfName(code, english)}
	if fix, ok := nameFixes[code]; ok {
		l.English, l.Native = fix[0], fix[1]
	}
	if sc, conf := tag.Script(); conf != language.No {
		l.Script = cldrScripts[sc.String()]
	}
	if fix, ok := scriptFixes[code]; ok {
		l.Script = fix
	}
	return l, true
}

// aliases maps codes YouTube also uses to the code in our table.
var aliases = map[string]string{
	"pt":    "pt-BR",
	"zh-CN": "zh-Hans",
	"zh-SG": "zh-Hans",
	"zh-TW": "zh-Hant",
	"zh-HK": "zh-Hant",
	"iw":    "he",
	"nb":    "no",
	"tl":    "fil",
	"in":    "id",
}

var byCode map[string]Language

func init() {
	// Entries with an empty native name take it from the Unicode CLDR data
	// that ships with golang.org/x/text.
	for i, l := range table {
		if l.Native == "" {
			table[i].Native = selfName(l.Code, l.English)
		}
	}
	skip := map[string]bool{}
	for _, code := range notTranslated {
		skip[code] = true
	}
	for _, code := range verified {
		if skip[code] {
			continue
		}
		if l, ok := fromCLDR(code); ok {
			table = append(table, l)
		}
	}
	ranked := rankAll()
	byCode = make(map[string]Language, len(ranked))
	for _, l := range ranked {
		byCode[strings.ToLower(l.Code)] = l
	}
}

// rankAll returns the table ordered: popular, next, then the rest by English name.
func rankAll() []Language {
	order := map[string]int{}
	for i, c := range Popular {
		order[c] = i + 1
	}
	for i, c := range next {
		order[c] = len(Popular) + i + 1
	}
	all := append([]Language(nil), table...)
	sort.SliceStable(all, func(i, j int) bool {
		oi, iok := order[all[i].Code]
		oj, jok := order[all[j].Code]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		default:
			return all[i].English < all[j].English
		}
	})
	for i := range all {
		all[i].Rank = i + 1
		all[i].Popular = i < len(Popular)
	}
	return all
}

// selfName is the language's name in its own language, capitalized.
func selfName(code, fallback string) string {
	tag, err := language.Parse(code)
	if err != nil {
		return fallback
	}
	name := display.Self.Name(tag)
	if name == "" {
		return fallback
	}
	r, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToUpper(r)) + name[size:]
}

// All returns every language, most watched first. The source language, when
// given, is left out.
func All(source string) []Language {
	src := Canonical(source)
	var out []Language
	for _, l := range rankAll() {
		if src != "" && SameBase(l.Code, src) {
			continue
		}
		out = append(out, l)
	}
	return out
}

// Get finds a language by code or alias, case-insensitively.
func Get(code string) (Language, bool) {
	l, ok := byCode[strings.ToLower(Canonical(code))]
	return l, ok
}

// Canonical maps an alias to the code in the table and fixes the case of
// known codes. Unknown codes are returned unchanged.
func Canonical(code string) string {
	code = strings.TrimSpace(code)
	for alias, target := range aliases {
		if strings.EqualFold(alias, code) {
			return target
		}
	}
	for _, l := range table {
		if strings.EqualFold(l.Code, code) {
			return l.Code
		}
	}
	return code
}

// SameBase reports whether two codes share a primary language subtag, for
// example "en" and "en-GB". It is used to leave the source language out.
func SameBase(a, b string) bool {
	base := func(s string) string {
		s = strings.ToLower(Canonical(s))
		if i := strings.IndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
		return s
	}
	return a != "" && b != "" && base(a) == base(b)
}

// Describe returns the table entry for a code or, for a code outside the
// table, names from the Unicode CLDR data and an unknown script (the script
// check is then skipped, not failed). ok is false when CLDR does not know it.
func Describe(code string) (Language, bool) {
	if l, ok := Get(code); ok {
		return l, true
	}
	tag, err := language.Parse(strings.TrimSpace(code))
	if err != nil {
		return Language{}, false
	}
	english := display.English.Languages().Name(tag)
	if english == "" {
		return Language{}, false
	}
	return Language{Code: tag.String(), English: english, Native: selfName(tag.String(), english)}, true
}

// Name is the native name for a code, or the code itself when unknown.
func Name(code string) string {
	if l, ok := Describe(code); ok {
		return l.Native
	}
	return code
}

// EnglishName is the English name for a code, or the code itself.
func EnglishName(code string) string {
	if l, ok := Describe(code); ok {
		return l.English
	}
	return code
}

// Sort orders codes by rank; unknown codes go last in the order given.
func Sort(codes []string) []string {
	out := append([]string(nil), codes...)
	sort.SliceStable(out, func(i, j int) bool {
		li, iok := Get(out[i])
		lj, jok := Get(out[j])
		switch {
		case iok && jok:
			return li.Rank < lj.Rank
		default:
			return iok && !jok
		}
	})
	return out
}
