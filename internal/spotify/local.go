package spotify

import (
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var localCatalog struct {
	sync.RWMutex
	tracks []Track
}

func SetLocalTracks(tracks []Track) {
	localCatalog.Lock()
	localCatalog.tracks = append([]Track(nil), tracks...)
	localCatalog.Unlock()
}

func (t *Track) Local() bool {
	return t.LocalPath != "" || strings.HasPrefix(t.ID, "local:") || strings.HasPrefix(t.ID, "spotify:local:")
}

func MatchLocal(uri string) (Track, bool) {
	parts := strings.Split(strings.TrimPrefix(uri, "spotify:local:"), ":")
	if !strings.HasPrefix(uri, "spotify:local:") || len(parts) != 4 {
		return Track{}, false
	}
	for index := range parts {
		parts[index], _ = url.QueryUnescape(parts[index])
	}
	localCatalog.RLock()
	defer localCatalog.RUnlock()
	var found Track
	for _, track := range localCatalog.tracks {
		if !strings.EqualFold(track.Name, parts[2]) || !strings.EqualFold(track.ArtistNames(), parts[0]) {
			continue
		}
		if parts[1] != "" && !strings.EqualFold(track.Album.Name, parts[1]) {
			continue
		}
		if found.ID != "" {
			return Track{}, false
		}
		found = track
	}
	return found, found.ID != ""
}

func FileURL(path string) string {
	path = filepath.ToSlash(path)
	// Preserve Windows UNC paths (//host/share/file -> file://host/share/file).
	if strings.HasPrefix(path, "//") {
		if host, rest, ok := strings.Cut(strings.TrimPrefix(path, "//"), "/"); ok && host != "" {
			return (&url.URL{Scheme: "file", Host: host, Path: "/" + rest}).String()
		}
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func localPlaceholder(uri string) Track {
	track := Track{ID: uri, LocalURI: uri, Name: "未匹配的本地歌曲", Unplayable: true}
	parts := strings.Split(strings.TrimPrefix(uri, "spotify:local:"), ":")
	if len(parts) != 4 {
		return track
	}
	for index := range parts {
		parts[index], _ = url.QueryUnescape(parts[index])
	}
	track.Name, track.Album.Name = parts[2], parts[1]
	track.Artists = []Artist{{Name: parts[0]}}
	seconds, _ := strconv.ParseInt(parts[3], 10, 64)
	track.Duration = seconds * 1000
	return track
}

const maxLocalCoverBytes = 10 << 20

func ReadLocalURL(address string) ([]byte, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return nil, err
	}
	path := parsed.Path
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
		path = "//" + parsed.Host + path
	} else if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	f, err := os.Open(filepath.FromSlash(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxLocalCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxLocalCoverBytes {
		return nil, errors.New("local file too large")
	}
	return data, nil
}
