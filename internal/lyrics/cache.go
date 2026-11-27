package lyrics

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var cacheMu sync.Mutex

type Cached struct {
	Key    string
	Source string
	Lines  []Line
}

func cachePath(dir, key string) string {
	return filepath.Join(dir, fmt.Sprintf("%x.json", sha256.Sum256([]byte(key))))
}

func loadCachedUnlocked(dir, key string) Cached {
	var cached Cached
	if dir == "" || key == "" {
		return cached
	}
	data, err := os.ReadFile(cachePath(dir, key))
	if err != nil || json.Unmarshal(data, &cached) != nil || cached.Key != key {
		return Cached{}
	}
	return cached
}

func LoadCached(dir, key string) Cached {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	return loadCachedUnlocked(dir, key)
}

func SaveCached(dir, key, source string, lines []Line) {
	saveCached(dir, key, source, lines, false)
}

// SaveCachedForce unconditionally replaces any previously cached lyrics for
// key, used for explicit user actions like manual lyric import or re-search.
func SaveCachedForce(dir, key, source string, lines []Line) {
	saveCached(dir, key, source, lines, true)
}

func saveCached(dir, key, source string, lines []Line, force bool) {
	if dir == "" || key == "" || len(lines) == 0 {
		return
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	if !force {
		previous := loadCachedUnlocked(dir, key)
		quality := func(items []Line) int {
			score := 0
			hasTranslation := false
			for _, line := range items {
				if !line.Unsynced {
					score = max(score, 1)
				}
				if line.Timed {
					score = max(score, 2)
				}
				if line.Translation != "" {
					hasTranslation = true
				}
			}
			if hasTranslation {
				score += 4
			}
			return score
		}
		if quality(previous.Lines) > quality(lines) {
			return
		}
	}
	data, err := json.Marshal(Cached{key, source, lines})
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return
	}
	file, err := os.CreateTemp(dir, "lyrics-*.part")
	if err != nil {
		return
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	closeErr := file.Close()
	if err == nil && closeErr == nil {
		os.Rename(file.Name(), cachePath(dir, key))
	}
}
