package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"vinyl/internal/fusion"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

// A failed external search must use the current player's lyrics rather than report no lyrics on the island.
func TestFusionFallsBackToPlayerLyrics(t *testing.T) {
	application := newTestApp()
	application.pl.Cue([]spotify.Track{{ID: "fallback", Name: "7-11 的爱恋"}})
	application.pl.SetLyrics([]lyrics.Line{{Text: "一起长大的约定", At: time.Second, End: 5 * time.Second}})
	key := fusionTrackKey(application.pl.Current())
	application.fusion.key, application.fusion.generation = key, 1
	application.receiveFusion(key, 1, fusion.Result{}, errors.New("not found"))
	if len(application.fusion.lines) != 1 || application.fusion.message != "" || application.fusion.loading {
		t.Fatal("player lyrics were not used as a fallback")
	}
}

// Untranslated foreign player caches must not prevent Fusion from searching bilingual lyrics.
func TestFusionCacheUsabilityChecks(t *testing.T) {
	if fusionCacheUsable(lyrics.Cached{Source: "播放器歌词", Lines: []lyrics.Line{{Text: "Koko soko", At: time.Second}}}) {
		t.Fatal("untranslated foreign player cache blocked Fusion lookup")
	}
	if !fusionCacheUsable(lyrics.Cached{Source: "播放器歌词", Lines: []lyrics.Line{{Text: "一起长大的约定", At: time.Second}}}) {
		t.Fatal("Chinese player cache was unnecessarily rejected")
	}
	if fusionCacheUsable(lyrics.Cached{Source: "QQ 音乐 · Lyricify", Lines: []lyrics.Line{{Text: "Instrumental", At: time.Second}}}) {
		t.Fatal("untranslated foreign Fusion cache blocked translation lookup")
	}
	if !fusionCacheUsable(lyrics.Cached{Source: "播放器歌词", Lines: []lyrics.Line{{Text: "Koko soko", Translation: "无所不在", At: time.Second}}}) {
		t.Fatal("translated player cache was rejected")
	}
}

// When the Fusion helper lacks a translation for a foreign song, NetEase translation supplies it.
func TestFusionNetEaseTranslationFallback(t *testing.T) {
	oldFetch, oldNetEase := fetchFusion, neteaseTranslation
	t.Cleanup(func() { fetchFusion, neteaseTranslation = oldFetch, oldNetEase })

	fetchFusion = func(context.Context, string, fusion.Request, bool) (fusion.Result, error) {
		return fusion.Result{}, errors.New("not found")
	}
	neteaseTranslation = func(spotify.Track, []lyrics.Line) (string, string, error) {
		return "[00:01.00]Just want ten minutes", "[00:01.00]只需要十分钟", nil
	}

	track := spotify.Track{ID: "10min", Name: "10 Minutes"}
	result, err := fetchFusionWithTranslationFallback(context.Background(), "", track, fusion.Request{Title: track.Name}, false)
	if err != nil || result.Source != "网易云 · 翻译" || !hasFusionTranslation(result.Lines) {
		t.Fatalf("NetEase translation fallback failed: result=%+v err=%v", result, err)
	}
}

// Bilingual Fusion lyrics must update the player's in-memory lyrics so Now Playing stays in sync with the island.
func TestFusionSyncsBilingualLyricsToPlayer(t *testing.T) {
	application := newTestApp()
	application.pl.Cue([]spotify.Track{{ID: "kd", Name: "Korean Dream"}})
	application.pl.SetLyrics([]lyrics.Line{{Text: "Nae mamdaero hallaeyo", At: time.Second, End: 5 * time.Second}})
	key := fusionTrackKey(application.pl.Current())
	application.fusion.key, application.fusion.generation = key, 1
	bilingual := []lyrics.Line{{Text: "내 맘대로 할래요", Translation: "随我心意去做", At: time.Second, End: 5 * time.Second}}
	application.receiveFusion(key, 1, fusion.Result{Source: "QQ 音乐 · Lyricify", Lines: bilingual}, nil)
	got, loaded := application.pl.Lyrics()
	if !loaded || len(got) != 1 || got[0].Text != "내 맘대로 할래요" || got[0].Translation != "随我心意去做" || len(got[0].Words) != 0 {
		t.Fatalf("Now Playing lyrics did not sync with Fusion: loaded=%v got=%+v", loaded, got)
	}
}

func TestShouldSyncFusionLyricsToPlayerGuards(t *testing.T) {
	translated := []lyrics.Line{{Text: "hello", Translation: "你好", At: time.Second}}
	untranslated := []lyrics.Line{{Text: "hello", At: time.Second}}
	unsynced := []lyrics.Line{{Text: "hello", Unsynced: true}}
	if shouldSyncFusionLyricsToPlayer(translated, true, "LRCLIB", untranslated) {
		t.Fatal("untranslated Fusion result downgraded translated player lyrics")
	}
	if shouldSyncFusionLyricsToPlayer(untranslated, true, "LRCLIB", unsynced) {
		t.Fatal("unsynced Fusion result downgraded synced player lyrics")
	}
	if shouldSyncFusionLyricsToPlayer(untranslated, true, "QQ 音乐", []lyrics.Line{{Text: "纯音乐，请欣赏", At: time.Second}}) {
		t.Fatal("instrumental notice overwrote real lyrics")
	}
	if !shouldSyncFusionLyricsToPlayer(unsynced, true, "QQ 音乐", untranslated) {
		t.Fatal("synced Fusion result did not upgrade unsynced player lyrics")
	}
}
