package app

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
	"vinyl/internal/spotify"
)

// The playlist search must remain usable after scrolling, and spaces typed into it must not pause music.
func TestPlaylistSearchRemainsAfterScrolling(t *testing.T) {
	application := artistApp()
	playlist := application.playlists["42"]
	for index := 0; index < 100; index++ {
		playlist.Tracks = append(playlist.Tracks, spotify.Track{ID: fmt.Sprint(index + 2), Name: fmt.Sprintf("歌曲%d", index), Duration: 200000})
	}
	application.pl.PlayQueue(playlist.Tracks, 0)
	tester := ui.NewTester(application.view, 1200, 800)
	tester.Frame()
	tester.Scroll(900, 550, 0, 1600)
	tester.Advance(time.Second)
	tester.Frame()
	if err := tester.Click("歌单内搜索"); err != nil {
		t.Fatal("search scrolled out of view:", err)
	}
	tester.Type("歌曲 69")
	tester.Frame()
	tester.Frame()
	if application.playlistQueries["42"] != "歌曲 69" || !application.pl.Playing() {
		t.Fatal("typing into the fixed search changed playback or lost the query")
	}
	if err := tester.Click("歌曲69"); err != nil {
		t.Fatal("filtered result was not brought into view:", err)
	}
}

// Playlist filtering matches song, artist and album case-insensitively and keeps duplicate occurrences distinct.
func TestPlaylistSearchMatchesMetadata(t *testing.T) {
	tracks := []spotify.Track{{ID: "one", Name: "晴天", Artists: []spotify.Artist{{Name: "周杰伦"}}, Album: spotify.Album{Name: "叶惠美"}}, {ID: "two", Name: "Love Story", Artists: []spotify.Artist{{Name: "Taylor Swift"}}}}
	for _, query := range []string{"晴天", "周杰伦", "叶惠美", "周杰伦 晴天"} {
		got := filterPlaylistTracks(tracks, query)
		if len(got) != 1 || got[0].ID != "one" {
			t.Fatal("Chinese metadata search failed", query)
		}
	}
	if got := filterPlaylistTracks(tracks, "LOVE swift"); len(got) != 1 || got[0].ID != "two" {
		t.Fatal("case-insensitive mixed-field search failed")
	}
	if len(filterPlaylistTracks(tracks, "不存在")) != 0 || len(filterPlaylistTracks(tracks, "  ")) != 2 {
		t.Fatal("empty query or missing results handled incorrectly")
	}
}

// Simplified and traditional queries match either metadata spelling without changing the displayed tracks.
func TestPlaylistSearchChineseVariants(t *testing.T) {
	for _, track := range []spotify.Track{
		{ID: "song", Name: "愛與夢", Artists: []spotify.Artist{{Name: "周杰倫"}}, Album: spotify.Album{Name: "葉惠美"}},
		{ID: "song", Name: "爱与梦", Artists: []spotify.Artist{{Name: "周杰伦"}}, Album: spotify.Album{Name: "叶惠美"}},
	} {
		for _, query := range []string{"爱与梦", "愛與夢", "周杰伦", "周杰倫", "叶惠美", "葉惠美", "愛与梦 周杰伦 葉惠美"} {
			got := filterPlaylistTracks([]spotify.Track{track}, query)
			if len(got) != 1 || got[0].ID != track.ID || got[0].Name != track.Name || got[0].ArtistNames() != track.ArtistNames() || got[0].Album.Name != track.Album.Name {
				t.Fatalf("query %q failed or changed metadata: %+v", query, got)
			}
		}
	}
}

// Typing filters the current playlist, and double-clicking a result plays its original position without cutting down the queue.
func TestPlaylistSearchInputAndPlayback(t *testing.T) {
	application := artistApp()
	playlist := application.playlists["42"]
	playlist.Tracks = append(playlist.Tracks, spotify.Track{ID: "two", Name: "Love Story", Duration: 200000}, spotify.Track{ID: "three", Name: "下一首", Duration: 200000})
	tester := ui.NewTester(application.view, 1200, 900)
	tester.Frame()
	if err := tester.Click("歌单内搜索"); err != nil {
		t.Fatal(err)
	}
	tester.Type("love")
	tester.Frame()
	tester.Frame()
	if application.playlistQueries["42"] != "love" {
		t.Fatal("search input not applied")
	}
	for range 2 {
		if err := tester.Click("Love Story"); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
	}
	if application.pl.Current() == nil || application.pl.Current().ID != "two" || !application.pl.Playing() || len(application.pl.Queue()) != 3 {
		t.Fatal("filtered playback started the wrong song or truncated the playlist")
	}
	if err := tester.Click("清除搜索"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if application.playlistQueries["42"] != "" {
		t.Fatal("clear did not restore the full playlist")
	}
}
