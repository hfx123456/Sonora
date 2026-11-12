package app

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// rowDoubleClicked starts playback on a double click, or pauses on a
// single click of the current song.
func (a *app) rowDoubleClicked(c *ui.Context, row ui.Element, current bool) bool {
	if row.DoubleClicked() {
		a.songDoubleClickedUntil = c.Now().Add(500 * time.Millisecond)
		a.songDoubleClickedWall = time.Now().Add(500 * time.Millisecond)
		row.Clicked()
		return true
	}
	if (!a.songDoubleClickedUntil.IsZero() && !c.Now().After(a.songDoubleClickedUntil)) ||
		(!a.songDoubleClickedWall.IsZero() && time.Now().Before(a.songDoubleClickedWall)) {
		row.Clicked()
		return false
	}
	if current && row.Clicked() && a.pl.Playing() {
		a.pl.TogglePlay()
	}
	return false
}

// finishSongClick is kept for backward compatibility with view callers.
func (a *app) finishSongClick(c *ui.Context) {}
