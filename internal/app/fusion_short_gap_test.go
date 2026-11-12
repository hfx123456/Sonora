package app

import (
	"testing"
	"time"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// Short pauses between sung lines must keep the previous lyric instead of flashing song metadata.
func TestFusionShortGapKeepsLyric(t *testing.T) {
	application := newTestApp()
	application.pl.Cue([]spotify.Track{{ID: "short-gap", Name: "Song"}})
	application.fusion.lines = []lyrics.Line{{Text: "上一句", At: time.Second, End: 3 * time.Second}, {Text: "下一句", At: 6 * time.Second, End: 9 * time.Second}}
	application.pl.Hold(5500 * time.Millisecond)
	line, _ := application.fusionCurrent()
	if line.Text != "上一句" {
		t.Fatal("short pause flashed track information")
	}
	application.pl.Hold(6 * time.Second)
	line, _ = application.fusionCurrent()
	if line.Text != "下一句" {
		t.Fatal("next sung line did not replace the previous lyric")
	}
}
