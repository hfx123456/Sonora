package localmusic

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Overlapping sources import each audio file once, read its length and keep its path and sidecar lyrics.
func TestScanLocalSources(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "中文目录")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, "歌手 - 歌名.wav")
	data := make([]byte, 44+16000*2*3)
	copy(data, "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)-8))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1)
	binary.LittleEndian.PutUint16(data[22:], 1)
	binary.LittleEndian.PutUint32(data[24:], 16000)
	binary.LittleEndian.PutUint32(data[28:], 32000)
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(len(data)-44))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "歌手 - 歌名.lrc"), []byte("[00:01.00]第一句"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(folder, "protected.m4p"), []byte("not playable"), 0o600)
	tracks, warnings := Scan([]string{root, folder, path}, "")
	if len(warnings) != 0 || len(tracks) != 1 {
		t.Fatalf("tracks=%v warnings=%v", tracks, warnings)
	}
	track := tracks[0]
	if track.Name != "歌名" || track.ArtistNames() != "歌手" || track.Length() != 3*time.Second || track.LocalPath != path || !track.Local() {
		t.Fatalf("metadata: %+v", track)
	}
	if Lyrics(track) != "[00:01.00]第一句" {
		t.Fatal("sidecar lyrics were not read")
	}
	os.Remove(path)
	tracks, _ = Scan([]string{root}, "")
	if len(tracks) != 0 {
		t.Fatal("deleted file remained in a rescan")
	}
}

// Files whose bytes are unavailable stay visible but cannot be played as if they were downloaded.
func TestUnavailableLocalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unavailable.mp3")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tracks, warnings := Scan([]string{path}, "")
	if len(tracks) != 1 || len(warnings) != 1 || tracks[0].Playable() || tracks[0].LocalError == "" {
		t.Fatalf("unavailable file: %+v, warnings %v", tracks, warnings)
	}
}
