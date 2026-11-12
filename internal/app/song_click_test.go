package app

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"vinyl/internal/spotify"
)

// A song row must wait for a double click before starting playback.
func TestSongRowDoubleClickPlays(t *testing.T) {
	a := artistApp()
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("孤勇者"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Current() != nil {
		t.Fatal("single click started a song")
	}
	if err := tt.Click("孤勇者"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Current() == nil || a.pl.Current().ID != "1" {
		t.Fatal("double click did not start the song")
	}
	if !a.pl.Playing() {
		t.Fatal("double click did not play the song")
	}
}

// Clicking another song must leave the currently playing song playing.
func TestSongRowClickOtherKeepsPlaying(t *testing.T) {
	a := artistApp()
	list := a.playlists["42"]
	list.Tracks = append(list.Tracks, spotify.Track{ID: "2", Name: "另一首歌", Duration: 200000})
	tt := ui.NewTester(a.view, 1200, 800)
	a.pl.PlayQueue(list.Tracks, 0)
	tt.Frame()
	if !a.pl.Playing() {
		t.Fatal("playback did not start")
	}
	if err := tt.Click("另一首歌"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.pl.Playing() || a.pl.Current().ID != "1" {
		t.Fatal("single click changed playback")
	}
}

// A single click on a song row pauses the current playback.
func TestSongRowClickPauses(t *testing.T) {
	a := artistApp()
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	a.pl.PlayQueue(a.playlists["42"].Tracks, 0)
	tt.Frame()
	if !a.pl.Playing() {
		t.Fatal("playback did not start")
	}
	if err := tt.Click("孤勇者"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Playing() {
		t.Fatal("single click did not pause playback")
	}
}

// Double clicking another song while one plays starts the new song, even
// though the first click of the pair was on the song playing.
func TestSongRowDoubleClickOtherPlays(t *testing.T) {
	a := artistApp()
	list := a.playlists["42"]
	list.Tracks = append(list.Tracks, spotify.Track{ID: "2", Name: "另一首歌", Duration: 200000})
	tt := ui.NewTester(a.view, 1200, 800)
	a.pl.PlayQueue(list.Tracks, 0)
	tt.Frame()
	if !a.pl.Playing() {
		t.Fatal("playback did not start")
	}
	if err := tt.Click("另一首歌"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("另一首歌"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Current() == nil || a.pl.Current().ID != "2" {
		t.Fatal("double click did not switch songs")
	}
	if !a.pl.Playing() {
		t.Fatal("double click did not play the other song")
	}
}

// With Spotify Connect listening, a double click still plays here: the
// queue Spotify has plays on, and a new one must not wait for its answer.
func TestSongRowDoubleClickPlaysWithConnect(t *testing.T) {
	defer func(old func(*spotify.Connect, string, string, map[string]any) error) {
		connectCommand = old
	}(connectCommand)
	connectCommand = func(*spotify.Connect, string, string, map[string]any) error {
		return errors.New("offline")
	}
	a := artistApp()
	a.connect = &connectState{client: spotify.NewConnect("local", "Vinyl", nil), observer: false, receiver: false,
		jobs: make(chan func() error, 32), done: make(chan struct{})}
	t.Cleanup(a.connect.client.Close)
	a.pl.Control = a.connectControl
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("孤勇者"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("孤勇者"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Current() == nil || a.pl.Current().ID != "1" {
		t.Fatal("double click did not start the song")
	}
	if !a.pl.Playing() {
		t.Fatalf("double click did not play: playing %v, loading %v", a.pl.Playing(), a.pl.Loading())
	}
	select {
	case job := <-a.connect.jobs:
		job()
	default:
	}
	if !a.pl.Playing() {
		t.Fatalf("a failed play request left the song waiting: playing %v, loading %v", a.pl.Playing(), a.pl.Loading())
	}
}
