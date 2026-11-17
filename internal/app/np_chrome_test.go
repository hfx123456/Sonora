package app

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// With NM_FRAMES set, saves the stage's matching glass controls over warm artwork in both library appearances and window sizes.
func TestNowPlayingChromeFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{1200, 800}, {880, 580}} {
			a := libraryRefreshApp()
			track := a.pl.Queue()[3]
			a.pl.Cue([]spotify.Track{track})
			var lines []lyrics.Line
			for i, text := range []string{"晚风轻轻吹过窗前", "让时间停在这一瞬间", "在每一个有你的明天"} {
				at := time.Duration(i) * 8 * time.Second
				line := lyrics.Line{At: at, End: at + 8*time.Second, Text: text}
				line.Words = lyrics.SpreadWords(text, line.At, line.End)
				lines = append(lines, line)
			}
			a.pl.SetLyrics(lines)
			a.pl.Hold(12 * time.Second)
			a.nowPlaying, a.npPane = true, paneStage
			tt := ui.NewTester(a.view, size[0], size[1])
			tt.SetDark(dark)
			tt.Move(float32(size[0])/2, float32(size[1])/2)
			tt.Advance(time.Second)
			path := filepath.Join(dir, fmt.Sprintf("stage-chrome-dark-%t-%dx%d.png", dark, size[0], size[1]))
			f, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
