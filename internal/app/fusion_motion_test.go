package app

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Line-only lyrics previously stayed static; visual timings now animate them without claiming real word sync.
func TestFusionLineOnlyMotion(t *testing.T) {
	input := []lyrics.Line{{At: time.Second, End: 5 * time.Second, Text: "晚风轻轻吹过窗前"}, {At: 5 * time.Second, End: 8 * time.Second, Text: "作词: Test"}, {Text: "Plain", Unsynced: true}}
	lines := prepareFusionLines(input)
	if len(lines[0].Words) == 0 || lines[0].Timed {
		t.Fatal("line-only lyrics lack visual motion or claim real word timing")
	}
	if len(lines[1].Words) != 0 || len(lines[2].Words) != 0 {
		t.Fatal("animated credits or invented times for unsynced lyrics")
	}
	if before, _ := fusionWordMotion(lines[0].Words[0], 0); before != 0 {
		t.Fatal("word was lit before it started")
	}
	if progress, pulse := fusionWordMotion(lines[0].Words[0], lines[0].Words[0].Start+200*time.Millisecond); progress <= 0 || pulse <= 0 {
		t.Fatal("word did not light and lift")
	}
	if progress, pulse := fusionWordMotion(lines[0].Words[0], lines[0].Words[0].End+time.Second); progress != 1 || pulse != 0 {
		t.Fatal("finished word never settled")
	}
	real := []lyrics.Line{{At: time.Second, End: 4 * time.Second, Text: "你好", Timed: true, Words: []lyrics.Word{{Text: "你", Start: time.Second, End: 2 * time.Second}, {Text: "好", Start: 2 * time.Second, End: 4 * time.Second}}}}
	prepared := prepareFusionLines(real)
	if !prepared[0].Timed || prepared[0].Words[1].Start != 2*time.Second {
		t.Fatal("replaced real word sync")
	}
}

// Chinese translations previously stayed static; they now progress within the original sung interval.
func TestFusionTranslationMotion(t *testing.T) {
	original := lyrics.Line{At: time.Second, End: 8 * time.Second, Text: "ありがとう", Words: []lyrics.Word{{Text: "ありが", Start: 2 * time.Second, End: 4 * time.Second}, {Text: "とう", Start: 4 * time.Second, End: 6 * time.Second}}}
	translated := fusionTranslationLine(original, "谢谢你一直在我身边")
	if translated.At != 2*time.Second || translated.End != 6*time.Second || len(translated.Words) == 0 || translated.Timed {
		t.Fatalf("translation did not follow the original interval: %+v", translated)
	}
	word := translated.Words[0]
	before, _ := fusionWordMotion(word, word.Start-time.Millisecond)
	during, lift := fusionWordMotion(word, word.Start+(word.End-word.Start)/2)
	after, settled := fusionWordMotion(word, word.End+time.Second)
	if before != 0 || during <= 0 || during >= 1 || lift <= 0 || after != 1 || settled != 0 {
		t.Fatal("translation sweep or lift did not advance and settle")
	}
	original.Unsynced, original.Words = true, nil
	if len(fusionTranslationLine(original, "谢谢你").Words) != 0 {
		t.Fatal("invented translation timing for unsynced lyrics")
	}
}

// Opening during credits must reserve the later translation row instead of clipping bilingual lyrics.
func TestFusionIslandTranslationSpace(t *testing.T) {
	a := newTestApp()
	a.settings.FusionMode = "capsule"
	a.pl.Cue([]spotify.Track{{ID: "bilingual", Name: "Song"}})
	a.pl.Hold(time.Second)
	a.fusion.lines = []lyrics.Line{{At: 0, End: 3 * time.Second, Text: "作词: Test"}, {At: 3 * time.Second, End: 8 * time.Second, Text: "ありがとう", Translation: "谢谢你"}}
	_, intro := a.fusionWindowSize()
	a.pl.Hold(4 * time.Second)
	_, singing := a.fusionWindowSize()
	a.settings.FusionHideTranslation = true
	_, single := a.fusionWindowSize()
	if intro != singing || singing <= single {
		t.Fatalf("translation space changed or disappeared: intro=%d singing=%d single=%d", intro, singing, single)
	}
}

// After the final Chinese lyric, the capsule expands again to show the outro's track and artist.
func TestFusionChineseInterludeWindowHeight(t *testing.T) {
	a := newTestApp()
	a.settings.FusionMode = "capsule"
	a.pl.Cue([]spotify.Track{{ID: "interlude", Name: "回音(《天师钟馗》电视剧片尾曲)", Artists: []spotify.Artist{{Name: "周定纬"}}, Duration: 151327}})
	a.fusion.lines = []lyrics.Line{
		{At: 24 * time.Second, End: 28 * time.Second, Text: "错过的恋情飘零任风去打听"},
		{At: 123 * time.Second, End: 128 * time.Second, Text: "等爱情我永无止境"},
	}
	a.pl.Hold(124 * time.Second)
	_, singingH := a.fusionWindowSize()
	a.pl.Hold(132 * time.Second)
	_, outroH := a.fusionWindowSize()
	if outroH <= singingH {
		t.Fatalf("outro two-line caption did not expand capsule height: singing=%d outro=%d", singingH, outroH)
	}
}

// Changing island width leaves font scale and playback intact, and resetting restores the compact size.
func TestFusionIslandWidthControls(t *testing.T) {
	a := newTestApp()
	a.settings.FusionMode = "capsule"
	a.settings.FusionScale = 110
	a.pl.Cue([]spotify.Track{{ID: "size", Name: "Song"}})
	a.pl.Hold(time.Second)
	initial, _ := a.fusionWindowSize()
	tester := ui.NewTester(a.view, 1180, 780)
	choose := func(label string) {
		t.Helper()
		tester.Frame()
		if err := tester.Click("Fusion 双语歌词"); err != nil {
			t.Fatal(err)
		}
		if err := tester.ChooseMenuItem(label); err != nil {
			t.Fatal(err)
		}
	}
	choose("词岛加宽")
	wider, _ := a.fusionWindowSize()
	if wider <= initial || a.settings.FusionScale != 110 || !a.pl.Playing() {
		t.Fatal("width adjustment changed font scale or playback")
	}
	choose("词岛缩窄")
	if width, _ := a.fusionWindowSize(); width != initial {
		t.Fatal("width could not be restored")
	}
	choose("重置词岛大小")
	if a.settings.FusionIslandWidth != 0 || a.settings.FusionScale != 100 || !a.pl.Playing() {
		t.Fatal("size reset did not restore the compact island")
	}
	a.changeFusionIslandWidth(-10000)
	small := a.fusionIslandWidth()
	a.changeFusionIslandWidth(10000)
	if small <= 0 || a.fusionIslandWidth() > 960 {
		t.Fatal("width escaped its usable limits")
	}
}

// Caption changes retain one outgoing line, while a song change clears it to avoid stale lyrics.
func TestFusionCaptionTransitions(t *testing.T) {
	base := time.Unix(100, 0)
	var motion fusionCaptionMotion
	motion.update("song", fusionCaption{line: lyrics.Line{At: time.Second, Text: "第一句"}}, base)
	motion.update("song", fusionCaption{line: lyrics.Line{At: 3 * time.Second, Text: "第二句"}}, base.Add(time.Second))
	if motion.previous.line.Text != "第一句" || motion.phase(base.Add(time.Second), false) != 0 {
		t.Fatal("line changed without an outgoing caption")
	}
	if phase := motion.phase(base.Add(time.Second+180*time.Millisecond), false); phase <= 0 || phase >= 1 {
		t.Fatal("caption did not crossfade")
	}
	if motion.phase(base.Add(2*time.Second), false) != 1 {
		t.Fatal("caption transition never settles")
	}
	motion.update("other", fusionCaption{line: lyrics.Line{Text: "新歌"}}, base.Add(3*time.Second))
	if motion.previous.line.Text != "" || motion.phase(base.Add(3*time.Second), false) != 1 {
		t.Fatal("old song leaked into the new song")
	}
}

// Hover avoidance stays hidden while the cursor remains inside and restores after leaving, without event flicker.
func TestFusionHoverAvoidance(t *testing.T) {
	base := time.Unix(100, 0)
	var hover fusionHover
	hover.step(base, false, true)
	for frame := 1; frame <= 30; frame++ {
		hover.step(base.Add(time.Duration(frame)*20*time.Millisecond), true, true)
	}
	if hover.opacity != 0 {
		t.Fatalf("hover did not hide: %f", hover.opacity)
	}
	hover.step(base.Add(650*time.Millisecond), true, true)
	if hover.opacity != 0 {
		t.Fatal("hidden window flickered while cursor stayed over it")
	}
	hover.step(base.Add(700*time.Millisecond), false, true)
	if hover.opacity != 0 {
		t.Fatal("restored without the leave delay")
	}
	for frame := 1; frame <= 50; frame++ {
		hover.step(base.Add(700*time.Millisecond+time.Duration(frame)*20*time.Millisecond), false, true)
	}
	if hover.opacity != 1 {
		t.Fatalf("did not restore: %f", hover.opacity)
	}
	hover.step(base.Add(2*time.Second), true, false)
	if hover.opacity != 1 {
		t.Fatal("disabled avoidance still hid lyrics")
	}
}

// Attached islands use the display's physical top, not a saved y or a top taskbar's work-area inset.
func TestFusionPhysicalTopAnchor(t *testing.T) {
	displays := []mygo.Display{{Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1440}, WorkArea: mygo.Rectangle{X: 0, Y: 48, Width: 2560, Height: 1392}}, {Bounds: mygo.Rectangle{X: -1920, Y: -120, Width: 1920, Height: 1080}}}
	for _, sample := range []struct {
		bounds mygo.Rectangle
		wantY  int
	}{{mygo.Rectangle{X: 692, Y: 180, Width: 684, Height: 60}, 0}, {mygo.Rectangle{X: -1500, Y: 100, Width: 684, Height: 60}, -120}} {
		pinned := fusionPinnedBounds(sample.bounds, displays)
		if pinned.Y != sample.wantY || pinned.X != sample.bounds.X {
			t.Fatalf("wrong display anchor: %+v", pinned)
		}
	}
}

// Listeners can disable hover avoidance and motion, then restore the physical top from the playback menu.
func TestFusionMotionMenu(t *testing.T) {
	a := newTestApp()
	tester := ui.NewTester(a.view, 1180, 780)
	for _, choice := range []string{"歌词动态效果", "鼠标移入词岛自动避让", "词岛贴顶居中"} {
		tester.Frame()
		if err := tester.Click("Fusion 双语歌词"); err != nil {
			t.Fatal(err)
		}
		if err := tester.ChooseMenuItem(choice); err != nil {
			t.Fatal(err)
		}
	}
	if !a.settings.FusionStatic || !a.settings.FusionNoAvoid || a.settings.FusionFloatingIsland || a.settings.FusionMode != "capsule" {
		t.Fatal("motion controls did not update preferences")
	}
}

// Capsule island width tracks lyric length and springs smoothly with elastic overshoot across line changes.
func TestFusionIslandDynamicWidthAndSpringAnimation(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionMode = "capsule"
	a.pl.Cue([]spotify.Track{{ID: "dyn", Name: "Song", Duration: 200000}})
	a.fusion.lines = []lyrics.Line{
		{At: 0, End: 4 * time.Second, Text: "你好"},
		{At: 4 * time.Second, End: 10 * time.Second, Text: "ありがとう 君がいてくれて本当よかったよ", Translation: "谢谢 有你在身边真好"},
	}

	width, height := a.fusionWindowSize()
	tester := ui.NewTester(a.fusionOverlay, width, height)

	a.pl.Hold(time.Second)
	tester.Frame()
	shortW, shortH := a.fusion.capsuleW, a.fusion.capsuleH

	a.pl.Hold(5 * time.Second)
	tester.Advance(100 * time.Millisecond)
	tester.Frame()
	midW, midH := a.fusion.capsuleW, a.fusion.capsuleH
	if midW <= shortW || midH <= shortH {
		t.Fatalf("island did not begin expanding smoothly: short=(%v,%v) mid=(%v,%v)", shortW, shortH, midW, midH)
	}

	// Around the first spring peak (~300-350ms), width should overshoot its settled length like Dynamic Island.
	var peakW float32
	for step := range 16 {
		_ = step
		tester.Advance(25 * time.Millisecond)
		tester.Frame()
		if a.fusion.capsuleW > peakW {
			peakW = a.fusion.capsuleW
		}
	}

	tester.Advance(2 * time.Second)
	tester.Frame()
	longW, longH := a.fusion.capsuleW, a.fusion.capsuleH
	if longW <= midW || longH <= shortH {
		t.Fatalf("island did not settle larger for bilingual lyric: short=(%v,%v) long=(%v,%v)", shortW, shortH, longW, longH)
	}
	if peakW <= longW {
		t.Fatalf("spring did not overshoot target like Dynamic Island: peak=%v settled=%v", peakW, longW)
	}

	a.settings.FusionStatic = true
	a.pl.Hold(time.Second)
	tester.Frame()
	if a.fusion.capsuleW != shortW || a.fusion.capsuleH != shortH {
		t.Fatalf("static mode did not snap back cleanly: got=(%v,%v) want=(%v,%v)", a.fusion.capsuleW, a.fusion.capsuleH, shortW, shortH)
	}
}

// Interactive hover bounds track the centered animated capsule rather than the wider transparent window.
func TestFusionCapsuleHitBounds(t *testing.T) {
	full := mygo.Rectangle{X: 300, Y: 0, Width: 680, Height: 76}
	got := fusionCapsuleHitBounds(full, 240, 48)
	if got.X != 520 || got.Width != 240 || got.Y != 0 || got.Height != 48 {
		t.Fatalf("hit bounds = %+v, want X=520 W=240 Y=0 H=48", got)
	}
	if same := fusionCapsuleHitBounds(full, 0, 0); same != full {
		t.Fatalf("zero capsule size changed bounds: %+v", same)
	}
	if clamped := fusionCapsuleHitBounds(full, 900, 120); clamped != full {
		t.Fatalf("oversized capsule escaped bounds: %+v", clamped)
	}
}

// Dragged island positions restore across displays and switch between top-attached and floating shapes by position.
func TestFusionIslandMultiDisplayRestoreAndEdgeDetection(t *testing.T) {
	displays := []mygo.Display{
		{Primary: true, Bounds: mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1440}, WorkArea: mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1392}},
		{Bounds: mygo.Rectangle{X: 2560, Y: -120, Width: 1920, Height: 1080}, WorkArea: mygo.Rectangle{X: 2560, Y: -120, Width: 1920, Height: 1032}},
	}
	if !fusionAtTopEdge(mygo.Rectangle{X: 3100, Y: -116, Width: 680, Height: 60}, displays) {
		t.Fatal("island near secondary display top edge was not detected as top-attached")
	}
	if fusionAtTopEdge(mygo.Rectangle{X: 3100, Y: 220, Width: 680, Height: 60}, displays) {
		t.Fatal("island dragged into middle of secondary display was treated as top-attached")
	}
	if restored, ok := fusionRestoreBounds([2]int{3100, -115}, 680, 60, "capsule", false, displays); !ok || restored.X != 3100 || restored.Y != -120 {
		t.Fatalf("top-edge restore on secondary display = %+v, ok=%v", restored, ok)
	}
	if restored, ok := fusionRestoreBounds([2]int{3100, 320}, 680, 60, "capsule", false, displays); !ok || restored.X != 3100 || restored.Y != 320 {
		t.Fatalf("floating restore on secondary display = %+v, ok=%v", restored, ok)
	}
	if _, ok := fusionRestoreBounds([2]int{6000, 300}, 680, 60, "capsule", false, displays); ok {
		t.Fatal("disconnected display coordinates were restored")
	}
}

// Locked floating lyrics and capsule reveal a clickable unlock button that unlocks on click.
func TestFusionLockedRevealAndUnlock(t *testing.T) {
	for _, mode := range []string{"desktop", "capsule"} {
		a := newTestApp()
		a.settings = defaultSettings()
		a.settings.FusionMode = mode
		a.settings.FusionLocked = true
		a.fusion.lockRevealed = true
		a.fusion.capsuleW, a.fusion.capsuleH = 360, 48
		a.pl.Cue([]spotify.Track{{ID: "lock", Name: "Song"}})
		a.pl.Hold(time.Second)

		w, h := a.fusionWindowSize()
		tester := ui.NewTester(a.fusionOverlay, w, h)
		tester.Frame()
		if !tester.HasText("解锁") {
			t.Fatalf("%s mode did not show unlock button when locked and revealed", mode)
		}
		if err := tester.Click("解锁桌面歌词"); err != nil {
			t.Fatalf("%s mode unlock click failed: %v", mode, err)
		}
		if a.settings.FusionLocked {
			t.Fatalf("%s mode remained locked after clicking unlock", mode)
		}
	}
	deskBounds := mygo.Rectangle{X: 100, Y: 200, Width: 800, Height: 140}
	desk := fusionUnlockRect(deskBounds, "desktop", 0, 0, 1)
	if desk.X <= 100 || desk.X+desk.Width >= 900 || desk.Y != 204 {
		t.Fatalf("unexpected desktop unlock rect: %+v", desk)
	}
	trig := fusionDesktopUnlockTrigger(deskBounds, 1)
	if !fusionPointInRect(mygo.Point{X: 500, Y: 320}, trig) || fusionPointInRect(mygo.Point{X: 150, Y: 320}, trig) || fusionPointInRect(mygo.Point{X: 500, Y: 220}, trig) {
		t.Fatalf("unexpected desktop unlock trigger zone: %+v", trig)
	}
	capRect := fusionUnlockRect(mygo.Rectangle{X: 100, Y: 0, Width: 700, Height: 80}, "capsule", 300, 48, 1)
	if capRect.X < 300 || capRect.X+capRect.Width > 600 || capRect.Y < 2 || capRect.Y+capRect.Height > 48 {
		t.Fatalf("unexpected capsule unlock rect: %+v", capRect)
	}
}
