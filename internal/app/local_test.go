package app

import (
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

// Clicking a local track starts its queue signed out, without asking for Spotify login.
func TestLocalPagePlaysSignedOut(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile, a.localLoaded = nil, true
	a.localTracks = []spotify.Track{{ID: "local:one", LocalPath: "song.mp3", Name: "本地测试歌曲", Duration: 180000}}
	a.route = route{page: pageLocal}
	previous := askSignIn
	defer func() { askSignIn = previous }()
	askSignIn = func(*app) { t.Error("local playback asked to sign in") }
	tester := ui.NewTester(a.view, 1200, 800)
	tester.Frame()
	if err := tester.Click("本地测试歌曲"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("本地测试歌曲"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.pl.Current() == nil || a.pl.Current().LocalPath != "song.mp3" {
		t.Fatal("click did not select local track")
	}
}

// Connect updates cannot replace local playback; choosing an online queue enables Connect again.
func TestLocalConnectIsolation(t *testing.T) {
	a := testConnectApp(t)
	local := spotify.Track{ID: "local:one", LocalPath: "song.mp3"}
	if a.connectControl("play", player.QueueRequest{Tracks: []spotify.Track{local}}) {
		t.Fatal("sent a local path to Spotify")
	}
	a.pl.Cue([]spotify.Track{local})
	a.connectEvent(spotify.ConnectEvent{Kind: "cluster", Data: json.RawMessage(`{"active_device_id":"phone","player_state":{"track":{"uri":"spotify:track:remote"}}}`)})
	if a.pl.Current().ID != local.ID || a.pl.IsRemote() {
		t.Fatal("remote update replaced local playback")
	}
	if a.connectControl("seek", nil) || len(a.connect.jobs) != 0 {
		t.Fatal("local controls went through Connect")
	}
	if !a.connectControl("play", player.QueueRequest{Tracks: []spotify.Track{{ID: "online"}}}) || a.connect.local || len(a.connect.jobs) != 1 {
		t.Fatal("online playback did not return to Connect")
	}
}

func TestLocalSearchChineseVariants(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile, a.localLoaded = nil, true
	a.localTracks = []spotify.Track{
		{ID: "local:1", LocalPath: "a.mp3", Name: "愛與夢", Artists: []spotify.Artist{{Name: "周杰倫"}}, Album: spotify.Album{Name: "葉惠美"}, Duration: 180000},
		{ID: "local:2", LocalPath: "b.mp3", Name: "晴天", Artists: []spotify.Artist{{Name: "周杰伦"}}, Album: spotify.Album{Name: "叶惠美"}, Duration: 180000},
	}
	a.route = route{page: pageLocal}
	a.localQuery = "爱与梦 周杰伦"
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if !tt.HasText("愛與夢") || tt.HasText("晴天") {
		t.Fatalf("simplified local search failed: match=%v, unmatched=%v", tt.HasText("愛與夢"), tt.HasText("晴天"))
	}
}

func TestLocalSearchCapsuleAndFullQueuePlayback(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile, a.localLoaded = nil, true
	a.localTracks = []spotify.Track{
		{ID: "local:1", LocalPath: "a.mp3", Name: "第一首", Artists: []spotify.Artist{{Name: "歌手A"}}, Duration: 180000},
		{ID: "local:2", LocalPath: "b.mp3", Name: "回音(《天师钟馗》电视剧片尾曲)", Artists: []spotify.Artist{{Name: "周定纬"}}, Album: spotify.Album{Name: "钟馗传说 电视原声带"}, Duration: 151000},
		{ID: "local:3", LocalPath: "c.mp3", Name: "第三首", Artists: []spotify.Artist{{Name: "歌手C"}}, Duration: 180000},
	}
	a.route = route{page: pageLocal}
	tester := ui.NewTester(a.view, 1200, 800)
	tester.Frame()
	if err := tester.Click("筛选本地音乐"); err != nil {
		t.Fatal(err)
	}
	tester.Type("回音")
	tester.Frame()
	tester.Frame()
	if a.localQuery != "回音" || !tester.HasText("找到 1 首") {
		t.Fatalf("local search query=%q", a.localQuery)
	}
	for range 2 {
		if err := tester.Click("回音(《天师钟馗》电视剧片尾曲)"); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	if a.pl.Current() == nil || a.pl.Current().ID != "local:2" || len(a.pl.Queue()) != 3 {
		t.Fatalf("filtered local playback did not preserve full local queue: cur=%v len=%d", a.pl.Current(), len(a.pl.Queue()))
	}
	if err := tester.Click("清除搜索"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if a.localQuery != "" {
		t.Fatal("clear button did not clear local search")
	}
}

// Scrolling local music pins the search toolbar at the top, clips scrolled rows behind it, and returns below the hero when scrolled back.
func TestLocalToolbarPinsAndClipsWhenScrolled(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile, a.localLoaded = nil, true
	for i := range 60 {
		a.localTracks = append(a.localTracks, spotify.Track{
			ID:        fmt.Sprintf("local:%d", i),
			LocalPath: fmt.Sprintf("song%d.mp3", i),
			Name:      fmt.Sprintf("本地歌曲%d", i),
			Duration:  200000,
		})
	}
	a.route = route{page: pageLocal}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	before, ok := tt.Find("筛选本地音乐")
	if !ok {
		t.Fatal("local search is missing before scrolling")
	}
	scroll := func(delta float32) ui.Rect {
		t.Helper()
		tt.Scroll(900, 550, 0, delta)
		tt.Advance(time.Second)
		tt.Frame()
		r, ok := tt.Find("筛选本地音乐")
		if !ok {
			t.Fatal("local search left the viewport")
		}
		return r
	}
	pinned := scroll(320)
	if pinned.Y >= before.Y-100 {
		t.Fatalf("local toolbar did not pin at top: before=%v, after=%v", before, pinned)
	}
	for range 2 {
		if err := tt.Click("筛选本地音乐"); err != nil {
			t.Fatalf("pinned local search was not clickable: %v", err)
		}
	}
	if a.pl.Current() != nil {
		t.Fatalf("clicking pinned local search hit a track underneath: %+v", a.pl.Current())
	}
	// Verify the sort control stays aligned on the same pinned toolbar row.
	bar, ok := tt.Find("本地音乐排序")
	if !ok {
		t.Fatal("local sort control missing after scrolling")
	}
	if bar.Y+bar.H < pinned.Y+pinned.H {
		t.Fatalf("local sort control misaligned with search: search=%v sort=%v", pinned, bar)
	}
	if again := scroll(1600); again.Y != pinned.Y {
		t.Fatalf("local toolbar moved after pinning: %v to %v", pinned, again)
	}
	if back := scroll(-100000); back.Y != before.Y {
		t.Fatalf("local toolbar did not return below the hero: %v to %v", before, back)
	}
}

// Sorting local music, typing a space-containing query, and double-clicking a result keeps playback running and preserves the full sorted queue.
func TestLocalSortAndSearchPlayback(t *testing.T) {
	a := newTestApp()
	a.settings = defaultSettings()
	a.profile, a.localLoaded = nil, true
	a.localTracks = []spotify.Track{
		{ID: "local:c", LocalPath: "c.mp3", Name: "Zebra Song", Duration: 240000},
		{ID: "local:b", LocalPath: "b.mp3", Name: "Beta Song", Duration: 200000},
		{ID: "local:a", LocalPath: "a.mp3", Name: "Alpha Song", Duration: 180000},
	}
	a.pl.PlayQueue(a.localTracks, 0)
	a.route = route{page: pageLocal}
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	if err := tt.Click("本地音乐排序"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("歌曲名称"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.pl.Current().ID != "local:c" || !slices.Equal(playlistTrackIDs(a.pl.Queue()), []string{"local:c", "local:b", "local:a"}) {
		t.Fatal("sorting local music changed the active queue")
	}
	if err := tt.Click("筛选本地音乐"); err != nil {
		t.Fatal(err)
	}
	tt.Type("beta song")
	tt.Frame()
	tt.Frame()
	if a.localQuery != "beta song" || !a.pl.Playing() {
		t.Fatalf("typing space in local search changed playback or query=%q playing=%v", a.localQuery, a.pl.Playing())
	}
	for range 2 {
		if err := tt.Click("Beta Song"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if a.pl.Current() == nil || a.pl.Current().ID != "local:b" || !slices.Equal(playlistTrackIDs(a.pl.Queue()), []string{"local:a", "local:b", "local:c"}) {
		t.Fatalf("filtered local playback lost sorted queue: cur=%v queue=%v", a.pl.Current(), playlistTrackIDs(a.pl.Queue()))
	}
}

// Adding a WebDAV cloud source saves it, triggers cloud scanning, and allows filtering between local and cloud tracks.
func TestCloudSourceFormAddAndFilter(t *testing.T) {
	prevScan := scanCloudSources
	defer func() { scanCloudSources = prevScan }()

	scanned := make(chan []localmusic.CloudSource, 1)
	scanCloudSources = func(_ context.Context, sources []localmusic.CloudSource, _, _ string) ([]spotify.Track, []string) {
		scanned <- slices.Clone(sources)
		return []spotify.Track{{
			ID:          "cloud:demo",
			CloudURL:    "https://dav.example.com/dav/Music/CloudTrack.flac",
			CloudSource: "我的云盘",
			Name:        "Cloud Track",
			Duration:    195000,
		}}, nil
	}

	a := newTestApp()
	a.settings = defaultSettings()
	a.settings.path = filepath.Join(t.TempDir(), "settings.json")
	a.profile, a.localLoaded = nil, true
	a.route = route{page: pageLocal}

	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()

	if err := tt.Click("添加云盘"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.cloudFormOpen {
		t.Fatal("cloud form did not open")
	}

	if err := tt.Click("云盘名称"); err != nil {
		t.Fatal(err)
	}
	tt.Type("我的云盘")
	if err := tt.Click("WebDAV 地址"); err != nil {
		t.Fatal(err)
	}
	tt.Type("https://dav.example.com/dav/Music")
	tt.Frame()
	tt.Frame()

	if err := tt.Click("保存并扫描"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	select {
	case got := <-scanned:
		if len(got) != 1 || got[0].Name != "我的云盘" || got[0].URL != "https://dav.example.com/dav/Music" {
			t.Fatalf("unexpected scanned cloud sources: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("saving WebDAV cloud source did not trigger ScanCloud")
	}

	if a.cloudFormOpen || len(a.settings.CloudSources) != 1 {
		t.Fatalf("cloud source not saved cleanly: open=%v count=%d", a.cloudFormOpen, len(a.settings.CloudSources))
	}

	// Verify scope filtering between local and cloud tracks.
	a.localTracks = []spotify.Track{
		{ID: "local:one", LocalPath: "local.mp3", Name: "Local Track", Duration: 180000},
		{ID: "cloud:demo", CloudURL: "https://dav.example.com/dav/Music/CloudTrack.flac", CloudSource: "我的云盘", Name: "Cloud Track", Duration: 195000},
	}
	tt.Frame()
	if err := tt.Click("仅云盘"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.localScope != 2 {
		t.Fatalf("expected cloud scope 2, got %d", a.localScope)
	}
	filtered := a.filterLocalScope(a.orderedLocalTracks())
	if len(filtered) != 1 || !filtered[0].Cloud() {
		t.Fatalf("expected only cloud track, got %+v", filtered)
	}
}

// With NM_FRAMES set, saves screenshots of the Cloud Music Disk setup/folder picker and playlist track covers.
func TestCloudShowcaseFrames(t *testing.T) {
	dir := os.Getenv("NM_FRAMES")
	if dir == "" {
		t.Skip()
	}
	savePNG := func(name string, tt *ui.Tester) {
		t.Helper()
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}

	// 1. Cloud Music Disk connection card + visual folder browser.
	a := libraryRefreshApp()
	a.settings.CloudSources = []cloudSourceSetting{
		{ID: "od1", Type: localmusic.CloudTypeOneDrive, Name: "OneDrive", Username: "sonora@outlook.com", Folder: "/Music"},
		{ID: "qk1", Type: localmusic.CloudTypeQuark, Name: "夸克网盘", Folder: "音乐收藏"},
	}
	a.cloudFormOpen = true
	a.cloudFormTab = 1
	a.cloudBrowseID = "qk1"
	a.cloudBrowsePath = ""
	a.cloudBrowseSelected = "音乐收藏"
	a.cloudBrowseDirs = []localmusic.CloudFolder{
		{Name: "ACG 无损", Path: "ACG 无损"},
		{Name: "音乐收藏", Path: "音乐收藏"},
		{Name: "华语精选", Path: "华语精选"},
		{Name: "日系摇滚", Path: "日系摇滚"},
	}
	a.localLoaded = true
	a.localTracks = []spotify.Track{
		{ID: "cloud:1", CloudURL: "https://graph.microsoft.com/v1.0/me/drive/items/1/content", CloudSource: "OneDrive", Name: "All I Need", Artists: []spotify.Artist{{Name: "DJ Okawari"}}, Album: spotify.Album{Name: "Restore", PicURL: a.pl.Queue()[0].Album.PicURL}, Duration: 245000},
		{ID: "cloud:2", CloudURL: "quark://qk1/2", CloudSource: "夸克网盘", Name: "Beat Hit!", Artists: []spotify.Artist{{Name: "宮崎歩"}}, Album: spotify.Album{Name: "Beat Hit!", PicURL: a.pl.Queue()[1].Album.PicURL}, Duration: 258000},
		{ID: "local:3", LocalPath: "C:\\Music\\sample.flac", Name: "Bad Apple!! feat. nomico", Artists: []spotify.Artist{{Name: "のみこ"}}, Album: spotify.Album{Name: "EXSERENS", PicURL: a.pl.Queue()[2].Album.PicURL}, Duration: 214000},
	}
	a.route = route{page: pageLocal}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	tt.Frame()
	savePNG("cloud-music-connect", tt)

	// 2. Playlist with unified per-row album covers enabled.
	b := libraryRefreshApp()
	b.settings.PlaylistTrackCovers = true
	b.route = route{page: pagePlaylist, playlist: b.mine[0].ID}
	pt := ui.NewTester(b.view, 1180, 760)
	pt.Frame()
	pt.Frame()
	savePNG("playlist-covers", pt)
}
