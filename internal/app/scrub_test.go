package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

// Dragging the progress bar seeks once, where it is let go: not as the
// pointer moves, even off the thin bar, and not to the end, which would
// play the next song, before it is let go.
func TestScrubSeeksOnRelease(t *testing.T) {
	for _, pane := range []struct {
		name string
		pane int
	}{{"lyrics", paneLyrics}, {"stage", paneStage}} {
		t.Run(pane.name, func(t *testing.T) {
			a := newTestApp()
			a.settings = defaultSettings()
			tr := spotify.Track{ID: "7", Name: "宝船", Duration: 200000}
			a.pl.Cue([]spotify.Track{tr, {ID: "8", Name: "下一首"}})
			a.pl.SetLyrics(nil)
			out := audio.NewPlayer(&silence{})
			out.SetVolume(0)
			out.Play()
			a.pl.Attach(out)
			defer out.Close()
			a.nowPlaying, a.npPane = true, pane.pane
			tt := ui.NewTester(a.view, 1200, 800)
			tt.Move(600, 400)
			tt.Advance(time.Second)
			tt.Frame()
			bar, ok := tt.Find("播放进度")
			if !ok {
				t.Fatal("no progress bar")
			}
			y := bar.Y + bar.H/2
			at := func(f float32) float32 { return bar.X + bar.W*f }

			tt.Press(at(0.25), y)
			tt.Frame()
			tt.Move(at(0.6), y-40) // off the bar, above it
			tt.Frame()
			tt.Move(at(1)+30, y+30) // past its end, below it
			tt.Frame()
			if pos := a.pl.Position(); pos > 10*time.Second {
				t.Fatalf("seeked to %v while dragging", pos)
			}
			if a.pl.Current().ID != "7" {
				t.Fatal("went to the next song while dragging")
			}
			tt.Move(at(0.6), y-40)
			tt.Frame()
			tt.Release(at(0.6), y-40)
			tt.Frame()
			tt.Frame()
			if pos := a.pl.Position(); pos < 110*time.Second || pos > 130*time.Second {
				t.Errorf("let go at 60%%, at %v", pos)
			}
		})
	}
}

// Releasing a drag after the song changes must not seek the replacement song.
func TestStageScrubIgnoresTrackChangeDuringDrag(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "first", Duration: 200000}})
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneStage
	tt := ui.NewTester(a.view, 880, 580)
	tt.Move(440, 290)
	tt.Advance(time.Second)
	tt.Frame()
	bar, ok := tt.Find("播放进度")
	if !ok {
		t.Fatal("no progress bar")
	}
	y := bar.Y + bar.H/2
	tt.Press(bar.X+bar.W*.3, y)
	tt.Frame()

	a.pl.Cue([]spotify.Track{{ID: "second", Duration: 200000}})
	a.pl.SetLyrics(nil)
	a.pl.Seek(7 * time.Second)
	tt.Frame()
	x := bar.X + bar.W*.6
	tt.Move(x, y)
	tt.Release(x, y)
	tt.Frame()
	if pos := a.pl.Position(); pos != 7*time.Second {
		t.Fatalf("a drag from the previous track moved the new track to %v", pos)
	}
}

// Leaving the window hides the stage controls after an active seek is released, and reentering makes them usable again.
func TestStageControlsHideOutsideWindow(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "stage", Duration: 200000}})
	a.pl.SetLyrics(nil)
	a.nowPlaying, a.npPane = true, paneStage
	tt := ui.NewTester(a.view, 880, 580)
	tt.Move(440, 290)
	tt.Advance(time.Second)
	tt.Frame()
	if _, ok := tt.Find("播放进度"); !ok {
		t.Fatal("the controls did not appear on entry")
	}
	tt.Move(440, 620)
	tt.Advance(300 * time.Millisecond)
	if _, ok := tt.Find("播放进度"); ok {
		t.Fatal("the controls stayed available outside the window")
	}

	tt.Move(440, 290)
	tt.Advance(300 * time.Millisecond)
	bar, ok := tt.Find("播放进度")
	if !ok {
		t.Fatal("the progress bar did not return")
	}
	tt.Press(bar.X+bar.W*.3, bar.Y+bar.H/2)
	x := bar.X + bar.W*.6
	tt.Move(x, 620)
	tt.Advance(300 * time.Millisecond)
	if !a.npScrub.held || a.pl.Position() != 0 {
		t.Fatal("leaving the window interrupted the drag")
	}
	tt.Release(x, 620)
	tt.Advance(300 * time.Millisecond)
	if pos := a.pl.Position(); pos < 118*time.Second || pos > 122*time.Second {
		t.Fatalf("release outside the window sought to %v", pos)
	}
	if _, ok := tt.Find("播放进度"); ok {
		t.Fatal("the controls stayed available after the drag ended")
	}

	tt.Move(440, 290)
	tt.Advance(300 * time.Millisecond)
	repeat := a.pl.Repeat()
	if err := tt.Click("循环"); err != nil {
		t.Fatal(err)
	}
	if a.pl.Repeat() == repeat {
		t.Fatal("the controls did not handle clicks after reentry")
	}
}
