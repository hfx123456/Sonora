package app

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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
