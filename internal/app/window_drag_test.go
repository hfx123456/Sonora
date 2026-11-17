package app

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

// Glass mode used to remove the page header's drag target together with its background; scrolling and navigation must preserve the native move request.
func TestPageHeaderDragsWindow(t *testing.T) {
	for _, glass := range []bool{false, true} {
		t.Run(fmt.Sprint("glass=", glass), func(t *testing.T) {
			a := newTestApp()
			a.settings = defaultSettings()
			a.settings.Glass = glass
			a.homeSections = testSections(3)
			a.history = []route{{page: pageHome}}
			a.forward = []route{{page: pageHome}}
			tt := ui.NewTester(a.view, 1200, 800)
			drag := func() {
				t.Helper()
				before := tt.WindowDragRequests()
				tt.Press(740, 20)
				tt.Move(800, 40)
				tt.Release(800, 40)
				if got := tt.WindowDragRequests() - before; got != 1 {
					t.Fatalf("pressing the header requested %d window moves, want 1", got)
				}
			}
			drag()
			tt.Scroll(900, 500, 0, 700)
			tt.Advance(time.Second)
			drag()
			before := tt.WindowDragRequests()
			if err := tt.Click("返回"); err != nil {
				t.Fatal(err)
			}
			if len(a.history) != 0 {
				t.Fatal("the header drag target intercepted the back button")
			}
			if err := tt.Click("前进"); err != nil {
				t.Fatal(err)
			}
			if len(a.history) != 1 || tt.WindowDragRequests() != before {
				t.Fatal("navigation moved the window instead of navigating")
			}
		})
	}
}

// Now Playing's empty top strip moves the window while its pane tabs still respond to clicks.
func TestNowPlayingHeaderDragsWindow(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "drag", Name: "Song", Duration: 200000}})
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneLyrics
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Advance(time.Second)
	tt.Press(300, 20)
	tt.Move(360, 40)
	tt.Release(360, 40)
	if tt.WindowDragRequests() != 1 {
		t.Fatal("Now Playing's header did not request a window move")
	}
	if err := tt.Click("列表"); err != nil {
		t.Fatal(err)
	}
	if a.npPane != paneQueue || tt.WindowDragRequests() != 1 {
		t.Fatal("the header drag target intercepted the pane tab")
	}
}
