package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func TestNetEaseDirectImportAndPlaybackFallback(t *testing.T) {
	dir := t.TempDir()
	localFile := filepath.Join(dir, "qingtian.flac")
	if err := os.WriteFile(localFile, []byte("flac"), 0o600); err != nil {
		t.Fatal(err)
	}

	prevSongMedia := fetchNetEaseSongMedia
	prevSearch := searchSpotifyForNetEase
	prevResolve := resolveSpotifyForNetEase
	defer func() {
		fetchNetEaseSongMedia = prevSongMedia
		searchSpotifyForNetEase = prevSearch
		resolveSpotifyForNetEase = prevResolve
		spotify.SetLocalTracks(nil)
	}()

	fetchNetEaseSongMedia = func(ctx context.Context, cookie string, songID int64) (*spotify.Media, error) {
		switch songID {
		case 1:
			return &spotify.Media{File: "http://m701.music.126.net/free.mp3", Duration: 210 * time.Second}, nil
		case 2:
			return nil, spotify.ErrNetEaseTrialOnly
		case 3:
			return nil, spotify.ErrNetEaseUnavailable
		default:
			return nil, spotify.ErrNetEaseUnavailable
		}
	}
	searchSpotifyForNetEase = func(q string, limit int) ([]spotify.Track, error) {
		if strings.Contains(q, "稻香") {
			return []spotify.Track{
				{ID: "sp-dj", Name: "稻香 (DJ版)", Artists: []spotify.Artist{{Name: "周杰伦"}}, Duration: 223000},
				{ID: "sp-daoxiang", Name: "稻香", Artists: []spotify.Artist{{Name: "周杰倫"}}, Album: spotify.Album{Name: "魔杰座"}, Duration: 223100},
			}, nil
		}
		return nil, nil
	}
	var resolvedURIs []string
	resolveSpotifyForNetEase = func(uri string) (*spotify.Media, error) {
		resolvedURIs = append(resolvedURIs, uri)
		return &spotify.Media{Manifest: "#EXTM3U", Duration: 223 * time.Second}, nil
	}

	spotify.SetLocalTracks([]spotify.Track{{
		ID:        "local:qingtian",
		LocalPath: localFile,
		Name:      "晴天",
		Artists:   []spotify.Artist{{Name: "周杰伦"}},
		Duration:  269000,
	}})

	items := []spotify.NetEasePlaylistTrack{
		{ID: 1, Name: "海屿你", Artists: []string{"马也_Crabbit"}, Album: "海屿你", Duration: 210000},
		{ID: 2, Name: "稻香", Artists: []string{"周杰伦"}, Album: "魔杰座", Duration: 223000},
		{ID: 3, Name: "晴天", Artists: []string{"周杰伦"}, Album: "叶惠美", Duration: 269200},
	}
	tracks := make([]spotify.Track, len(items))
	for i, it := range items {
		tracks[i] = it.ToTrack()
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.finishNetEaseImport("", 3778678, "网易云热歌榜", tracks, false)

	if len(a.hybrids) != 1 || a.hybrids[0].Name != "网易云热歌榜" {
		t.Fatalf("expected new hybrid playlist, got %+v", a.hybrids)
	}
	pl := a.playlists[a.hybrids[0].ID]
	if countPlayableTracks(pl.Tracks) != 3 {
		t.Fatalf("all direct NetEase tracks should be playable, got %d", countPlayableTracks(pl.Tracks))
	}
	if !pl.Tracks[0].NetEase() || !pl.Tracks[1].NetEase() || !pl.Tracks[2].NetEase() {
		t.Fatalf("tracks should remain native NetEase tracks: %+v", pl.Tracks)
	}

	// 1. Default playback uses NetEase audio stream directly.
	m1, err := a.resolveNetEasePlayback(context.Background(), pl.Tracks[0])
	if err != nil || m1.File != "http://m701.music.126.net/free.mp3" {
		t.Fatalf("track 1 should play via NetEase directly, got (%+v, %v)", m1, err)
	}

	// 2. Trial-only NetEase track falls back seamlessly to Spotify.
	m2, err := a.resolveNetEasePlayback(context.Background(), pl.Tracks[1])
	if err != nil || m2.Manifest != "#EXTM3U" || len(resolvedURIs) != 1 || resolvedURIs[0] != "spotify:track:sp-daoxiang" {
		t.Fatalf("track 2 should fall back to Spotify, got (%+v, %v, uris=%v)", m2, err, resolvedURIs)
	}

	// 3. Unavailable NetEase track with a matching local file plays locally.
	m3, err := a.resolveNetEasePlayback(context.Background(), pl.Tracks[2])
	if err != nil || m3.LocalPath != localFile {
		t.Fatalf("track 3 should fall back to local file, got (%+v, %v)", m3, err)
	}
}

func TestNetEaseSidebarSectionSettingsAndHybridJoin(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.setNetEaseAccount(8888, "测试用户", "MUSIC_U=demo")
	a.netease.playlists = []spotify.NetEasePlaylistSummary{
		{ID: 123456, Name: "深夜单曲循环", TrackCount: 42},
	}
	a.rebuildNetEaseLists()

	// Dedicated "网易云歌单" section renders in the sidebar and opens the playlist page.
	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if !tester.HasText("网易云歌单") || !tester.HasText("深夜单曲循环") {
		t.Fatal("sidebar missing dedicated 网易云歌单 section or playlist row")
	}

	// Clicking '+' opens the NetEase dialog; Settings page shows the NetEase account row.
	if err := tester.Click("添加网易云歌单"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !a.netease.open || !tester.HasText("添加网易云歌单") || !tester.HasText("我的网易云歌单 · 测试用户") {
		t.Fatal("netease dialog did not open cleanly from sidebar '+'")
	}
	a.netease.open = false
	a.route = route{page: pageSettings}
	tester.Frame()
	if !tester.HasText("测试用户") || !tester.HasText("网易云音乐 · 已同步 1 个歌单") || !tester.HasText("刷新歌单") {
		t.Fatal("settings account section missing logged-in NetEase row")
	}

	// Adding a NetEase playlist into a hybrid playlist works directly.
	neID := spotify.NetEasePlaylistID(123456)
	a.playlists[neID] = &spotify.Playlist{
		ID:   neID,
		Name: "深夜单曲循环",
		Tracks: []spotify.Track{
			(spotify.NetEasePlaylistTrack{ID: 777, Name: "夜曲", Artists: []string{"周杰伦"}, Duration: 226000}).ToTrack(),
		},
	}
	hyb := a.createHybridPlaylist("混音测试", nil)
	a.importPlaylistIntoHybrid(neID, hyb.ID)
	got := a.playlists[hyb.ID]
	if len(got.Tracks) != 1 || got.Tracks[0].ID != "netease:777" || !got.Tracks[0].Playable() {
		t.Fatalf("hybrid playlist tracks after importing NetEase playlist = %+v", got.Tracks)
	}

	// Importing the same song from Spotify upgrades the NetEase copy (Spotify > local > NetEase);
	// importing a lower-priority NetEase copy of an existing Spotify song skips it.
	added, skipped := a.addManyToHybrid(hyb.ID, []spotify.Track{
		{ID: "sp-yequ", Name: "夜曲", Artists: []spotify.Artist{{Name: "周杰倫"}}, Duration: 226000},
		{ID: "sp-qingtian", Name: "晴天", Artists: []spotify.Artist{{Name: "周杰伦"}}, Duration: 269000},
	})
	if added != 1 || skipped != 0 {
		t.Fatalf("priority upgrade addManyToHybrid = added:%d skipped:%d, want 1/0", added, skipped)
	}
	if a.playlists[hyb.ID].Tracks[0].ID != "sp-yequ" {
		t.Fatalf("expected Spotify to upgrade NetEase duplicate, got %+v", a.playlists[hyb.ID].Tracks[0])
	}
	added2, skipped2 := a.addManyToHybrid(hyb.ID, []spotify.Track{
		(spotify.NetEasePlaylistTrack{ID: 888, Name: "晴天", Artists: []string{"周杰伦"}, Duration: 269000}).ToTrack(),
	})
	if added2 != 0 || skipped2 != 1 {
		t.Fatalf("lower-priority NetEase duplicate should be skipped, got added:%d skipped:%d", added2, skipped2)
	}
}
