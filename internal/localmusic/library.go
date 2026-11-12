package localmusic

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dhowden/tag"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"

	"vinyl/internal/spotify"
)

func Supported(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3", ".flac", ".m4a", ".wav", ".ogg", ".opus", ".aac":
		return true
	}
	return false
}

func Scan(roots []string, coverDir string) ([]spotify.Track, []string) {
	var tracks []spotify.Track
	var warnings []string
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s：%v", path, err))
				return nil
			}
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !Supported(path) {
				return nil
			}
			absolute, err := filepath.Abs(path)
			if err != nil || seen[absolute] {
				return nil
			}
			seen[absolute] = true
			track, err := Read(absolute, coverDir)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("%s：%v", path, err))
				if track.ID != "" {
					track.Unplayable, track.LocalError = true, err.Error()
					tracks = append(tracks, track)
				}
				return nil
			}
			tracks = append(tracks, track)
			return nil
		})
		if err != nil {
			warnings = append(warnings, err.Error())
		}
	}
	slices.SortFunc(tracks, func(first, second spotify.Track) int {
		if first.Unplayable != second.Unplayable {
			if first.Unplayable {
				return 1
			}
			return -1
		}
		return cmp.Or(strings.Compare(strings.ToLower(first.Name), strings.ToLower(second.Name)), strings.Compare(first.LocalPath, second.LocalPath))
	})
	return tracks, warnings
}

func Read(path, coverDir string) (spotify.Track, error) {
	sum := sha256.Sum256([]byte(path))
	track := spotify.Track{ID: "local:" + hex.EncodeToString(sum[:16]), LocalPath: path, Name: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))}
	if info, err := os.Stat(path); err == nil && cloudOnly(info) {
		return track, fmt.Errorf("OneDrive 云端文件尚未下载到本机")
	}
	file, err := os.Open(path)
	if err != nil {
		return track, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return track, fmt.Errorf("无法读取音频文件")
	}
	var probe [1]byte
	if _, err := file.ReadAt(probe[:], 0); err != nil {
		return track, err
	}
	track.Duration = duration(file, info.Size()).Milliseconds()
	file.Seek(0, 0)
	if metadata, err := tag.ReadFrom(file); err == nil {
		track.Name = cmp.Or(strings.TrimSpace(metadata.Title()), track.Name)
		if artist := cmp.Or(metadata.Artist(), metadata.AlbumArtist()); artist != "" {
			track.Artists = []spotify.Artist{{Name: artist}}
		}
		track.Album.Name = metadata.Album()
		track.LocalLyrics = metadata.Lyrics()
		if picture := metadata.Picture(); picture != nil && len(picture.Data) <= 10<<20 && coverDir != "" {
			hash := sha256.Sum256(picture.Data)
			cover := filepath.Join(coverDir, hex.EncodeToString(hash[:16])+".img")
			if os.MkdirAll(coverDir, 0o700) == nil && os.WriteFile(cover, picture.Data, 0o600) == nil {
				track.Album.PicURL = spotify.FileURL(cover)
			}
		}
	}
	if len(track.Artists) == 0 {
		if artist, title, ok := strings.Cut(track.Name, " - "); ok {
			track.Artists = []spotify.Artist{{Name: artist}}
			track.Name = title
		}
	}
	if track.Album.PicURL == "" {
		for _, name := range []string{"cover.jpg", "cover.png", "folder.jpg", "folder.png"} {
			cover := filepath.Join(filepath.Dir(path), name)
			if info, err := os.Stat(cover); err == nil && info.Mode().IsRegular() && info.Size() <= 10<<20 {
				track.Album.PicURL = spotify.FileURL(cover)
				break
			}
		}
	}
	return track, nil
}

func Lyrics(track spotify.Track) string {
	path := strings.TrimSuffix(track.LocalPath, filepath.Ext(track.LocalPath)) + ".lrc"
	if data, err := os.ReadFile(path); err == nil && len(data) <= 2<<20 {
		if len(data) >= 2 && (data[0] == 0xff && data[1] == 0xfe || data[0] == 0xfe && data[1] == 0xff) {
			if decoded, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(data); err == nil {
				return string(decoded)
			}
		}
		if strings.ToValidUTF8(string(data), "") != string(data) {
			if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil {
				return string(decoded)
			}
		}
		return strings.TrimPrefix(string(data), "\ufeff")
	}
	return track.LocalLyrics
}
