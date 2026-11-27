package lyrics

import "testing"

// Manual import/re-search uses SaveCachedForce to overwrite a previously cached lyric even with a lower heuristic score.
func TestSaveCachedForceOverwritesHigherScoreCache(t *testing.T) {
	dir := t.TempDir()
	key := "spotify:track:test-force"
	highScore := []Line{{Text: "Wrong Song", Translation: "错配翻译", Timed: true}}
	manual := []Line{{Text: "Right Song", Translation: "正确翻译", Timed: false}}
	SaveCached(dir, key, "old", highScore)
	SaveCached(dir, key, "auto-lower", manual)
	if got := LoadCached(dir, key); got.Source != "old" {
		t.Fatalf("non-force save should keep higher-score cache, got %q", got.Source)
	}
	SaveCachedForce(dir, key, "manual", manual)
	if got := LoadCached(dir, key); got.Source != "manual" || len(got.Lines) != 1 || got.Lines[0].Text != "Right Song" {
		t.Fatalf("force save did not overwrite stale cache: %+v", got)
	}
}
