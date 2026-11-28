package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/spotify"
)

func TestHybridPlaylistCRUDAndPersistence(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "hybrid-playlists.json")
	localFile := filepath.Join(dir, "live.flac")
	if err := os.WriteFile(localFile, []byte("flac"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.hybridStore = loadHybridPlaylistsFrom(storePath)

	spTrack := spotify.Track{
		ID:       "sp001",
		Name:     "Studio Version",
		Artists:  []spotify.Artist{{Name: "Artist A"}},
		Album:    spotify.Album{Name: "Album A", PicURL: "https://example.com/a.jpg"},
		Duration: 210000,
	}
	localTrack := spotify.Track{
		ID:        "local:abc123",
		LocalPath: localFile,
		Name:      "Live Bootleg",
		Artists:   []spotify.Artist{{Name: "Artist A"}},
		Duration:  250000,
	}
	cloudTrack := spotify.Track{
		ID:          "cloud:xyz789",
		CloudURL:    "https://dav.example.com/concert.flac",
		CloudSource: "群晖 NAS",
		Name:        "Concert Encore",
		Artists:     []spotify.Artist{{Name: "Artist B"}},
		Duration:    300000,
	}

	pl := a.createHybridPlaylist("我的珍藏现场", &spTrack)
	if pl == nil || !pl.Hybrid() {
		t.Fatalf("expected hybrid playlist, got %+v", pl)
	}
	pid := pl.ID
	a.addToHybrid(pid, &localTrack)
	a.addToHybrid(pid, &cloudTrack)
	// Duplicate add should be ignored.
	a.addToHybrid(pid, &localTrack)

	got := a.playlists[pid]
	if len(got.Tracks) != 3 {
		t.Fatalf("tracks = %d, want 3", len(got.Tracks))
	}
	if got.CoverURL != spTrack.Album.PicURL {
		t.Fatalf("cover = %q, want %q", got.CoverURL, spTrack.Album.PicURL)
	}

	// Move last track up by one.
	a.moveHybridTrack(pid, got.Tracks[2].UID, -1)
	got = a.playlists[pid]
	if got.Tracks[1].ID != cloudTrack.ID || got.Tracks[2].ID != localTrack.ID {
		t.Fatalf("order after move = [%s, %s, %s]", got.Tracks[0].ID, got.Tracks[1].ID, got.Tracks[2].ID)
	}

	// Rename & pin.
	a.renameHybridPlaylist(pid, "演唱会合集")
	a.togglePlaylistPinned(pid)
	if !a.isPlaylistPinned(pid) || len(a.pinnedPlaylists()) != 1 {
		t.Fatalf("hybrid playlist should support pinning")
	}

	// Reload in a brand new signed-out app instance.
	b := newTestApp()
	b.profile = nil
	b.hybridStore = loadHybridPlaylistsFrom(storePath)
	b.refreshHybridPlaylists()
	reloaded := b.playlists[pid]
	if reloaded == nil || reloaded.Name != "演唱会合集" || len(reloaded.Tracks) != 3 {
		t.Fatalf("reloaded hybrid playlist mismatch: %+v", reloaded)
	}
	// Signed out: Spotify track is unplayable, local track stays playable.
	if reloaded.Tracks[0].Playable() || reloaded.Tracks[0].LocalError != "需登录 Spotify" {
		t.Fatalf("spotify track while signed out = playable:%v err:%q", reloaded.Tracks[0].Playable(), reloaded.Tracks[0].LocalError)
	}
	if !reloaded.Tracks[2].Playable() {
		t.Fatalf("local track should stay playable while signed out: %+v", reloaded.Tracks[2])
	}

	// Remove one track and delete playlist.
	b.removeFromHybrid(pid, reloaded.Tracks[1].UID)
	if len(b.playlists[pid].Tracks) != 2 {
		t.Fatalf("tracks after remove = %d, want 2", len(b.playlists[pid].Tracks))
	}
	b.deleteHybridPlaylist(pid)
	if b.playlists[pid] != nil || len(b.hybrids) != 0 {
		t.Fatalf("playlist was not deleted")
	}
	c := loadHybridPlaylistsFrom(storePath)
	if len(c.entries) != 0 {
		t.Fatalf("deleted playlist remained on disk: %d", len(c.entries))
	}
}

func TestHybridPlaylistHydrationAndOfflinePlayback(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cloud-audio")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	localmusic.SetCloudCacheDir(cacheDir)
	t.Cleanup(func() { localmusic.SetCloudCacheDir("") })

	cachedCloudID := "cloud:cached-track"
	// Create matching cached audio file using localmusic's cache layout? Let's test both
	// offline-uncached cloud track and local file reconnection.
	movedFile := filepath.Join(dir, "moved", "acoustic.flac")
	if err := os.MkdirAll(filepath.Dir(movedFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(movedFile, []byte("flac"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.profile = nil // signed out
	a.cloudOffline = true

	pl := a.createHybridPlaylist("离线测试", nil)
	a.addToHybrid(pl.ID, &spotify.Track{ID: "sp1", Name: "Spotify Track", Duration: 180000})
	a.addToHybrid(pl.ID, &spotify.Track{ID: cachedCloudID, CloudURL: "https://dav/a.flac", CloudSource: "NAS", Name: "Cloud Track", Duration: 180000})
	a.addToHybrid(pl.ID, &spotify.Track{
		ID:        "local:old-path-hash",
		LocalPath: filepath.Join(dir, "old", "acoustic.flac"),
		Name:      "Acoustic Demo",
		Artists:   []spotify.Artist{{Name: "Indie Band"}},
		Duration:  180000,
	})

	hydrated := a.playlists[pl.ID]
	if countPlayableTracks(hydrated.Tracks) != 0 {
		t.Fatalf("expected 0 playable tracks before local scan, got %d", countPlayableTracks(hydrated.Tracks))
	}
	if hydrated.Tracks[1].LocalError != "云盘离线且未缓存" {
		t.Fatalf("cloud offline err = %q", hydrated.Tracks[1].LocalError)
	}
	if hydrated.Tracks[2].LocalError != "本地文件缺失" {
		t.Fatalf("missing local err = %q", hydrated.Tracks[2].LocalError)
	}

	// Playing when nothing is playable should show a toast and not crash or prompt sign-in.
	a.playPlaylist(hydrated)
	if a.toast != "当前没有可播放的歌曲" || a.pl.Current() != nil {
		t.Fatalf("expected unplayable toast, got toast=%q current=%v", a.toast, a.pl.Current())
	}

	// Simulate scanning the new folder where "Acoustic Demo" moved to while cloud remains offline:
	// hydration reconnects the moved local file while keeping the uncached cloud track unavailable.
	a.applyScannedTracks([]spotify.Track{{
		ID:        "local:new-path-hash",
		LocalPath: movedFile,
		Name:      "Acoustic Demo",
		Artists:   []spotify.Artist{{Name: "Indie Band"}},
		Duration:  180000,
	}}, []string{"云盘刷新失败，当前显示离线曲库"}, true)

	hydrated = a.playlists[pl.ID]
	if countPlayableTracks(hydrated.Tracks) != 1 || !hydrated.Tracks[2].Playable() {
		t.Fatalf("expected reconnected local track to be playable: %+v", hydrated.Tracks[2])
	}
	if hydrated.Tracks[2].LocalPath != movedFile {
		t.Fatalf("reconnected path = %q, want %q", hydrated.Tracks[2].LocalPath, movedFile)
	}

	// Play playlist while still signed out: should skip unplayable Spotify & offline cloud tracks and play only the playable local track.
	a.playPlaylist(hydrated)
	if cur := a.pl.Current(); cur == nil || cur.ID != "local:new-path-hash" {
		t.Fatalf("playing track = %+v, want reconnected local track", cur)
	}
	if q := a.pl.Queue(); len(q) != 1 {
		t.Fatalf("queued tracks = %d, want 1 playable track", len(q))
	}
}

func TestHybridPlaylistSidebarAndPageUI(t *testing.T) {
	dir := t.TempDir()
	localFile := filepath.Join(dir, "demo.flac")
	if err := os.WriteFile(localFile, []byte("flac"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.profile = nil // works signed out too

	pl := a.createHybridPlaylist("通勤混播", &spotify.Track{
		ID:        "local:demo",
		LocalPath: localFile,
		Name:      "Demo Track",
		Artists:   []spotify.Artist{{Name: "Local Artist"}},
		Duration:  120000,
	})
	a.addToHybrid(pl.ID, &spotify.Track{
		ID:       "sp-demo",
		Name:     "Spotify Song",
		Artists:  []spotify.Artist{{Name: "SP Artist"}},
		Duration: 150000,
	})

	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if a.route.page != pagePlaylist || a.route.playlist != pl.ID {
		t.Fatalf("route = %+v, want hybrid playlist page", a.route)
	}

	// Signing out/in preserves hybrid playlists in a.playlists and sidebar.
	a.signedIn(&spotify.Profile{UserID: "u1", Nickname: "User"})
	tester.Frame()
	if a.playlists[pl.ID] == nil || len(a.hybrids) != 1 {
		t.Fatalf("hybrid playlist lost on sign-in")
	}
	if !a.playlists[pl.ID].Tracks[1].Playable() {
		t.Fatalf("spotify track should become playable after sign-in")
	}

	// Verify local & cloud tracks expose the hybrid playlist submenu even on the local music page.
	a.localTracks = []spotify.Track{
		{ID: "local:extra", LocalPath: localFile, Name: "Extra Local", Artists: []spotify.Artist{{Name: "Local"}}},
		{ID: "cloud:extra", CloudURL: "https://dav.example.com/x.flac", CloudSource: "Alist", Name: "Extra Cloud", Artists: []spotify.Artist{{Name: "Cloud"}}},
	}
	a.localLoaded = true
	a.open(route{page: pageLocal})
	tester.Frame()
	if err := tester.RightClick("Extra Cloud"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("添加到混合歌单", "通勤混播"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if len(a.playlists[pl.ID].Tracks) != 3 {
		t.Fatalf("expected cloud track added via context menu, got %d tracks", len(a.playlists[pl.ID].Tracks))
	}
}

func TestHybridPlaylistWholeImportShuffleOrderAndShuffledPlayback(t *testing.T) {
	dir := t.TempDir()
	storePath := filepath.Join(dir, "hybrid-playlists.json")
	localFile := filepath.Join(dir, "local.flac")
	if err := os.WriteFile(localFile, []byte("flac"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.hybridStore = loadHybridPlaylistsFrom(storePath)

	spPlaylistID := "spotify:playlist:orig-1"
	spTracks := []spotify.Track{
		{ID: "sp-1", Name: "SP One", Artists: []spotify.Artist{{Name: "A"}}, Duration: 180000},
		{ID: "sp-2", Name: "SP Two", Artists: []spotify.Artist{{Name: "B"}}, Duration: 180000},
		{ID: "sp-3", Name: "SP Three", Artists: []spotify.Artist{{Name: "C"}}, Duration: 180000},
	}
	a.mine = []spotify.Playlist{{ID: spPlaylistID, Name: "我的原有歌单", TrackCount: len(spTracks)}}
	a.playlists[spPlaylistID] = &spotify.Playlist{ID: spPlaylistID, Name: "我的原有歌单", Tracks: spTracks}
	a.localTracks = []spotify.Track{
		{ID: "local:1", LocalPath: localFile, Name: "Local One", Artists: []spotify.Artist{{Name: "L1"}}, Duration: 200000},
		{ID: "local:2", LocalPath: localFile, Name: "Local Two", Artists: []spotify.Artist{{Name: "L2"}}, Duration: 200000},
		{ID: "cloud:1", CloudURL: "https://dav.example.com/c1.flac", CloudSource: "NAS", Name: "Cloud One", Artists: []spotify.Artist{{Name: "C1"}}, Duration: 220000},
	}

	pl := a.createHybridPlaylist("混排大合集", nil)

	// Import whole Spotify playlist and whole local+cloud library via UI header menu.
	tester := ui.NewTester(a.view, 1180, 780)
	tester.Frame()
	if err := tester.Click("导入歌单"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if err := tester.ChooseMenuItem("我的歌单", "我的原有歌单"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if got := len(a.playlists[pl.ID].Tracks); got != 3 {
		t.Fatalf("tracks after importing spotify playlist = %d, want 3", got)
	}

	// Re-importing the same playlist skips all duplicates cleanly.
	a.importPlaylistIntoHybrid(spPlaylistID, pl.ID)
	if got := len(a.playlists[pl.ID].Tracks); got != 3 {
		t.Fatalf("duplicate import changed track count to %d", got)
	}

	// Import all local + cloud tracks.
	a.importLocalScopeIntoHybrid(pl.ID, 0)
	if got := len(a.playlists[pl.ID].Tracks); got != 6 {
		t.Fatalf("tracks after importing local/cloud = %d, want 6", got)
	}

	beforeIDs := make([]string, 6)
	for i, tr := range a.playlists[pl.ID].Tracks {
		beforeIDs[i] = tr.ID
	}

	// Click "打乱顺序" button in header: order must visibly change and persist to disk.
	tester.Frame()
	if err := tester.Click("打乱顺序"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()

	afterIDs := make([]string, 6)
	same := true
	for i, tr := range a.playlists[pl.ID].Tracks {
		afterIDs[i] = tr.ID
		if afterIDs[i] != beforeIDs[i] {
			same = false
		}
	}
	if same {
		t.Fatalf("shuffle order did not change track order: %v", afterIDs)
	}

	// Reload from disk and verify shuffled order persisted.
	reloaded := loadHybridPlaylistsFrom(storePath)
	if len(reloaded.entries) != 1 || len(reloaded.entries[0].Tracks) != 6 {
		t.Fatalf("reloaded hybrid store mismatch")
	}
	for i, rec := range reloaded.entries[0].Tracks {
		if rec.Track.ID != afterIDs[i] {
			t.Fatalf("persisted order mismatch at %d: got %s want %s", i, rec.Track.ID, afterIDs[i])
		}
	}

	// Click primary "随机混播" button: queues all 6 playable tracks and produces a shuffled order across runs.
	if err := tester.Click("随机混播"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	q := a.pl.Queue()
	if len(q) != 6 {
		t.Fatalf("shuffled playback queue len = %d, want 6", len(q))
	}
	sawDifferent := false
	for range 12 {
		a.playPlaylist(a.playlists[pl.ID])
		q2 := a.pl.Queue()
		for i := range q2 {
			if q2[i].ID != afterIDs[i] {
				sawDifferent = true
				break
			}
		}
		if sawDifferent {
			break
		}
	}
	if !sawDifferent {
		t.Fatalf("playPlaylist on hybrid playlist never produced a shuffled order distinct from stored order")
	}
}
