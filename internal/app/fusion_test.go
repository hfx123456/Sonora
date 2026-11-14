package app

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/fusion"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Selecting the new playback-bar icon opens the panel; closing it preserves playback.
func TestFusionPanelControls(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Best Friend", Duration: 319808}})
	a.pl.Hold(2 * time.Second)
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 10 * time.Second, Text: "ありがとう", Translation: "谢谢你"}}
	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("Fusion 双语歌词"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("内部双语歌词面板"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !a.settings.FusionPanel {
		t.Fatal("panel did not open")
	}
	if err := tester.Click("歌词提前半秒"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.settings.FusionOffset != 500 {
		t.Fatal("timing offset did not change")
	}
	if err := tester.Click("关闭双语歌词面板"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.settings.FusionPanel || !a.pl.Playing() {
		t.Fatal("closing lyrics changed playback")
	}
}

// Switching floating modes and closing them preserves the listener's panel choice.
func TestFusionModeMenu(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionPanel = true
	tester := ui.NewTester(a.view, 1180, 780)
	for _, selection := range []string{"透明桌面双行歌词", "顶部胶囊双语歌词", "关闭桌面歌词"} {
		tester.Frame()
		if err := tester.Click("Fusion 双语歌词"); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		if err := tester.ChooseMenuItem(selection); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		want := map[string]string{"透明桌面双行歌词": "desktop", "顶部胶囊双语歌词": "capsule", "关闭桌面歌词": ""}[selection]
		if a.settings.FusionMode != want || !a.settings.FusionPanel {
			t.Fatalf("mode=%q panel=%v", a.settings.FusionMode, a.settings.FusionPanel)
		}
	}
}

// Late responses from an earlier song or reload cannot replace the current translation.
func TestFusionStaleResponse(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "current", Name: "Current"}})
	key := fusionTrackKey(a.pl.Current())
	a.fusion.key, a.fusion.generation = key, 3
	response := fusion.Result{Lines: []lyrics.Line{{Text: "current", Translation: "当前"}}}
	a.receiveFusion(key, 2, response, nil)
	if len(a.fusion.lines) > 0 {
		t.Fatal("accepted stale generation")
	}
	a.receiveFusion("previous", 3, response, nil)
	if len(a.fusion.lines) > 0 {
		t.Fatal("accepted previous song")
	}
	a.receiveFusion(key, 3, response, nil)
	if len(a.fusion.lines) != 1 || a.fusion.lines[0].Translation != "当前" {
		t.Fatal("rejected current translation")
	}
}

// Loading and the intro show notes until the first timed lyric begins.
func TestFusionWaitingNotesTransition(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "waiting", Name: "Song"}})
	a.fusion.loading = true
	line, detail := a.fusionCurrent()
	if line.Text != fusionWaitingNotes || detail != "" {
		t.Fatal("loading did not show waiting notes")
	}
	a.fusion.loading = false
	a.fusion.lines = []lyrics.Line{{At: 5 * time.Second, End: 10 * time.Second, Text: "第一句"}}
	a.pl.Hold(time.Second)
	line, detail = a.fusionCurrent()
	if line.Text != fusionWaitingNotes || detail != "" {
		t.Fatal("intro did not show waiting notes")
	}
	a.pl.Hold(5 * time.Second)
	line, _ = a.fusionCurrent()
	if line.Text != "第一句" {
		t.Fatal("notes did not switch to the first lyric")
	}
}

// A one-line instrumental notice previously appeared as lyrics and then changed to an interlude.
func TestFusionInstrumentalNoticeShowsNotes(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "instrumental", Name: "纯音乐"}})
	for _, text := range []string{"纯音乐，请欣赏", "此歌曲为没有填词的纯音乐，请您欣赏", "此歌曲为DJ舞曲，请您欣赏"} {
		for _, unsynced := range []bool{false, true} {
			a.fusion.lines = []lyrics.Line{{At: time.Second, End: 5 * time.Second, Text: text, Unsynced: unsynced}}
			for _, position := range []time.Duration{0, 2 * time.Second, 30 * time.Second} {
				a.pl.Hold(position)
				line, detail := a.fusionCurrent()
				if line.Text != fusionWaitingNotes || detail != "" {
					t.Fatalf("notice %q at %v: %q, %q", text, position, line.Text, detail)
				}
			}
		}
	}
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 2 * time.Second, Text: "纯音乐，请欣赏"}, {At: 3 * time.Second, End: 8 * time.Second, Text: "真正的歌词"}}
	a.pl.Hold(4 * time.Second)
	if line, _ := a.fusionCurrent(); line.Text != "真正的歌词" {
		t.Fatal("real lyrics were hidden")
	}
}

// Instrumental gaps show the song and artist until the next lyric starts.
func TestFusionInstrumentalShowsTrack(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "song", Name: "歌名", Artists: []spotify.Artist{{Name: "歌手"}}}})
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 3 * time.Second, Text: "第一句"}, {At: 10 * time.Second, End: 15 * time.Second, Text: "下一句"}}
	a.pl.Hold(6 * time.Second)
	line, detail := a.fusionCurrent()
	if line.Text != "♪  歌名" || detail != "歌手" {
		t.Fatalf("instrumental caption = %q, %q", line.Text, detail)
	}
	a.pl.Hold(10 * time.Second)
	line, _ = a.fusionCurrent()
	if line.Text != "下一句" {
		t.Fatal("instrumental caption did not return to lyrics")
	}
}

// The second desktop line is a translation, never the next original lyric.
func TestFusionOriginalAndTranslation(t *testing.T) {
	a := newTestApp()
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Song"}})
	a.pl.Hold(2 * time.Second)
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 4 * time.Second, Text: "ありがとう", Translation: "谢谢你"}, {At: 4 * time.Second, End: 8 * time.Second, Text: "いつも"}}
	line, translation := a.fusionCurrent()
	if line.Text != "ありがとう" || translation != "谢谢你" {
		t.Fatal("wrong bilingual line")
	}
	a.fusion.lines[0].Translation = ""
	_, translation = a.fusionCurrent()
	if translation != "" {
		t.Fatalf("substituted another line: %q", translation)
	}
	a.settings.FusionOffset = 3000
	line, _ = a.fusionCurrent()
	if line.Text != "いつも" {
		t.Fatal("offset did not affect current line")
	}
}

// Chinese songs have no translation placeholder, duplicate translation, or unused second row.
func TestFusionChineseHasNoTranslationRow(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionMode = "capsule"
	a.settings.FusionPanel = true
	a.pl.Cue([]spotify.Track{{ID: "chinese", Name: "表白", Artists: []spotify.Artist{{Name: "萧亚轩"}}}})
	a.pl.Hold(2 * time.Second)
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 8 * time.Second, Text: "晚风轻轻吹过窗前"}, {At: 8 * time.Second, End: 12 * time.Second, Text: "灯光落在旧相片", Translation: "灯光落在旧相片"}}
	_, translation := a.fusionCurrent()
	if translation != "" {
		t.Fatalf("Chinese translation row: %q", translation)
	}
	_, height := a.fusionWindowSize()
	if height != 50 {
		t.Fatalf("Chinese lyrics left a second row: height=%d", height)
	}
	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if tester.HasText("暂无中文翻译") {
		t.Fatal("Chinese song still asks for translation")
	}
	a.pl.Hold(9 * time.Second)
	_, translation = a.fusionCurrent()
	if translation != "" {
		t.Fatal("duplicated original as translation")
	}
}

// Chinese credits do not hide English translations, and kanji-only Japanese lines keep theirs.
func TestFusionLanguageContext(t *testing.T) {
	english := []lyrics.Line{{Text: "作词: Someone"}, {Text: "A new morning fills the sky", Translation: "清晨的天空"}}
	if fusionChinese(english) || visibleFusionTranslation(english[1], fusionChinese(english), false) == "" {
		t.Fatal("credits changed the song's language")
	}
	japanese := []lyrics.Line{{Text: "空", Translation: "天空"}, {Text: "ありがとう"}}
	if fusionChinese(japanese) || visibleFusionTranslation(japanese[0], fusionChinese(japanese), false) != "天空" {
		t.Fatal("kanji-only lyric lost its translation")
	}
}

// Translation visibility, shape, size, and click-through choices update independently of playback.
func TestFusionAppearanceMenu(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Song"}})
	a.pl.Hold(2 * time.Second)
	a.fusion.lines = []lyrics.Line{{At: time.Second, End: 10 * time.Second, Text: "ありがとう", Translation: "谢谢你"}}
	tester := ui.NewTester(a.view, 1180, 780)
	for _, choice := range []string{"显示中文译文", "词岛悬浮胶囊形状", "歌词字号放大", "锁定桌面歌词（点击穿透）"} {
		tester.Frame()
		if err := tester.Click("Fusion 双语歌词"); err != nil {
			t.Fatal(err)
		}
		if err := tester.ChooseMenuItem(choice); err != nil {
			t.Fatal(err)
		}
	}
	_, translation := a.fusionCurrent()
	if translation != "" || !a.settings.FusionHideTranslation || !a.settings.FusionFloatingIsland || !a.settings.FusionLocked || a.settings.FusionScale != 110 || !a.pl.Playing() {
		t.Fatal("appearance controls changed the wrong state")
	}
	a.toggleFusionLock()
	if a.settings.FusionLocked {
		t.Fatal("could not unlock lyrics")
	}
}

// The adapted Lyricify background compiles and renders a cached frame from album artwork.
func TestFusionBackgroundRenders(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Song", Album: spotify.Album{PicURL: "fixture"}}})
	image := image.NewRGBA(image.Rect(0, 0, 24, 24))
	for row := range 24 {
		for column := range 24 {
			image.SetRGBA(column, row, color.RGBA{uint8(column * 10), uint8(row * 10), 90, 255})
		}
	}
	art := &artwork{bmp: ui.NewBitmap(image), backdrop: ui.NewBitmap(blurImage(image)), tint: ui.RGB(180, 90, 120), used: a.art.now()}
	a.art.art[spotify.Sized("fixture", 600)+"#backdrop"] = art
	a.art.art[spotify.Sized("fixture", 160)] = art
	tester := ui.NewTester(a.fusionPanel, 420, 620)
	tester.Frame()
	if a.fusion.background.canvas == nil {
		t.Fatal("background did not render")
	}
	first := a.fusion.background.canvas
	tester.Frame()
	if a.fusion.background.canvas != first {
		t.Fatal("idle background rebuilt its canvas")
	}
}

// Clicking a timed lyric seeks to its original song time after the user's timing offset.
func TestFusionClickSeeks(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionPanel = true
	a.settings.FusionOffset = 500
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Song", Duration: 200000}})
	a.pl.Hold(time.Second)
	a.fusion.lines = []lyrics.Line{{At: 10 * time.Second, End: 20 * time.Second, Text: "ありがとう", Translation: "谢谢你"}}
	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("跳转歌词：ありがとう"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.pl.SongPosition() != 9500*time.Millisecond {
		t.Fatalf("seek=%v", a.pl.SongPosition())
	}
}

// Closing the floating lyrics hides that mode without pausing the song or closing the internal panel.
func TestFusionFloatingClose(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionMode = "capsule"
	a.settings.FusionPanel = true
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Song", Duration: 200000}})
	a.pl.Hold(time.Second)
	tester := ui.NewTester(a.fusionOverlay, 740, 92)
	tester.Frame()
	tester.Move(300, 40)
	tester.Frame()
	if err := tester.Click("关闭桌面歌词"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.settings.FusionMode != "" || !a.settings.FusionPanel || !a.pl.Playing() {
		t.Fatal("closing floating lyrics changed playback or panel state")
	}
}

// Render the two floating modes and the panel for visual inspection without opening windows.
func TestFusionFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip("set NM_FRAMES to inspect lyrics")
	}
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Cue([]spotify.Track{{ID: "1", Name: "Best Friend", Artists: []spotify.Artist{{Name: "西野カナ"}}, Duration: 319808}})
	a.pl.Hold(2 * time.Second)
	a.fusion.lines = []lyrics.Line{{At: 890 * time.Millisecond, End: 9260 * time.Millisecond, Text: "ありがとう 君がいてくれて本当よかったよ", Translation: "谢谢 有你在身边真好"}, {At: 9260 * time.Millisecond, End: 13320 * time.Millisecond, Text: "どんな時だっていつも", Translation: "不论什么时候"}, {At: 13320 * time.Millisecond, End: 17320 * time.Millisecond, Text: "笑っていられる", Translation: "总会对我微笑"}}
	a.fusion.source = "网易云 · Lyricify"
	if path := os.Getenv("NM_FUSION_COVER"); path != "" {
		fusionPreviewArt(t, a, path)
	}
	for _, mode := range []string{"desktop", "capsule", "panel", "narrow"} {
		a.settings.FusionMode = mode
		width, height := a.fusionWindowSize()
		view := a.fusionOverlay
		if mode == "capsule" {
			width, height = a.fusionWindowSize()
		}
		if mode == "capsule" || mode == "desktop" {
			view = func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#ededed")).Children(func() { a.fusionOverlay(c) })
			}
		}
		if mode == "panel" || mode == "narrow" {
			width, height = 1180, 780
			view = a.view
			a.settings.FusionPanel = true
		}
		if mode == "narrow" {
			width = 880
		}
		tester := ui.NewTester(view, width, height)
		tester.Frame()
		tester.Frame()
		if mode == "panel" || mode == "narrow" {
			tester.Advance(time.Second)
			tester.Frame()
		}
		file, err := os.Create(filepath.Join(dir, "fusion-"+mode+".png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, tester.Image())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if path := os.Getenv("NM_FUSION_CHINESE_CACHE"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var cached struct {
			Source string
			Lines  []struct {
				At, End           int64
				Text, Translation string
			}
		}
		if err = json.Unmarshal(data, &cached); err != nil {
			t.Fatal(err)
		}
		a.pl.Cue([]spotify.Track{{ID: "chinese", Name: "表白", Artists: []spotify.Artist{{Name: "萧亚轩"}}, Duration: 209467}})
		a.pl.Hold(40 * time.Second)
		a.fusion.lines = nil
		for _, line := range cached.Lines {
			a.fusion.lines = append(a.fusion.lines, lyrics.Line{At: time.Duration(line.At) * time.Millisecond, End: time.Duration(line.End) * time.Millisecond, Text: line.Text, Translation: line.Translation})
		}
		a.fusion.source = cached.Source
		a.fusion.scroll, a.fusion.at = ui.ScrollState{}, -2
		if cover := os.Getenv("NM_FUSION_CHINESE_COVER"); cover != "" {
			fusionPreviewArt(t, a, cover)
		}
		for _, mode := range []string{"capsule", "desktop", "panel"} {
			a.settings.FusionMode = mode
			width, height := a.fusionWindowSize()
			view := func(c *ui.Context) {
				ui.Box(c).Fill().Background(ui.Hex("#ededed")).Children(func() { a.fusionOverlay(c) })
			}
			if mode == "panel" {
				width, height = 1180, 780
				view = a.view
			}
			tester := ui.NewTester(view, width, height)
			tester.Frame()
			tester.Frame()
			if mode == "panel" {
				tester.Advance(time.Second)
				tester.Frame()
			}
			file, err := os.Create(filepath.Join(dir, "fusion-chinese-"+mode+".png"))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(file, tester.Image())
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func fusionPreviewArt(t *testing.T, a *app, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bitmap, image, err := decodeArt(data, 600)
	if err != nil {
		t.Fatal(err)
	}
	art := &artwork{bmp: bitmap, backdrop: ui.NewBitmap(blurImage(image)), tint: dominantColor(image), used: a.art.now()}
	a.pl.Current().Album.PicURL = "preview"
	for _, size := range []int{96, 120, 160, 600} {
		a.art.art[spotify.Sized("preview", size)] = art
	}
	a.art.art[spotify.Sized("preview", 600)+"#backdrop"] = art
}

// Pausing hid nothing before, leaving a frozen line over the desktop; the
// floating lyrics now hide on pause and return on play, while the internal
// panel and a disabled floating mode are left alone.
func TestFusionPauseHidesFloating(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.FusionMode = "capsule"
	a.pl.Changed = func() { a.syncFusionVisibility() }
	a.pl.Cue([]spotify.Track{{ID: "pause", Name: "歌", Duration: 200000}})
	out := audio.NewPlayer(&silence{})
	out.SetVolume(0)
	out.Play()
	a.pl.Attach(out)
	defer out.Close()

	a.syncFusionVisibility()
	if a.fusion.pauseHidden {
		t.Fatal("playing floating lyrics were hidden")
	}
	a.pl.TogglePlay()
	if !a.fusion.pauseHidden {
		t.Fatal("pause did not hide the floating lyrics")
	}
	a.pl.TogglePlay()
	if a.fusion.pauseHidden {
		t.Fatal("play did not bring the floating lyrics back")
	}
	a.settings.FusionMode = ""
	a.pl.TogglePlay()
	if a.fusion.pauseHidden {
		t.Fatal("a disabled floating mode was still hidden on pause")
	}
}

func TestFusionPausedManualShowStaysVisibleUntilPlayed(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.pl.Changed = func() { a.syncFusionVisibility() }
	a.pl.Cue([]spotify.Track{{ID: "pause", Name: "歌", Duration: 200000}})
	out := audio.NewPlayer(&silence{})
	out.SetVolume(0)
	out.Play()
	a.pl.Attach(out)
	defer out.Close()

	// Pause first so playback is genuinely paused.
	a.pl.TogglePlay()
	if a.pl.Playing() {
		t.Fatal("expected player to be paused")
	}

	// Enabling desktop/capsule lyrics while paused keeps it visible immediately.
	a.settings.FusionMode = "capsule"
	a.fusion.manualShow = true
	a.fusion.pauseHidden = false
	a.syncFusionVisibility()
	if a.fusion.pauseHidden {
		t.Fatal("floating lyrics hid immediately when enabled while paused")
	}

	// Playing clears manualShow; a subsequent pause should hide it again.
	a.pl.TogglePlay()
	if !a.pl.Playing() || a.fusion.manualShow || a.fusion.pauseHidden {
		t.Fatalf("after play: playing=%v manualShow=%v pauseHidden=%v", a.pl.Playing(), a.fusion.manualShow, a.fusion.pauseHidden)
	}
	a.pl.TogglePlay()
	if !a.fusion.pauseHidden {
		t.Fatal("subsequent pause did not hide floating lyrics")
	}
}
