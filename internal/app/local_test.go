package app

import (
	"encoding/json"
	"testing"

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
