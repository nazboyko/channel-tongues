package main

import "testing"

func TestMarkAdded(t *testing.T) {
	cases := [][3]string{
		{"Rain on a Mountain cabin", "mountain", "Rain on a [Mountain] cabin"},
		{"un été calme", "été", "un [été] calme"},
		{"mountainous", "mountain", "mountainous"},
	}
	for _, c := range cases {
		if got := markAdded(c[0], []string{c[1]}); got != c[2] {
			t.Errorf("markAdded(%q, %q) = %q, want %q", c[0], c[1], got, c[2])
		}
	}
	if readsLanguage("") != "English" || readsLanguage("de") != "German" || readsLanguage("zxx") != "the video's language" {
		t.Fatal("readsLanguage")
	}
}
