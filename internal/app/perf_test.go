package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Karaoke sleeps while paused, after the lift settles, and through gaps, with reduced motion updating only at word boundaries.
func TestKaraokeEffectFrameBudget(t *testing.T) {
	words := []lyrics.Word{{Start: time.Second, End: 3 * time.Second}, {Start: 8 * time.Second, End: 10 * time.Second}}
	if got := lyricEffectDelay(words, 2*time.Second, true, false); got != lyricEffectFrame {
		t.Fatalf("active sweep cadence: %v", got)
	}
	if got := lyricEffectDelay(words, 4*time.Second, true, false); got != 4*time.Second {
		t.Fatalf("gap must sleep until the next word, got %v", got)
	}
	if got := lyricEffectDelay(words, 2*time.Second, true, true); got != time.Second {
		t.Fatalf("reduced motion must wait for the word boundary, got %v", got)
	}
	for _, state := range []struct {
		pos     time.Duration
		playing bool
	}{{2 * time.Second, false}, {11 * time.Second, true}} {
		if got := lyricEffectDelay(words, state.pos, state.playing, false); got != 0 {
			t.Fatalf("settled or paused lyrics still schedule frames: %v", got)
		}
	}
}

// The timed ink repaints without rebuilding its view and stops its timer as soon as playback pauses.
func TestKaraokePaintDoesNotRebuild(t *testing.T) {
	builds, paints := 0, 0
	playing := true
	line := lyrics.Line{Text: "Moonlight", Words: []lyrics.Word{{Text: "Moonlight", Start: 0, End: 10 * time.Second}}}
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		ui.Box(c).Fill().Children(func() {
			karaokeText(c, line, ui.Font{Size: 30, Weight: 700, LineHeight: 1.25}, white.Alpha(.38), white,
				func() time.Duration { paints++; return time.Second }, func() bool { return playing }, false)
		})
	}, 400, 100)
	beforeBuilds, beforePaints := builds, paints
	tt.Advance(time.Second)
	if builds != beforeBuilds || paints-beforePaints < 25 || paints-beforePaints > 31 {
		t.Fatalf("a second of ink: %d builds and %d paints", builds-beforeBuilds, paints-beforePaints)
	}
	playing = false
	tt.Advance(time.Second) // the already armed paint observes the pause
	beforePaints = paints
	tt.Advance(time.Second)
	if paints != beforePaints {
		t.Fatalf("paused ink keeps repainting: %d more paints", paints-beforePaints)
	}
}

// Fusion must animate line-only sources during singing, but stop requesting animation frames when paused or finished.
func TestFusionMotionFrameBudget(t *testing.T) {
	lines := []lyrics.Line{{At: time.Second, End: 5 * time.Second, Text: "晚风轻轻吹过窗前"}}
	if delay := fusionFrameDelay(lines, 2*time.Second, true, false); delay < time.Second/60 || delay > time.Second/55 {
		t.Fatalf("line-only animation cadence: %v", delay)
	}
	if fusionFrameDelay(lines, 2*time.Second, false, false) != 0 || fusionFrameDelay(lines, 10*time.Second, true, false) != 0 {
		t.Fatal("paused or finished lyrics still schedule frames")
	}
	if delay := fusionFrameDelay(lines, 2*time.Second, true, true); delay < time.Second {
		t.Fatal("reduced motion still runs at animation speed")
	}
}

// playingApp is an app playing a track with lyrics, at a position the test
// sets with pl.Seek, and the number of times its view was built.
func playingApp() (*app, *ui.Tester, *int) {
	a := newTestApp()
	a.settings = defaultSettings()
	tr := spotify.Track{ID: "7", Name: "宝船", Artists: []spotify.Artist{{Name: "DOUDOU"}}, Duration: 200000}
	a.pl.Cue([]spotify.Track{tr})
	// A line every 10 seconds, sung over the first 5 of them.
	var lrc string
	for i := range 12 {
		lrc += "[0" + string(rune('0'+i*10/60)) + ":" + string(rune('0'+i*10%60/10)) + "0.00]一二三四五六\n"
	}
	lines := lyrics.Build(lrc, "", "")
	for i := range lines {
		l := &lines[i]
		l.End = l.At + 5*time.Second
		l.Words = lyrics.SpreadWords(l.Text, l.At, l.End)
	}
	a.pl.SetLyrics(lines)
	a.pl.Hold(0)
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) { builds++; a.view(c) }, 1200, 800)
	return a, tt, &builds
}

// framesAt counts the frames the view asks for over a second, at 60 a
// second, with playback held at pos.
func framesAt(a *app, tt *ui.Tester, pos time.Duration) int {
	a.pl.Seek(pos)
	tt.Frame()
	tt.Advance(time.Second)
	tt.Frame()
	asked := 0
	for range 60 {
		tt.Advance(time.Second / 60)
		if tt.FramePending() {
			asked++
			tt.Frame()
		}
	}
	return asked
}

// Now Playing asks for frames only as fast as what it shows changes. The
// Tester runs timers as it settles a frame, so this counts only what does
// not change for a while: a frame log of the app (MYGO_FRAME_STATS) measures
// the rest.
func TestNowPlayingFrameBudget(t *testing.T) {
	a, tt, _ := playingApp()
	if n := framesAt(a, tt, 12*time.Second); n > 3 {
		t.Errorf("a page under the player: %d frames a second", n)
	}
	a.nowPlaying, a.npPane = true, paneStage
	if n := framesAt(a, tt, 18*time.Second); n > 12 {
		t.Errorf("the stage between lines: %d frames a second", n)
	}
}

// Another song's backdrop fades in over the last one's, which is then let
// go, so that the backdrop stops asking for frames.
func TestBackdropFadeEnds(t *testing.T) {
	a, tt, _ := coverApp(t)
	a.art.art[spotify.Sized("a", 600)+"#backdrop"] = &artwork{tint: ui.RGB(200, 40, 40)}
	a.art.art[spotify.Sized("b", 600)+"#backdrop"] = &artwork{tint: ui.RGB(40, 40, 200)}
	q := a.pl.Queue()
	q[1].Album.PicURL, q[2].Album.PicURL = "a", "b"
	tt.Frame()
	a.pl.Next()
	tt.Frame()
	if a.backdropCache.before == nil {
		t.Fatal("the backdrop changed songs without fading")
	}
	tt.Advance(backdropFade + 100*time.Millisecond)
	tt.Frame()
	if a.backdropCache.before != nil {
		t.Error("the last song's backdrop still fades after the fade")
	}
}

// The island's emblem froze and jumped through the intro's credits, when no
// lyric line asks for frames; the floating view now keeps a slow beat going
// while playing, and still rests when paused or motion is reduced.
func TestFusionIntroFrameBudget(t *testing.T) {
	lines := prepareFusionLines([]lyrics.Line{
		{At: 0, Text: "作词: 测试"},
		{At: 20 * time.Second, End: 25 * time.Second, Text: "第一句歌词"},
	})
	if delay := fusionFloatingDelay(lines, time.Second, true, false); delay != fusionIntroFrame {
		t.Fatalf("intro credit cadence: %v", delay)
	}
	if delay := fusionFloatingDelay(lines, 21*time.Second, true, false); delay != fusionMotionFrame {
		t.Fatalf("sung line cadence: %v", delay)
	}
	if delay := fusionFloatingDelay(lines, time.Second, false, false); delay != 0 {
		t.Fatalf("paused intro still schedules frames: %v", delay)
	}
	if delay := fusionFloatingDelay(lines, time.Second, true, true); delay < time.Second {
		t.Fatalf("reduced motion still runs at emblem speed: %v", delay)
	}
}
