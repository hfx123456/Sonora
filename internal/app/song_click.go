package app

import "github.com/egoist/mygo/ui"

// rowDoubleClicked starts playback on a double click, or toggles play/pause on a
// single click of the current song.
func (a *app) rowDoubleClicked(c *ui.Context, row ui.Element, current bool) bool {
	if row.DoubleClicked() {
		// DoubleClicked arrives on mouse-down. Ignore only the release of
		// this press, not every single click for the next half second.
		a.songDoubleClick = row.ID()
		if !row.Pressed() {
			a.songDoubleClick = 0
		}
		row.Clicked()
		return true
	}
	if a.songDoubleClick == row.ID() {
		if !row.Pressed() {
			a.songDoubleClick = 0
		}
		row.Clicked()
		return false
	}
	if current && row.Clicked() {
		a.pl.TogglePlay()
	}
	return false
}

// finishSongClick is kept for backward compatibility with view callers.
func (a *app) finishSongClick(c *ui.Context) {}
