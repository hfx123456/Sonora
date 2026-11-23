package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func TestPlayerBarRemovesDuplicateLyricsAndShowsCoverHoverCard(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.barCredits = map[string][]spotify.Credit{
		"t1": {
			{Name: "潘云安", Roles: []string{"Lyricist", "Composer"}},
			{Name: "陈君豪", Roles: []string{"Producer"}},
			{Name: "告五人", Roles: []string{"Main Artist"}},
		},
	}
	a.pl.Cue([]spotify.Track{{
		ID:       "t1",
		Name:     "带我去找夜生活",
		Artists:  []spotify.Artist{{ID: "a1", Name: "告五人"}},
		Album:    spotify.Album{ID: "al1", Name: "运气来得若有似无"},
		Duration: 300000,
	}})

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// The redundant lyrics button next to the queue icon is removed.
	if err := tt.Click("歌词"); err == nil {
		t.Fatal("expected redundant lyrics button to be removed from player bar")
	}

	// Hovering over the album cover in the bottom-left player bar reveals credits without duplicating artwork.
	if tt.HasText("制作人员") {
		t.Fatal("cover hover card should not be visible before hovering")
	}
	infoRect, ok := tt.Find("打开正在播放")
	if !ok {
		t.Fatal("player bar track info button not found")
	}
	tt.Move(infoRect.X+24, infoRect.Y+infoRect.H/2)
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	tt.Frame()
	for _, want := range []string{"制作人员", "作词", "作曲", "潘云安", "制作人", "陈君豪", "运气来得若有似无"} {
		if !tt.HasText(want) {
			t.Fatalf("expected %q in cover hover credits card; texts=%v", want, tt.Texts())
		}
	}

	// Opening queue switches to paneQueue, and clicking the album cover afterward always returns to lyrics.
	if err := tt.Click("播放队列"); err != nil {
		t.Fatal(err)
	}
	if !a.nowPlaying || a.npPane != paneQueue {
		t.Fatalf("after clicking queue: nowPlaying=%v npPane=%v", a.nowPlaying, a.npPane)
	}
	a.nowPlaying = false
	tt.Frame()
	tt.Advance(500 * time.Millisecond)
	tt.Frame()
	tt.ClickAt(infoRect.X+24, infoRect.Y+infoRect.H/2)
	tt.Frame()
	if !a.nowPlaying || a.npPane != a.lyricsPaneChosen() {
		t.Fatalf("clicking album cover after queue: nowPlaying=%v npPane=%v want=%v", a.nowPlaying, a.npPane, a.lyricsPaneChosen())
	}
}
