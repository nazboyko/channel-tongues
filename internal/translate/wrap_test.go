package translate

import (
	"context"
	"strings"
	"testing"
)

// The description of a real test video, wrapped mid-sentence by hand.
const wrappedSource = "The town is imagined, not a real place. The picture and the music were made with\n" +
	"AI tools and put together by hand.\n" +
	"\n" +
	"The Living Postcard: quiet places and soft piano. New postcards every Tuesday and Friday."

func TestJoinSoftWrapsOnTheRealSource(t *testing.T) {
	got := JoinSoftWraps(wrappedSource)
	want := "The town is imagined, not a real place. The picture and the music were made with AI tools and put together by hand.\n" +
		"\n" +
		"The Living Postcard: quiet places and soft piano. New postcards every Tuesday and Friday."
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestJoinSoftWrapsKeepsRealStructure(t *testing.T) {
	cases := []struct{ name, in string }{
		{"timestamps", "⟦1⟧ Rain begins\n⟦2⟧ Distant thunder"},
		{"a line before a timestamp list", "Chapters of this long relaxing night video\n⟦1⟧ Rain begins"},
		{"a link at the end", "More nights and more rain on my channel at ⟦3⟧\nand on my website"},
		{"hashtags", "Three hours of rain on a quiet cabin window tonight\n⟦5⟧ ⟦6⟧"},
		{"a tagline", "The Slow Night Hours\nQuiet scenes and soft music for deep sleep every night"},
		{"list markers", "What you hear in this three hour video\n- rain on the window\n- distant thunder"},
		{"numbered list", "1. Rain begins and slowly gets heavier over the hour\n2. Thunder"},
		{"a sentence end", "No ads in the middle of the video.\nRecorded on a quiet autumn night."},
		{"a colon", "Recorded with the following microphones and recorders:\nZoom H5"},
		{"a closing quote", "My grandmother always called this \"the sleeping rain”\nso I kept the name"},
		{"paragraphs", "First paragraph of the description is here\n\nSecond paragraph"},
	}
	for _, tc := range cases {
		if got := JoinSoftWraps(tc.in); got != tc.in {
			t.Errorf("%s: changed to %q", tc.name, got)
		}
	}
}

func TestJoinSoftWrapsJoinsSeveralLines(t *testing.T) {
	in := "Three hours of soft rain on a wooden cabin window,\nrecorded on a quiet autumn night in the\nmountains of the north."
	if got := JoinSoftWraps(in); strings.Count(got, "\n") != 0 {
		t.Fatalf("one sentence over three lines becomes one line: %q", got)
	}
}

func TestEngineTranslatesWrappedSentencesWhole(t *testing.T) {
	var got Request
	m := &scripted{answers: map[string][]func(Request) (Output, error){
		"tr": {func(r Request) (Output, error) {
			got = r
			// Three lines: the sentence whole, the blank line, the tagline.
			return Output{"sonbahar gölü 30sn", "Kasaba hayalidir, gerçek bir yer değildir. Görüntü ve müzik yapay zeka araçlarıyla yapılmış ve elle bir araya getirilmiştir.\n\n⟦1⟧: sakin yerler ve yumuşak piyano. Her Salı ve Cuma yeni kartpostallar."}, nil
		}},
	}}
	src := Source{Title: "autumn lake 30s", Description: wrappedSource, Language: "en"}
	res := (&Engine{Model: m}).Run(context.Background(), src, []string{"tr"}, []string{"The Living Postcard"}, nil)
	if strings.Count(got.Source.Description, "\n") != 2 {
		t.Fatalf("the model gets the sentence whole: %q", got.Source.Description)
	}
	if res[0].Status != StatusReady {
		t.Fatalf("a translation that keeps the sentence whole passes the line check: %+v", res[0])
	}
	if strings.Contains(res[0].Description, "müzik\n") {
		t.Fatal("no break in the middle of the sentence")
	}
}
