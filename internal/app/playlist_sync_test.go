package app

import (
	"testing"
	"time"

	"vinyl/internal/spotify"
)

// Every owned or saved playlist is queued without opening it, with one request at a time.
func TestPlaylistSyncCoversWholeLibrary(t *testing.T) {
	application := newTestApp()
	application.playlistCacheRoot = "enabled"
	application.mine = []spotify.Playlist{{ID: spotify.LikedURI}, {ID: "spotify:playlist:one"}}
	application.saved = []spotify.Playlist{{ID: "spotify:playlist:one"}, {ID: "spotify:playlist:two"}, {ID: "spotify:album:album"}}
	application.loadingPl[spotify.LikedURI] = true
	now := time.Unix(1000, 0)
	application.syncPlaylistCaches(now)
	if application.playlistSync.active != spotify.LikedURI || len(application.playlistSync.pending) != 2 {
		t.Fatal("background caching missed a playlist or duplicated the liked collection")
	}
	application.syncPlaylistCaches(now)
	if len(application.playlistSync.pending) != 2 || application.playlistSync.next() != "" {
		t.Fatal("another refresh duplicated work or started concurrent requests")
	}
	application.playlistSync.complete(spotify.LikedURI, now)
	if application.playlistSync.next() != "spotify:playlist:one" {
		t.Fatal("next playlist was not scheduled")
	}
	application.playlistSync.complete("spotify:playlist:one", now)
	if application.playlistSync.next() != "spotify:playlist:two" {
		t.Fatal("saved playlists were not included")
	}
}

// Edits during a fetch require one follow-up, while ordinary polls wait five minutes.
func TestPlaylistSyncEditsAndPolling(t *testing.T) {
	var state playlistSyncState
	now := time.Unix(1000, 0)
	id := "spotify:playlist:one"
	state.enqueue(id, false, now)
	state.next()
	state.enqueue(id, true, now)
	state.enqueue(id, true, now)
	if len(state.pending) != 1 || state.next() != "" {
		t.Fatal("edits duplicated requests or ran concurrently")
	}
	state.complete(id, now)
	if state.next() != id {
		t.Fatal("edit during the request was lost")
	}
	state.complete(id, now)
	state.enqueue(id, false, now.Add(playlistSyncInterval-time.Second))
	if state.next() != "" {
		t.Fatal("unchanged playlists refreshed too often")
	}
	state.enqueue(id, false, now.Add(playlistSyncInterval))
	if state.next() != id {
		t.Fatal("external edits would never be synchronized")
	}
}

// A freshly loaded collection must not be fetched again when the library first arrives.
func TestPlaylistSyncReusesForegroundRefresh(t *testing.T) {
	var state playlistSyncState
	now := time.Unix(1000, 0)
	state.complete(spotify.LikedURI, now)
	state.enqueue(spotify.LikedURI, false, now)
	if state.next() != "" {
		t.Fatal("the initial liked collection was fetched twice")
	}
	state.enqueue("spotify:playlist:new", false, now)
	if state.next() != "spotify:playlist:new" {
		t.Fatal("a newly discovered playlist was not cached immediately")
	}
}

// Signing out, quitting, or disabling storage must stop the scheduler from dispatching work.
func TestPlaylistSyncInactive(t *testing.T) {
	for _, mode := range []string{"signed-out", "quitting", "no-storage"} {
		application := newTestApp()
		application.playlistCacheRoot = "enabled"
		switch mode {
		case "signed-out":
			application.profile = nil
		case "quitting":
			application.quitting = true
		case "no-storage":
			application.playlistCacheRoot = ""
		}
		application.syncPlaylistCaches(time.Unix(1000, 0))
		application.cachePlaylistSoon("spotify:playlist:one")
		if application.playlistSync.active != "" || len(application.playlistSync.pending) != 0 {
			t.Fatalf("%s scheduled a request", mode)
		}
	}
}
