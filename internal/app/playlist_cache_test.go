package app

import (
	"errors"
	"os"
	"testing"
	"time"

	"vinyl/internal/spotify"
)

// A saved playlist survives restart and stays visible when the online refresh fails.
func TestPlaylistSnapshotSurvivesOfflineRestart(t *testing.T) {
	root := t.TempDir()
	stored := time.Unix(100, 0)
	playlist := &spotify.Playlist{ID: spotify.LikedURI, Tracks: []spotify.Track{{ID: "one", Name: "Saved song"}}}
	savePlaylistSnapshot(root, "listener", playlist.ID, playlist, stored)
	cached := readPlaylistSnapshot(root, "listener", playlist.ID)
	if cached == nil || !cached.Stored.Equal(stored) || len(cached.Playlist.Tracks) != 1 || cached.Playlist.Tracks[0].Name != "Saved song" {
		t.Fatal("playlist was not restored from disk")
	}
	application := newTestApp()
	application.receivePlaylist(playlist.ID, cached.Playlist, nil, false, cached.Stored)
	application.receivePlaylist(playlist.ID, nil, errors.New("offline"), false, stored.Add(time.Second))
	if application.playlists[playlist.ID] != cached.Playlist || !application.liked["one"] {
		t.Fatal("failed refresh erased the cached playlist or its hearts")
	}
}

// A refreshed snapshot replaces removed songs, including a now-empty playlist.
func TestPlaylistSnapshotReplacesOldTracks(t *testing.T) {
	root := t.TempDir()
	playlist := &spotify.Playlist{ID: "spotify:playlist:one", Tracks: []spotify.Track{{ID: "removed"}}}
	savePlaylistSnapshot(root, "listener", playlist.ID, playlist, time.Unix(100, 0))
	playlist.Tracks = nil
	savePlaylistSnapshot(root, "listener", playlist.ID, playlist, time.Unix(200, 0))
	cached := readPlaylistSnapshot(root, "listener", playlist.ID)
	if cached == nil || len(cached.Playlist.Tracks) != 0 || cached.Stored.Unix() != 200 {
		t.Fatal("refresh kept stale songs")
	}
}

// Account changes and corrupt files must never expose another listener's cached collection.
func TestPlaylistSnapshotIsolationAndCorruption(t *testing.T) {
	root := t.TempDir()
	playlist := &spotify.Playlist{ID: spotify.LikedURI, Tracks: []spotify.Track{{ID: "private"}}}
	savePlaylistSnapshot(root, "first", playlist.ID, playlist, time.Unix(100, 0))
	for _, user := range []string{"second", ""} {
		if readPlaylistSnapshot(root, user, playlist.ID) != nil {
			t.Fatal("another account received the cached playlist")
		}
	}
	path := playlistSnapshotPath(root, "first", playlist.ID)
	for _, data := range []string{"{", `{"Version":1,"User":"second","Key":"spotify:collection:tracks","Playlist":{}}`, `{"Version":2,"User":"first","Key":"spotify:collection:tracks","Playlist":{}}`} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if readPlaylistSnapshot(root, "first", playlist.ID) != nil {
			t.Fatal("invalid snapshot was accepted")
		}
	}
}
