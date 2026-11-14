package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

type trackFilterCache struct {
	key     string
	query   string
	count   int
	firstID string
	lastID  string
	matches []spotify.Track
}

func cachedFilterTracks(cache *trackFilterCache, key string, tracks []spotify.Track, query string) []spotify.Track {
	if strings.TrimSpace(query) == "" {
		return tracks
	}
	firstID, lastID := "", ""
	if n := len(tracks); n > 0 {
		firstID, lastID = tracks[0].ID, tracks[n-1].ID
	}
	if cache.key == key && cache.query == query && cache.count == len(tracks) && cache.firstID == firstID && cache.lastID == lastID {
		return cache.matches
	}
	matches := filterPlaylistTracks(tracks, query)
	*cache = trackFilterCache{
		key:     key,
		query:   query,
		count:   len(tracks),
		firstID: firstID,
		lastID:  lastID,
		matches: matches,
	}
	return matches
}

func filterPlaylistTracks(tracks []spotify.Track, query string) []spotify.Track {
	terms := strings.Fields(strings.ToLower(lyrics.SimplifyText(query)))
	if len(terms) == 0 {
		return tracks
	}
	var matches []spotify.Track
	for _, track := range tracks {
		text := strings.ToLower(lyrics.SimplifyText(track.Name + " " + track.ArtistNames() + " " + track.Album.Name))
		matched := true
		for _, term := range terms {
			if !strings.Contains(text, term) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, track)
		}
	}
	return matches
}

func (a *app) playlistSearch(c *ui.Context, playlist *spotify.Playlist) {
	if a.playlistQueries == nil {
		a.playlistQueries = map[string]string{}
	}
	query := a.playlistQueries[playlist.ID]
	ui.Row(c.Key("playlist-search")).Height(54).AlignItems(ui.Center).Gap(12).Padding(0, 0, 18, 0).Children(func() {
		s := a.searchField(c.Key("search-capsule"), playlist.ID, "clear-playlist-search", &a.playlistSearchBox, &query, "歌单内搜索", "搜索此歌单")
		s.row.Width(300).Shrink(1)
		if s.changed || s.cleared {
			a.playlistQueries[playlist.ID] = query
			a.trackList.ScrollTo(0, ui.Start)
		}
		if query != "" {
			ui.Text(c, fmt.Sprintf("找到 %d 首", len(cachedFilterTracks(&a.playlistFilterCache, playlist.ID, playlist.Tracks, query)))).FontSize(12).TextColor(a.pal.muted)
		}
	})
}

func (a *app) playPlaylistMatch(id string, match spotify.Track) {
	playlist := a.playlists[id]
	if playlist == nil {
		return
	}
	for index, track := range playlist.Tracks {
		if track.ID == match.ID && (match.UID == "" || track.UID == match.UID) {
			a.playFrom(spotify.HomeItem{List: playlist}, playlist.Tracks, index)
			return
		}
	}
}
