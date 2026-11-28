package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

func TestNetEaseTrackMatchingAndPlaceholderRevival(t *testing.T) {
	dir := t.TempDir()
	localFile := filepath.Join(dir, "qingtian.flac")
	revivedFile := filepath.Join(dir, "rare.flac")
	for _, f := range []string{localFile, revivedFile} {
		if err := os.WriteFile(f, []byte("flac"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	prevSearch := searchSpotifyForNetEase
	searchSpotifyForNetEase = func(q string, limit int) ([]spotify.Track, error) {
		if strings.Contains(q, "稻香") {
			return []spotify.Track{
				{ID: "sp-dj", Name: "稻香 (DJ版)", Artists: []spotify.Artist{{Name: "周杰伦"}}, Duration: 223000},
				{ID: "sp-daoxiang", Name: "稻香", Artists: []spotify.Artist{{Name: "周杰倫"}}, Album: spotify.Album{Name: "魔杰座"}, Duration: 223100},
			}, nil
		}
		return nil, nil
	}
	defer func() { searchSpotifyForNetEase = prevSearch }()

	localTracks := []spotify.Track{{
		ID:        "local:qingtian",
		LocalPath: localFile,
		Name:      "晴天",
		Artists:   []spotify.Artist{{Name: "周杰伦"}},
		Duration:  269000,
	}}

	items := []spotify.NetEasePlaylistTrack{
		{ID: 1, Name: "晴天", Artists: []string{"周杰伦"}, Album: "叶惠美", Duration: 269200},
		{ID: 2, Name: "稻香", Artists: []string{"周杰伦"}, Album: "魔杰座", Duration: 223000},
		{ID: 3, Name: "网易云冷门独享曲", Artists: []string{"独立乐队"}, Album: "Demo", Duration: 195000},
	}

	matched := make([]spotify.Track, len(items))
	var lHits, sHits, uHits int
	for i, it := range items {
		tr, kind := matchNetEaseTrack(context.Background(), it, localTracks, true)
		matched[i] = tr
		switch kind {
		case neteaseMatchLocal:
			lHits++
		case neteaseMatchSpotify:
			sHits++
		default:
			uHits++
		}
	}
	if lHits != 1 || sHits != 1 || uHits != 1 {
		t.Fatalf("match counts = local:%d spotify:%d unmatched:%d, want 1/1/1", lHits, sHits, uHits)
	}
	if matched[0].ID != "local:qingtian" || matched[1].ID != "sp-daoxiang" || matched[2].ID != "netease:3" {
		t.Fatalf("matched IDs = [%s, %s, %s]", matched[0].ID, matched[1].ID, matched[2].ID)
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.localTracks = localTracks
	a.finishNetEaseImport("", 3778678, "我的网易云搬家", matched, false, lHits, sHits, uHits)

	if len(a.hybrids) != 1 || a.hybrids[0].Name != "我的网易云搬家" {
		t.Fatalf("expected new hybrid playlist, got %+v", a.hybrids)
	}
	pid := a.hybrids[0].ID
	pl := a.playlists[pid]
	if countPlayableTracks(pl.Tracks) != 2 {
		t.Fatalf("playable tracks = %d, want 2", countPlayableTracks(pl.Tracks))
	}
	if pl.Tracks[2].Playable() || pl.Tracks[2].LocalError != "未匹配到音源" {
		t.Fatalf("unmatched placeholder = playable:%v err:%q", pl.Tracks[2].Playable(), pl.Tracks[2].LocalError)
	}

	// Adding the rare track into the local library automatically revives the unmatched placeholder.
	a.applyScannedTracks([]spotify.Track{
		localTracks[0],
		{ID: "local:rare", LocalPath: revivedFile, Name: "网易云冷门独享曲", Artists: []spotify.Artist{{Name: "独立乐队"}}, Duration: 195000},
	}, nil, true)
	pl = a.playlists[pid]
	if countPlayableTracks(pl.Tracks) != 3 || pl.Tracks[2].ID != "local:rare" {
		t.Fatalf("placeholder did not revive after local scan: playable=%d track=%+v", countPlayableTracks(pl.Tracks), pl.Tracks[2])
	}
}

func TestNetEaseImportDialogRendersAndOpensFromSidebar(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.setNetEaseAccount(8888, "测试用户", "MUSIC_U=demo")
	a.netease.playlists = []spotify.NetEasePlaylistSummary{
		{ID: 123456, Name: "深夜单曲循环", TrackCount: 42},
	}

	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("从网易云导入歌单…"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !a.netease.open {
		t.Fatal("netease import dialog did not open")
	}
	if !tester.HasText("我的网易云歌单 · 测试用户") || !tester.HasText("深夜单曲循环") {
		t.Fatal("dialog missing logged-in NetEase account or playlist row")
	}
}
