package app

import (
	"testing"
	"time"

	"vinyl/internal/spotify"
)

func TestPlaylistPlayEntryRespectsSort(t *testing.T) {
	a := newTestApp()
	pl := &spotify.Playlist{
		ID:   "spotify:playlist:sort-entry",
		Name: "Sorted List",
		Tracks: []spotify.Track{
			{ID: "t-z", Name: "Zulu", Artists: []spotify.Artist{{Name: "A"}}},
			{ID: "t-a", Name: "Alpha", Artists: []spotify.Artist{{Name: "B"}}},
		},
	}
	a.playlists[pl.ID] = pl
	a.playlistSorts = map[string]playlistSort{pl.ID: playlistByTitle}

	// Home card play button calls loadPlaylist(id, true) -> requestPlaylist -> playPlaylist.
	a.loadPlaylist(pl.ID, true)
	cur := a.pl.Current()
	if cur == nil || cur.Name != "Alpha" {
		t.Fatalf("cached loadPlaylist played %+v, want Alpha", cur)
	}

	// Freshly fetched playlist via receivePlaylist(thenPlay=true) must also follow user sort.
	delete(a.playlists, pl.ID)
	a.receivePlaylist(pl.ID, pl, nil, true, time.Now())
	cur = a.pl.Current()
	if cur == nil || cur.Name != "Alpha" {
		t.Fatalf("receivePlaylist played %+v, want Alpha", cur)
	}
}

func TestFilterLocalScopeCachesResult(t *testing.T) {
	a := newTestApp()
	ordered := []spotify.Track{
		{ID: "loc:1", Name: "Local 1", LocalPath: "/a.mp3"},
		{ID: "cloud:1", Name: "Cloud 1", CloudURL: "https://dav.example/1.mp3"},
		{ID: "cloud:2", Name: "Cloud 2", CloudURL: "https://dav.example/2.mp3"},
	}
	a.localScope = 2

	first := a.filterLocalScope(ordered)
	second := a.filterLocalScope(ordered)
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("got lens (%d, %d), want (2, 2)", len(first), len(second))
	}
	if &first[0] != &second[0] {
		t.Fatal("filterLocalScope should reuse cached slice across repaints")
	}

	a.localScope = 1
	localOnly := a.filterLocalScope(ordered)
	if len(localOnly) != 1 || localOnly[0].ID != "loc:1" {
		t.Fatalf("local scope got %+v, want only loc:1", localOnly)
	}
}

func TestDefaultSettingsStartupAnimation(t *testing.T) {
	if !defaultSettings().StartupAnimation {
		t.Fatal("StartupAnimation should be enabled by default")
	}
}
