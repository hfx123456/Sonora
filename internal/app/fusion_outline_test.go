package app

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"vinyl/internal/lyrics"
)

// Inspect the same shaped lyric during a sweep on light and dark backgrounds without a displaced black duplicate.
func TestFusionOutlineFrames(t *testing.T) {
	dir := os.Getenv("NM_OUTLINE_FRAMES")
	if dir == "" {
		t.Skip("set NM_OUTLINE_FRAMES to inspect lyric rendering")
	}
	application := newTestApp()
	application.settings.FusionFont = "Times New Roman"
	application.settings.FusionChineseFont = "Microsoft YaHei UI"
	line := lyrics.Line{Text: "一起长大的约定", At: 0, End: 7 * time.Second}
	line.Words = lyrics.SpreadWords(line.Text, line.At, line.End)
	for _, background := range []struct {
		name  string
		color ui.Color
	}{{"light", ui.Hex("#ededed")}, {"dark", ui.Hex("#20252b")}} {
		position := 2500 * time.Millisecond
		view := func(context *ui.Context) {
			ui.Column(context).Fill().Justify(ui.Center).Padding(20).Background(background.color).Children(func() {
				application.fusionSingleLine(context, line, position, 64, white, white.Alpha(.55), true, true)
			})
		}
		tester := ui.NewTester(view, 1000, 200)
		tester.Frame()
		tester.Frame()
		file, err := os.Create(filepath.Join(dir, "lyric-"+background.name+".png"))
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
