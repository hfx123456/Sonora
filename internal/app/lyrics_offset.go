package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/spotify"
)

const (
	lyricsOffsetMinMs     int64 = -30000
	lyricsOffsetMaxMs     int64 = 30000
	lyricsOffsetStepMs    int64 = 200
	lyricsOffsetBigStepMs int64 = 500
	maxSavedLyricsOffsets       = 2000
)

type lyricsOffsetEntry struct {
	Ms        int64 `json:"ms"`
	UpdatedAt int64 `json:"updatedAt"`
}

type lyricsOffsetFile struct {
	Version int                          `json:"version"`
	Offsets map[string]lyricsOffsetEntry `json:"offsets"`
}

// lyricsOffsetStore remembers per-track, per-lyrics-source timing offsets in
// UserData/lyrics-offsets.json. Stores without a path (as in tests) keep state
// in memory without touching disk.
type lyricsOffsetStore struct {
	path    string
	entries map[string]lyricsOffsetEntry
}

func lyricsOffsetsPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lyrics-offsets.json"), nil
}

func loadLyricsOffsets() lyricsOffsetStore {
	path, err := lyricsOffsetsPath()
	if err != nil {
		return lyricsOffsetStore{}
	}
	return loadLyricsOffsetsFrom(path)
}

func loadLyricsOffsetsFrom(path string) lyricsOffsetStore {
	st := lyricsOffsetStore{path: path, entries: map[string]lyricsOffsetEntry{}}
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var raw lyricsOffsetFile
	if err := json.Unmarshal(data, &raw); err != nil || raw.Version != 1 {
		return st
	}
	for k, v := range raw.Offsets {
		ms := clampLyricsOffsetMs(v.Ms)
		if k == "" || ms == 0 {
			continue
		}
		v.Ms = ms
		st.entries[k] = v
	}
	return st
}

func clampLyricsOffsetMs(ms int64) int64 {
	return max(lyricsOffsetMinMs, min(lyricsOffsetMaxMs, ms))
}

func (s *lyricsOffsetStore) get(key string) int64 {
	if key == "" || s.entries == nil {
		return 0
	}
	return clampLyricsOffsetMs(s.entries[key].Ms)
}

func (s *lyricsOffsetStore) set(key string, ms int64, now time.Time) error {
	if key == "" {
		return nil
	}
	ms = clampLyricsOffsetMs(ms)
	if s.entries == nil {
		s.entries = map[string]lyricsOffsetEntry{}
	}
	if ms == 0 {
		delete(s.entries, key)
	} else {
		s.entries[key] = lyricsOffsetEntry{Ms: ms, UpdatedAt: now.Unix()}
		s.prune()
	}
	return s.save()
}

func (s *lyricsOffsetStore) prune() {
	if len(s.entries) <= maxSavedLyricsOffsets {
		return
	}
	type pair struct {
		key string
		at  int64
	}
	all := make([]pair, 0, len(s.entries))
	for k, v := range s.entries {
		all = append(all, pair{k, v.UpdatedAt})
	}
	slices.SortFunc(all, func(a, b pair) int {
		if a.at != b.at {
			if a.at < b.at {
				return -1
			}
			return 1
		}
		return strings.Compare(a.key, b.key)
	})
	drop := len(all) - maxSavedLyricsOffsets
	for i := range drop {
		delete(s.entries, all[i].key)
	}
}

func (s *lyricsOffsetStore) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(lyricsOffsetFile{Version: 1, Offsets: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

func lyricsOffsetTrackKey(tr *spotify.Track) string {
	if tr == nil || tr.Episode {
		return ""
	}
	if tr.Local() {
		return tr.ID
	}
	if tr.LinkedFrom != "" {
		return "spotify:track:" + tr.LinkedFrom
	}
	return tr.URI()
}

func lyricsImportHash(raw string) string {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:4])
}

func lyricsOffsetSourceKey(source, importHash string) string {
	if importHash != "" {
		return "import:" + importHash
	}
	lower := strings.ToLower(strings.TrimSpace(source))
	switch {
	case lower == "":
		return "default"
	case strings.Contains(lower, "qq"):
		return "qq"
	case strings.Contains(lower, "网易云") || strings.Contains(lower, "netease"):
		return "netease"
	case strings.Contains(lower, "lrclib"):
		return "lrclib"
	case strings.Contains(lower, "播放器歌词") || lower == "player":
		return "player"
	default:
		if base, _, ok := strings.Cut(lower, " · "); ok && strings.TrimSpace(base) != "" {
			return strings.TrimSpace(base)
		}
		return lower
	}
}

func (a *app) currentLyricsSourceKey() string {
	tr := a.pl.Current()
	if tr == nil || tr.Episode {
		return ""
	}
	if a.fusion.key == fusionTrackKey(tr) || a.fusion.key == "" {
		if a.fusion.importHash != "" || a.fusion.source != "" {
			return lyricsOffsetSourceKey(a.fusion.source, a.fusion.importHash)
		}
	}
	if lines, _ := a.pl.Lyrics(); len(lines) > 0 {
		return "player"
	}
	return "default"
}

func (a *app) currentLyricsOffsetKey() string {
	trackKey := lyricsOffsetTrackKey(a.pl.Current())
	if trackKey == "" {
		return ""
	}
	sourceKey := a.currentLyricsSourceKey()
	if sourceKey == "" {
		return ""
	}
	return trackKey + "\t" + sourceKey
}

func (a *app) currentLyricsOffsetMs() int64 {
	return a.lyricsOffsets.get(a.currentLyricsOffsetKey())
}

func (a *app) currentLyricsOffset() time.Duration {
	return time.Duration(a.currentLyricsOffsetMs()) * time.Millisecond
}

// lyricsPosition returns the playback timestamp shifted by the current track's
// remembered lyrics offset, shared across desktop/capsule lyrics, the Fusion
// panel, Now Playing scrolling lyrics, and the stage visualizer.
func (a *app) lyricsPosition() time.Duration {
	return a.pl.SongPosition() + a.currentLyricsOffset()
}

func (a *app) setCurrentLyricsOffset(ms int64) {
	key := a.currentLyricsOffsetKey()
	if key == "" {
		return
	}
	if err := a.lyricsOffsets.set(key, ms, time.Now()); err != nil {
		log.Printf("lyrics offsets: %v", err)
	}
	a.resizeFusion()
	a.redraw()
}

func (a *app) nudgeLyricsOffset(deltaMs int64) {
	key := a.currentLyricsOffsetKey()
	if key == "" {
		return
	}
	next := clampLyricsOffsetMs(a.lyricsOffsets.get(key) + deltaMs)
	a.setCurrentLyricsOffset(next)
	switch {
	case next == 0:
		a.lyricsToast = "本曲歌词时间已归零"
	case deltaMs > 0:
		a.lyricsToast = fmt.Sprintf("本曲歌词提前 %.1f 秒（当前 %+.1fs）", float64(deltaMs)/1000, float64(next)/1000)
	case deltaMs < 0:
		a.lyricsToast = fmt.Sprintf("本曲歌词延后 %.1f 秒（当前 %+.1fs）", float64(-deltaMs)/1000, float64(next)/1000)
	}
}

func (a *app) resetCurrentLyricsOffset() {
	key := a.currentLyricsOffsetKey()
	if key == "" {
		return
	}
	a.setCurrentLyricsOffset(0)
	a.lyricsToast = "已重置本曲歌词时间"
}

func formatLyricsOffset(ms int64) string {
	if ms == 0 {
		return "0.0s"
	}
	return fmt.Sprintf("%+.1fs", float64(ms)/1000)
}
