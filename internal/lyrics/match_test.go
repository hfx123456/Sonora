package lyrics

import "testing"

func TestBilingualParenMatch(t *testing.T) {
	cases := []struct {
		want, cand string
		ok         bool
		base       string
	}{
		{"Haru Haru", "Haru Haru (一天一天)", true, "Haru Haru"},
		{"HaruHaru", "Haru Haru (一天一天)", true, "Haru Haru"},
		{"Haru Haru Haru", "春春春 (Haru Haru Haru)", true, "春春春"},
		{"Look at me only", "나만 바라봐（Look at me only） (只看着我)", true, "나만 바라봐"},
		{"Haru Haru", "Haru Haru (Live)", false, ""},
		{"Haru Haru", "Haru Haru (Ver. Heartbreak)", false, ""},
		{"Haru Haru", "Haru Haru -Japanese Version-", false, ""},
		{"Haru Haru", "Haru Haru (Part 2)", false, ""},
		{"Haru Haru", "하루하루 (一天一天)", false, ""},
	}
	for _, c := range cases {
		ok, base := BilingualParenMatch(c.want, c.cand)
		if ok != c.ok || base != c.base {
			t.Errorf("BilingualParenMatch(%q, %q) = (%v, %q), want (%v, %q)", c.want, c.cand, ok, base, c.ok, c.base)
		}
	}
}

func TestCrossScriptAndAlbumArtistMatch(t *testing.T) {
	if !CrossScript("Haru Haru", "하루하루") {
		t.Fatal("expected CrossScript for Haru Haru vs 하루하루")
	}
	if !CrossScript("Haru Haru", "하루하루 (一天一天)") {
		t.Fatal("expected CrossScript for Haru Haru vs 하루하루 (一天一天)")
	}
	if CrossScript("Rain", "Pain") {
		t.Fatal("unexpected CrossScript for Rain vs Pain")
	}
	if !ArtistsMatch([]string{"BIGBANG"}, []string{"BIGBANG (빅뱅)"}) {
		t.Fatal("expected ArtistsMatch for BIGBANG vs BIGBANG (빅뱅)")
	}
	if ArtistsMatch([]string{"AI"}, []string{"Aimer"}) {
		t.Fatal("unexpected ArtistsMatch for AI vs Aimer")
	}
	if !AlbumMatches("Stand Up", "3rd Mini Album : Stand Up") {
		t.Fatal("expected AlbumMatches for Stand Up vs 3rd Mini Album : Stand Up")
	}
	if !AlbumMatches("Solar", "Solar (International Release Album)") {
		t.Fatal("expected AlbumMatches for Solar")
	}
	if AlbumMatches("Stand Up", "Number 1") {
		t.Fatal("unexpected AlbumMatches for Stand Up vs Number 1")
	}
}

func TestHasExtraVersionTag(t *testing.T) {
	if !HasExtraVersionTag("Haru Haru", "Stand Up", "Haru Haru -Japanese Version-") {
		t.Fatal("expected extra version tag for Japanese Version")
	}
	if !HasExtraVersionTag("Haru Haru", "Stand Up", "하루하루 (一天一天) (Ver. Heartbreak)") {
		t.Fatal("expected extra version tag for Ver. Heartbreak")
	}
	if HasExtraVersionTag("Alive", "", "Alive (一天一天)") {
		t.Fatal("false positive on Alive")
	}
	if HasExtraVersionTag("Haru Haru (Acoustic)", "", "하루하루 (Acoustic)") {
		t.Fatal("rejected requested acoustic version")
	}
}
