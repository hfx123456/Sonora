package lyrics

import (
	"testing"
	"time"
)

// Cached lyrics survive a restart without losing translations or real word timing, and weaker fallback lyrics cannot replace them.
func TestCachePreservesLyricsQuality(t *testing.T) {
	dir := t.TempDir()
	key := "spotify:track:one"
	lines := []Line{{Text: "你好", Translation: "hello", At: time.Second, End: 3 * time.Second, Timed: true, Words: []Word{{Text: "你", Start: time.Second, End: 2 * time.Second}, {Text: "好", Start: 2 * time.Second, End: 3 * time.Second}}}}
	SaveCached(dir, key, "timed", lines)
	SaveCached(dir, key, "fallback", []Line{{Text: "你好", At: time.Second}})
	got := LoadCached(dir, key)
	if len(got.Lines) != 1 || got.Source != "timed" || !got.Lines[0].Timed || len(got.Lines[0].Words) != 2 || got.Lines[0].Translation != "hello" {
		t.Fatal("cache lost the original lyric quality")
	}
	if len(LoadCached(dir, "spotify:track:other").Lines) != 0 {
		t.Fatal("lyrics leaked to another track")
	}
	SaveCached(dir, key, "empty", nil)
	if len(LoadCached(dir, key).Lines) == 0 {
		t.Fatal("a failed lookup erased cached lyrics")
	}
}

// Translated lyrics outrank untranslated timed player lyrics in the shared cache.
func TestSaveCachedPrefersTranslatedOverUntranslated(t *testing.T) {
	dir := t.TempDir()
	key := "spotify:track:koko"
	SaveCached(dir, key, "播放器歌词", []Line{{Text: "Koko soko", At: time.Second, Timed: true}})
	SaveCached(dir, key, "QQ 音乐 · Lyricify", []Line{{Text: "Koko soko", Translation: "无所不在", At: time.Second}})
	got := LoadCached(dir, key)
	if got.Source != "QQ 音乐 · Lyricify" || got.Lines[0].Translation != "无所不在" {
		t.Fatalf("translated lyrics did not upgrade untranslated cache: %+v", got)
	}
	SaveCached(dir, key, "播放器歌词", []Line{{Text: "Koko soko", At: time.Second, Timed: true}})
	got = LoadCached(dir, key)
	if got.Source != "QQ 音乐 · Lyricify" || got.Lines[0].Translation != "无所不在" {
		t.Fatalf("untranslated player lyrics overwrote translation: %+v", got)
	}
}
