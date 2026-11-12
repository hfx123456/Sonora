package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	gifPalette "image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

func (a *app) fusionMotionScene(dir string) {
	result := map[string]any{}
	defer func() {
		data, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(filepath.Join(dir, "native-motion-result.json"), data, 0o600)
		mygo.RunOnMain(mygo.App.Quit)
	}()
	tracks, warnings := localmusic.Scan([]string{os.Getenv("NM_MOTION_AUDIO")}, filepath.Join(dir, "covers"))
	if len(tracks) != 1 || !tracks[0].Playable() {
		result["error"] = "test audio unavailable"
		result["warnings"] = warnings
		return
	}
	data, err := os.ReadFile(os.Getenv("NM_MOTION_LYRICS"))
	if err != nil {
		result["error"] = err.Error()
		return
	}
	var cached struct {
		Source string
		Lines  []struct {
			At, End           int64
			Text, Translation string
			Words             []struct {
				Text       string
				Start, End int64
			}
		}
	}
	if err = json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &cached); err != nil {
		result["error"] = err.Error()
		return
	}
	var lines []lyrics.Line
	wordLines := 0
	for _, raw := range cached.Lines {
		line := lyrics.Line{At: time.Duration(raw.At) * time.Millisecond, End: time.Duration(raw.End) * time.Millisecond, Text: raw.Text, Translation: raw.Translation, Timed: len(raw.Words) > 0}
		for _, word := range raw.Words {
			line.Words = append(line.Words, lyrics.Word{Text: word.Text, Start: time.Duration(word.Start) * time.Millisecond, End: time.Duration(word.End) * time.Millisecond})
		}
		if line.Timed {
			wordLines++
		}
		lines = append(lines, line)
	}
	result["title"], result["realWordTimedLines"] = tracks[0].Name, wordLines
	lines = prepareFusionLines(lines)
	translatedLines := 0
	for _, line := range lines {
		if len(fusionTranslationLine(line, line.Translation).Words) > 0 {
			translatedLines++
		}
	}
	result["animatedTranslationLines"] = translatedLines
	var point mygo.Point
	mygo.RunOnMain(func() {
		point = mygo.Point{X: -100000, Y: -100000}
		fusionCursor = func() mygo.Point { return point }
		a.settings.FusionStatic, a.settings.FusionNoAvoid, a.settings.FusionFloatingIsland, a.settings.FusionLocked = false, false, false, false
		a.settings.FusionScale = 100
		a.fusion.captureOnly = os.Getenv("NM_MOTION_VISIBLE") != "1"
		a.pl.SetVolume(0)
		url := tracks[0].Album.PicURL
		if cover := os.Getenv("NM_MOTION_COVER"); cover != "" {
			url = spotify.FileURL(cover)
		}
		if url != "" {
			if bmp, img, loadErr := loadArt("", url); loadErr == nil {
				tracks[0].Album.PicURL = url
				art := &artwork{bmp: bmp, backdrop: ui.NewBitmap(blurImage(img)), tint: dominantColor(img), used: a.art.now()}
				for _, size := range []int{120, 160, 600} {
					a.art.art[spotify.Sized(url, size)] = art
				}
				a.art.art[spotify.Sized(url, 600)+"#backdrop"] = art
			}
		}
		a.pl.PlayQueue(tracks, 0)
		a.fusion.key = fusionTrackKey(a.pl.Current())
		a.fusion.lines = prepareFusionLines(lines)
		a.fusion.source = cached.Source
		a.settings.FusionPanel = true
		a.setFusionMode("capsule")
		a.redraw()
	})
	var position time.Duration
	for range 120 {
		time.Sleep(100 * time.Millisecond)
		mygo.RunOnMain(func() { position = a.pl.Position() })
		if position > 500*time.Millisecond {
			break
		}
	}
	result["audioStarted"] = position > 500*time.Millisecond
	if position <= 500*time.Millisecond {
		mygo.RunOnMain(func() { result["error"] = a.pl.Err() })
		return
	}
	var target time.Duration
	for _, line := range lines {
		if len(line.Words) >= 4 && line.End-line.At > 1800*time.Millisecond && line.End-line.At < 8*time.Second && (translatedLines == 0 || line.Translation != "") {
			target = line.At + 200*time.Millisecond
			break
		}
	}
	mygo.RunOnMain(func() { a.pl.Seek(target) })
	time.Sleep(400 * time.Millisecond)
	var frames []image.Image
	var positions []int64
	for range 55 {
		mygo.RunOnMain(func() { positions = append(positions, a.pl.SongPosition().Milliseconds()); a.fusion.win.Invalidate() })
		encoded, captureErr := a.fusion.win.CapturePage()
		if captureErr != nil {
			result["captureError"] = captureErr.Error()
			break
		}
		frame, decodeErr := png.Decode(bytes.NewReader(encoded))
		if decodeErr != nil {
			result["captureError"] = decodeErr.Error()
			break
		}
		frames = append(frames, frame)
		if len(frames) == 1 || len(frames) == 25 || len(frames) == 49 {
			if file, saveErr := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%d.png", len(frames)-1))); saveErr == nil {
				png.Encode(file, frame)
				file.Close()
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	if len(frames) > 0 {
		result["gifFrames"] = len(frames)
		if err = writeFusionGIF(filepath.Join(dir, "native-lyrics-motion.gif"), frames, 7); err != nil {
			result["gifError"] = err.Error()
		}
	}
	result["playbackPositionsMs"] = positions
	mygo.RunOnMain(func() {
		bounds := a.fusion.win.Bounds()
		a.fusion.win.SetPosition(bounds.X, bounds.Y+96)
	})
	time.Sleep(180 * time.Millisecond)
	mygo.RunOnMain(func() {
		bounds := a.fusion.win.Bounds()
		result["windowY"], result["physicalScreenTop"] = bounds.Y, mygo.Screen.PrimaryDisplay().Bounds.Y
		result["windowWidth"], result["windowHeight"] = bounds.Width, bounds.Height
		point = mygo.Point{X: bounds.X + bounds.Width/2, Y: bounds.Y + 20}
	})
	time.Sleep(700 * time.Millisecond)
	mygo.RunOnMain(func() { result["hoverOpacity"] = a.fusion.win.Opacity() })
	time.Sleep(160 * time.Millisecond)
	mygo.RunOnMain(func() {
		result["hoverStableOpacity"] = a.fusion.win.Opacity()
		point = mygo.Point{X: -100000, Y: -100000}
	})
	time.Sleep(850 * time.Millisecond)
	mygo.RunOnMain(func() {
		result["restoredOpacity"] = a.fusion.win.Opacity()
		result["playing"] = a.pl.Playing()
		a.pl.TogglePlay()
	})
	time.Sleep(600 * time.Millisecond)
	var paused time.Duration
	mygo.RunOnMain(func() { paused = a.pl.Position() })
	time.Sleep(300 * time.Millisecond)
	mygo.RunOnMain(func() { result["pausedPositionStable"] = a.pl.Position()-paused < 100*time.Millisecond })
}

func writeFusionGIF(path string, frames []image.Image, delay int) error {
	animation := &gif.GIF{LoopCount: 0}
	for _, frame := range frames {
		bounds := frame.Bounds()
		background := image.NewRGBA(bounds)
		draw.Draw(background, bounds, image.NewUniform(color.RGBA{237, 237, 237, 255}), image.Point{}, draw.Src)
		draw.Draw(background, bounds, frame, bounds.Min, draw.Over)
		paletted := image.NewPaletted(bounds, gifPalette.Plan9)
		draw.FloydSteinberg.Draw(paletted, bounds, background, bounds.Min)
		animation.Image = append(animation.Image, paletted)
		animation.Delay = append(animation.Delay, delay)
		animation.Disposal = append(animation.Disposal, gif.DisposalBackground)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return gif.EncodeAll(file, animation)
}
