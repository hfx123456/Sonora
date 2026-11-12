package app

import (
	"errors"
	"testing"
	"time"

	"vinyl/internal/fusion"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// A failed external search must use the current player's lyrics rather than report no lyrics on the island.
func TestFusionFallsBackToPlayerLyrics(t *testing.T) {
	application := newTestApp()
	application.pl.Cue([]spotify.Track{{ID: "fallback", Name: "7-11 的爱恋"}})
	application.pl.SetLyrics([]lyrics.Line{{Text: "一起长大的约定", At: time.Second, End: 5 * time.Second}})
	key := fusionTrackKey(application.pl.Current())
	application.fusion.key, application.fusion.generation = key, 1
	application.receiveFusion(key, 1, fusion.Result{}, errors.New("not found"))
	if len(application.fusion.lines) != 1 || application.fusion.message != "" || application.fusion.loading {
		t.Fatal("player lyrics were not used as a fallback")
	}
}
