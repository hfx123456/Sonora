package app

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

// The session is kept a while after playback changes and every while as
// it plays, and paused nothing more is due. It was kept by a ticker that
// woke the app, and redrew its window, every half minute even at rest.
func TestSessionRestsWhilePaused(t *testing.T) {
	var due []func()
	defer func(after func(time.Duration, func())) { sessionAfter = after }(sessionAfter)
	sessionAfter = func(_ time.Duration, fn func()) { due = append(due, fn) }

	a := newTestApp()
	a.pl.Changed = a.keepSession
	a.pl.Cue([]spotify.Track{{ID: "7", Name: "宝船", Duration: 200000}})
	out := audio.NewPlayer(&silence{})
	out.SetVolume(0)
	out.Play()
	a.pl.Attach(out)
	defer out.Close()

	a.pl.TogglePlay()
	a.pl.TogglePlay()
	if len(due) != 1 {
		t.Fatalf("%d saves due after pausing and playing", len(due))
	}
	due[0]()
	if len(due) != 2 {
		t.Fatalf("playing, %d saves due after one, want another", len(due)-1)
	}

	a.pl.TogglePlay()
	due[1]()
	if len(due) != 2 {
		t.Errorf("paused, %d more saves due", len(due)-2)
	}
}

// Shuffle, Smart Shuffle, and Repeat persist across restarts and survive idle Connect cluster events.
func TestShuffleAndRepeatPersistAcrossRestartsAndIdleCluster(t *testing.T) {
	dir := t.TempDir()
	oldSessionPath := sessionPath
	sessionPath = func() (string, error) { return filepath.Join(dir, "session.json"), nil }
	t.Cleanup(func() { sessionPath = oldSessionPath })

	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.path = filepath.Join(dir, "settings.json")
	a.pl.SmartFeed = func(spotify.Track, func([]spotify.Track)) {}
	a.pl.Cue([]spotify.Track{
		{ID: "1", Name: "Song 1", Duration: 180000},
		{ID: "2", Name: "Song 2", Duration: 180000},
	})
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	// Click shuffle twice in transport bar -> Smart Shuffle, and repeat once -> RepeatAll.
	if err := tt.Click("随机播放"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("随机播放"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("循环"); err != nil {
		t.Fatal(err)
	}
	if a.pl.ShuffleMode() != player.ShuffleSmart || a.pl.Repeat() != player.RepeatAll {
		t.Fatalf("before save: shuffleMode=%v repeat=%v", a.pl.ShuffleMode(), a.pl.Repeat())
	}
	a.saveSession()

	// Simulate app restart with Connect starting up and receiving an idle cluster event.
	b := connectTestApp()
	b.settings = a.settings
	b.applySavedPlaybackModes()
	b.restoreSession()
	b.connectEvent(spotify.ConnectEvent{
		Kind: "cluster",
		Data: json.RawMessage(`{"active_device_id":"","player_state":{"options":{"shuffling_context":false,"repeating_context":false,"repeating_track":false}}}`),
	})
	if b.pl.ShuffleMode() != player.ShuffleSmart || b.pl.Repeat() != player.RepeatAll {
		t.Fatalf("after restart and idle cluster: shuffleMode=%v repeat=%v", b.pl.ShuffleMode(), b.pl.Repeat())
	}
}
