package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/fusion"
	"vinyl/internal/localmusic"
	"vinyl/internal/lyrics"
	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

type fusionState struct {
	win             *mygo.Window
	mode            string
	key             string
	generation      int
	cancel          context.CancelFunc
	lines           []lyrics.Line
	source, message string
	loading         bool
	scroll          ui.ScrollState
	at              int
	background      fusionBackground
	caption         fusionCaptionMotion
	hover           fusionHover
	hoverGeneration int
	placing         bool
	captureOnly     bool
	pauseHidden     bool
	manualShow      bool
	capsuleW        float32
	capsuleH        float32
	measure         fusionLineMeasure
}

var fetchFusion = fusion.Fetch

const fusionWaitingNotes = "♪ ♫ ♪"

func isInstrumentalText(s string) bool {
	text := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			return -1
		}
		return r
	}, s)
	switch text {
	case "纯音乐请欣赏", "此歌曲为没有填词的纯音乐请您欣赏", "此歌曲为DJ舞曲请您欣赏", "此歌曲为没有填词的DJ舞曲请您欣赏":
		return true
	}
	return strings.Contains(text, "纯音乐请欣赏") || strings.Contains(text, "没有填词的纯音乐") || strings.Contains(text, "DJ舞曲请您欣赏")
}

// Only known instrumental notices are placeholders, not lyrics.
func fusionInstrumentalNotice(lines []lyrics.Line) bool {
	if len(lines) == 0 {
		return false
	}
	nonCredit := 0
	for _, l := range lines {
		if lyrics.IsCredit(l.Text) {
			continue
		}
		nonCredit++
		if !isInstrumentalText(l.Text) {
			return false
		}
	}
	return nonCredit > 0
}

func fusionTrackKey(track *spotify.Track) string {
	if track == nil {
		return ""
	}
	return track.URI() + "\n" + track.LocalPath + "\n" + track.Name + "\n" + track.ArtistNames()
}

func (a *app) ensureFusion(refresh bool) {
	if a.win == nil || a.quitting {
		return
	}
	state := &a.fusion
	if !a.settings.FusionPanel && a.settings.FusionMode == "" {
		if state.cancel != nil {
			state.cancel()
			state.cancel = nil
			state.generation++
			if state.loading {
				state.key = ""
				state.loading = false
			}
		}
		return
	}
	track := a.pl.Current()
	key := fusionTrackKey(track)
	if !refresh && key == state.key {
		return
	}
	if state.cancel != nil {
		state.cancel()
	}
	state.generation++
	state.key, state.lines, state.source, state.message = key, nil, "", ""
	state.scroll, state.at = ui.ScrollState{}, -2
	state.loading = track != nil
	if track == nil {
		return
	}
	if track.Episode {
		state.loading = false
		state.message = "播客请使用播放器原有字幕"
		return
	}
	copyTrack := *track
	dir, _ := mygo.App.Path(mygo.PathCache)
	proxy := ""
	request, _ := http.NewRequest("GET", "https://music.163.com", nil)
	if address, err := spotify.Proxy(request); err == nil && address != nil {
		proxy = address.String()
	}
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	generation := state.generation
	go func() {
		defer cancel()
		if !refresh && !copyTrack.Local() {
			cached := lyrics.LoadCached(player.LyricsCacheDir, copyTrack.URI())
			if len(cached.Lines) > 0 {
				a.post(func() {
					a.receiveFusion(key, generation, fusion.Result{Lines: cached.Lines, Source: cached.Source}, nil)
				})
				return
			}
		}
		var artists []string
		for _, artist := range copyTrack.Artists {
			artists = append(artists, artist.Name)
		}
		input := fusion.Request{Title: copyTrack.Name, Artists: artists, Album: copyTrack.Album.Name, DurationMs: copyTrack.Duration, Proxy: proxy}
		if copyTrack.Local() {
			input.Raw = localmusic.Lyrics(copyTrack)
		}
		result, err := fetchFusion(ctx, filepath.Join(dir, "fusion"), input, refresh)
		a.post(func() { a.receiveFusion(key, generation, result, err) })
	}()
}

func (a *app) receiveFusion(key string, generation int, result fusion.Result, err error) {
	state := &a.fusion
	if generation != state.generation || key != state.key || key != fusionTrackKey(a.pl.Current()) {
		return
	}
	state.loading, state.cancel = false, nil
	if len(result.Lines) == 0 {
		if lines, _ := a.pl.Lyrics(); len(lines) > 0 {
			result.Lines, result.Source, err = append([]lyrics.Line(nil), lines...), "播放器歌词", nil
		} else if len(state.lines) > 0 {
			return
		}
	}
	if len(result.Lines) > 0 && !a.pl.Current().Local() {
		lyrics.SaveCached(player.LyricsCacheDir, a.pl.Current().URI(), result.Source, result.Lines)
	}
	state.lines, state.source = prepareFusionLines(result.Lines), result.Source
	state.message = ""
	if err != nil {
		state.message = err.Error()
	}
	a.resizeFusion()
}

func (a *app) fusionPosition() time.Duration {
	return a.pl.SongPosition() + time.Duration(a.settings.FusionOffset)*time.Millisecond
}

func (a *app) fusionCurrent() (lyrics.Line, string) {
	if a.pl.Current() == nil {
		return lyrics.Line{Text: appName}, "等待播放"
	}
	if a.fusion.loading {
		return lyrics.Line{Text: fusionWaitingNotes}, ""
	}
	if a.fusion.message != "" {
		return lyrics.Line{Text: a.pl.Current().Name}, a.fusion.message
	}
	if fusionInstrumentalNotice(a.fusion.lines) {
		return lyrics.Line{Text: fusionWaitingNotes}, ""
	}
	index := lyrics.Index(a.fusion.lines, a.fusionPosition())
	if index < 0 {
		if len(a.fusion.lines) > 0 && a.fusion.lines[0].Unsynced {
			return lyrics.Line{Text: a.pl.Current().Name}, "无时间轴歌词，请打开内部面板查看"
		}
		return lyrics.Line{Text: fusionWaitingNotes}, ""
	}
	line := a.fusion.lines[index]
	if isInstrumentalText(line.Text) {
		return lyrics.Line{Text: fusionWaitingNotes}, ""
	}
	longGap := index+1 >= len(a.fusion.lines) || a.fusion.lines[index+1].At-line.End >= 6*time.Second
	if longGap && a.fusionPosition() >= line.End+2*time.Second {
		track := a.pl.Current()
		return lyrics.Line{Text: "♪  " + track.Name}, track.ArtistNames()
	}
	return line, visibleFusionTranslation(line, fusionChinese(a.fusion.lines), a.settings.FusionHideTranslation)
}

func fusionChinese(lines []lyrics.Line) bool {
	han, other := 0, 0
	for _, line := range lines {
		if lyrics.IsCredit(line.Text) {
			continue
		}
		for _, character := range line.Text {
			if unicode.In(character, unicode.Hiragana, unicode.Katakana, unicode.Hangul) {
				return false
			}
			if unicode.Is(unicode.Han, character) {
				han++
			} else if unicode.IsLetter(character) {
				other++
			}
		}
	}
	return han > 0 && han*2 > other
}

func visibleFusionTranslation(line lyrics.Line, chinese, hidden bool) string {
	translation := strings.TrimSpace(line.Translation)
	if hidden || chinese || lyrics.IsCredit(line.Text) || translation == "" {
		return ""
	}
	if strings.EqualFold(strings.Join(strings.Fields(line.Text), ""), strings.Join(strings.Fields(translation), "")) {
		return ""
	}
	return translation
}

func (a *app) fusionScale() float32 {
	scale := a.settings.FusionScale
	if scale == 0 {
		scale = 100
	}
	return float32(max(75, min(150, scale))) / 100
}

func (a *app) changeFusionScale(delta int) {
	a.settings.FusionScale = max(75, min(150, int(a.fusionScale()*100)+delta))
	a.settings.save()
	a.resizeFusion()
	a.redraw()
}

func (a *app) fusionIslandWidth() int {
	if a.settings.FusionIslandWidth == 0 {
		return 680
	}
	return max(300, min(960, a.settings.FusionIslandWidth))
}

func (a *app) changeFusionIslandWidth(delta int) {
	a.settings.FusionIslandWidth = max(300, min(960, a.fusionIslandWidth()+delta))
	a.settings.save()
	a.resizeFusion()
	a.redraw()
}

func (a *app) toggleFusionLock() {
	a.settings.FusionLocked = !a.settings.FusionLocked
	a.settings.save()
	if a.fusion.win != nil {
		a.updateFusionHover()
	}
	a.redraw()
}

// syncFusionVisibility hides the floating lyrics while playback is paused
// and brings them back when it plays, so a pause clears the desktop instead
// of leaving a frozen line over it. The internal panel is unaffected.
func (a *app) syncFusionVisibility() {
	if a.settings.FusionMode == "" || a.quitting || a.fusion.captureOnly {
		return
	}
	playing := a.pl.Playing()
	if playing {
		a.fusion.manualShow = false
	} else if a.fusion.manualShow {
		if a.fusion.pauseHidden {
			a.fusion.pauseHidden = false
			if a.fusion.win != nil {
				a.fusion.win.ShowInactive()
			}
			if a.settings.FusionMode == "capsule" {
				a.startFusionHover()
			}
		}
		return
	}
	if playing == !a.fusion.pauseHidden {
		return
	}
	a.fusion.pauseHidden = !playing
	if !playing {
		a.fusion.hoverGeneration++
	}
	if a.fusion.win == nil {
		return
	}
	if playing {
		a.fusion.win.ShowInactive()
		if a.settings.FusionMode == "capsule" {
			a.startFusionHover()
		}
	} else {
		a.fusion.win.Hide()
	}
}

func (a *app) fusionTick(c *ui.Context, floating bool) {
	if a.fusion.key == fusionTrackKey(a.pl.Current()) && len(a.fusion.lines) == 0 {
		if lines, _ := a.pl.Lyrics(); len(lines) > 0 {
			a.fusion.lines = prepareFusionLines(append([]lyrics.Line(nil), lines...))
			a.fusion.loading, a.fusion.message, a.fusion.source = false, "", "播放器歌词"
		}
	}
	still := a.settings.FusionStatic || c.Preferences().ReduceMotion
	var delay time.Duration
	if floating {
		delay = fusionFloatingDelay(a.fusion.lines, a.fusionPosition(), a.pl.Playing(), still)
	} else {
		delay = fusionFrameDelay(a.fusion.lines, a.fusionPosition(), a.pl.Playing(), still)
	}
	if delay > 0 {
		c.After(delay)
	}
}

func (a *app) toggleFusionPanel() {
	a.settings.FusionPanel = !a.settings.FusionPanel
	a.settings.save()
	a.ensureFusion(false)
	a.redraw()
}

func (a *app) nudgeFusion(delta int64) {
	a.settings.FusionOffset = max(-30000, min(30000, a.settings.FusionOffset+delta))
	a.settings.save()
	a.resizeFusion()
	a.redraw()
}

func (a *app) importFusion() {
	track := a.pl.Current()
	if track == nil {
		return
	}
	key := fusionTrackKey(track)
	window := a.win
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: window, Title: "载入双语歌词", Filters: []mygo.FileFilter{{Name: "歌词文件", Extensions: []string{"lrc", "ttml", "yrc", "qrc", "lys", "lyl"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		info, err := os.Stat(paths[0])
		if err != nil || info.Size() > 2<<20 {
			a.post(func() { a.toast = "歌词文件无法读取或大于 2 MB" })
			return
		}
		data, err := os.ReadFile(paths[0])
		if err != nil {
			a.post(func() { a.toast = "无法读取歌词文件" })
			return
		}
		a.post(func() {
			if key != fusionTrackKey(a.pl.Current()) {
				return
			}
			if a.fusion.cancel != nil {
				a.fusion.cancel()
			}
			a.fusion.generation++
			generation := a.fusion.generation
			a.fusion.key, a.fusion.loading, a.fusion.message = key, true, ""
			ctx, cancel := context.WithCancel(context.Background())
			a.fusion.cancel = cancel
			dir, _ := mygo.App.Path(mygo.PathCache)
			go func() {
				defer cancel()
				result, err := fetchFusion(ctx, filepath.Join(dir, "fusion"), fusion.Request{Raw: strings.TrimPrefix(string(data), "\ufeff"), ParseOnly: true}, false)
				a.post(func() { a.receiveFusion(key, generation, result, err) })
			}()
		})
	}()
}
