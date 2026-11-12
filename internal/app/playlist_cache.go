package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"vinyl/internal/spotify"
)

type playlistSnapshot struct {
	Version  int
	User     string
	Key      string
	Stored   time.Time
	Playlist *spotify.Playlist
}

var playlistDetail = spotify.PlaylistDetail

func playlistSnapshotPath(root, user, key string) string {
	return filepath.Join(root, fmt.Sprintf("%x", sha256.Sum256([]byte(user))), fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}

func readPlaylistSnapshot(root, user, key string) *playlistSnapshot {
	if root == "" || user == "" {
		return nil
	}
	data, err := os.ReadFile(playlistSnapshotPath(root, user, key))
	if err != nil {
		return nil
	}
	var snapshot playlistSnapshot
	if json.Unmarshal(data, &snapshot) != nil || snapshot.Version != 1 || snapshot.User != user || snapshot.Key != key || snapshot.Playlist == nil {
		return nil
	}
	return &snapshot
}

func savePlaylistSnapshot(root, user, key string, playlist *spotify.Playlist, stored time.Time) {
	if root == "" || user == "" || playlist == nil {
		return
	}
	path := playlistSnapshotPath(root, user, key)
	data, err := json.Marshal(playlistSnapshot{1, user, key, stored, playlist})
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0o700)
	}
	if err == nil {
		err = writeAtomic(path, data)
	}
	if err != nil {
		log.Printf("playlist cache: %v", err)
	}
}
