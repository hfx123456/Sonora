package spotify

import (
	"os"
	"path/filepath"
	"testing"
)

// A playlist's local URI matches a unique title, artist and album, while ambiguous files stay unplayable.
func TestLocalPlaylistMatch(t *testing.T) {
	defer SetLocalTracks(nil)
	track := Track{ID: "local:one", LocalPath: "song.mp3", Name: "歌 名", Artists: []Artist{{Name: "歌手"}}, Album: Album{Name: "专辑"}}
	uri := "spotify:local:%E6%AD%8C%E6%89%8B:%E4%B8%93%E8%BE%91:%E6%AD%8C+%E5%90%8D:200"
	SetLocalTracks([]Track{track})
	got := tracks([]*gqlTrack{{URI: uri, uid: "item"}}, nil)
	if len(got) != 1 || got[0].LocalPath != "song.mp3" || got[0].UID != "item" || !got[0].Playable() {
		t.Fatalf("match: %+v", got)
	}
	duplicate := track
	duplicate.ID, duplicate.LocalPath = "local:two", "other.mp3"
	SetLocalTracks([]Track{track, duplicate})
	got = tracks([]*gqlTrack{{URI: uri}}, nil)
	if len(got) != 1 || got[0].Playable() || got[0].LocalPath != "" {
		t.Fatal("ambiguous local URI chose an arbitrary file")
	}
}

// Cover paths survive URL escaping, resizing fragments and non-ASCII filenames.
func TestLocalCoverURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "封面 #1.jpg")
	if err := os.WriteFile(path, []byte("picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := Fetch(Sized(FileURL(path), 96))
	if err != nil || string(data) != "picture" {
		t.Fatalf("cover: %s %v", data, err)
	}
}
