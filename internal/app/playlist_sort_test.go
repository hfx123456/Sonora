package app

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

// Sorting compares the selected metadata, preserves ties, and leaves the source playlist unchanged.
func TestPlaylistSortMetadata(t *testing.T) {
	tracks := []spotify.Track{
		{ID: "a", UID: "a", Name: "beta", Artists: []spotify.Artist{{Name: "Charlie"}}, Album: spotify.Album{Name: "Beta"}, Duration: 240000},
		{ID: "b", UID: "b", Name: "Alpha", Artists: []spotify.Artist{{Name: "Alpha"}}, Album: spotify.Album{Name: "Zulu"}, Duration: 180000},
		{ID: "c", UID: "c", Name: "alpha", Artists: []spotify.Artist{{Name: "Bravo"}}, Album: spotify.Album{Name: "Alpha"}, Duration: 180000},
	}
	before := slices.Clone(tracks)
	for _, tc := range []struct {
		order playlistSort
		want  []string
	}{
		{playlistOriginal, []string{"a", "b", "c"}},
		{playlistByTitle, []string{"b", "c", "a"}},
		{playlistByArtist, []string{"b", "c", "a"}},
		{playlistByAlbum, []string{"c", "a", "b"}},
		{playlistByDuration, []string{"b", "c", "a"}},
	} {
		got := sortPlaylistTracks(tracks, tc.order)
		if ids := playlistTrackIDs(got); !slices.Equal(ids, tc.want) {
			t.Errorf("order %d: got %v, want %v", tc.order, ids, tc.want)
		}
		if !reflect.DeepEqual(tracks, before) {
			t.Fatal("sorting changed the source playlist")
		}
	}
}

// A refreshed playlist replaces the sorted and filtered view even when its count and end tracks stay the same.
func TestPlaylistSortRefresh(t *testing.T) {
	a := newTestApp()
	pl := &spotify.Playlist{ID: "42", Tracks: []spotify.Track{{ID: "a", Name: "Alpha"}, {ID: "c", Name: "Charlie"}, {ID: "e", Name: "Echo"}}}
	a.playlistSorts = map[string]playlistSort{pl.ID: playlistByTitle}
	ordered := a.orderedPlaylistTracks(pl)
	if matches := cachedFilterTracks(&a.playlistFilterCache, pl.ID, ordered, "Charlie"); len(matches) != 1 {
		t.Fatal("initial search did not match the middle track")
	}
	pl.Tracks = slices.Clone(pl.Tracks)
	pl.Tracks[1] = spotify.Track{ID: "z", Name: "Zulu"}
	ordered = a.orderedPlaylistTracks(pl)
	if ids := playlistTrackIDs(ordered); !slices.Equal(ids, []string{"a", "e", "z"}) {
		t.Fatalf("refresh kept the previous order: %v", ids)
	}
	if matches := cachedFilterTracks(&a.playlistFilterCache, pl.ID, ordered, "Charlie"); len(matches) != 0 {
		t.Fatal("refresh kept a removed search result")
	}
	pl.Tracks = nil
	if len(a.orderedPlaylistTracks(pl)) != 0 {
		t.Fatal("empty refresh kept sorted songs")
	}
}

// Choosing a sort leaves playback alone until a song is chosen, and searching keeps the complete sorted queue.
func TestPlaylistSortInputAndPlayback(t *testing.T) {
	for _, width := range []int{1200, 880} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			a := artistApp()
			pl := a.playlists["42"]
			pl.Tracks = []spotify.Track{{ID: "c", Name: "Zebra", Duration: 240000}, {ID: "b", Name: "Beta", Duration: 200000}, {ID: "a", Name: "Alpha", Duration: 180000}}
			a.pl.PlayQueue(pl.Tracks, 0)
			tt := ui.NewTester(a.view, width, 580)
			tt.Frame()
			choose := func(label string) {
				t.Helper()
				if err := tt.Click("歌单排序"); err != nil {
					t.Fatal(err)
				}
				if err := tt.ChooseMenuItem(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			choose("歌曲名称")
			if a.pl.Current().ID != "c" || !slices.Equal(playlistTrackIDs(a.pl.Queue()), []string{"c", "b", "a"}) {
				t.Fatal("sorting changed the active queue")
			}
			if err := tt.Click("歌单内搜索"); err != nil {
				t.Fatal(err)
			}
			tt.Type("beta")
			tt.Frame()
			for range 2 {
				if err := tt.Click("Beta"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			if a.pl.Current().ID != "b" || !slices.Equal(playlistTrackIDs(a.pl.Queue()), []string{"a", "b", "c"}) {
				t.Fatal("filtered playback lost the full sorted queue")
			}
			a.pl.Next()
			if a.pl.Current().ID != "c" {
				t.Fatal("next did not follow the chosen sort")
			}
			choose("默认顺序")
			if err := tt.Click("播放"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if a.pl.Current().ID != "c" || !slices.Equal(playlistTrackIDs(a.pl.Queue()), []string{"c", "b", "a"}) {
				t.Fatal("restoring the original order did not restore playback order")
			}
		})
	}
}

// Starting a filtered duplicate uses its playlist occurrence within the complete sorted queue.
func TestPlaylistSortDuplicateOccurrence(t *testing.T) {
	a := artistApp()
	pl := a.playlists["42"]
	pl.Tracks = []spotify.Track{{ID: "other", UID: "other", Name: "Zulu"}, {ID: "same", UID: "first", Name: "Alpha"}, {ID: "same", UID: "second", Name: "Alpha"}}
	a.playlistSorts = map[string]playlistSort{pl.ID: playlistByTitle}
	a.playPlaylistMatch(pl.ID, pl.Tracks[2])
	if cur := a.pl.Current(); cur == nil || cur.UID != "second" || len(a.pl.Queue()) != 3 {
		t.Fatal("sorted playback chose the first duplicate or lost the full queue")
	}
	a.pl.Next()
	if a.pl.Current().ID != "other" {
		t.Fatal("next did not continue after the selected occurrence")
	}
}

func playlistTrackIDs(tracks []spotify.Track) []string {
	ids := make([]string, len(tracks))
	for i := range tracks {
		ids[i] = tracks[i].ID
	}
	return ids
}
