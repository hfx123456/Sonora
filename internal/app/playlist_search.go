package app

import (
	"fmt"
	"strings"
	"time"

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
	ui.Row(c.Key("playlist-search")).AlignItems(ui.Center).Gap(12).Padding(0, 0, 18, 0).Children(func() {
		field := ui.Row(c.Key("search-capsule")).Width(300).Shrink(1).Height(36).PaddingX(12).Gap(8).AlignItems(ui.Center).Radius(18)
		field.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(a.pal.muted)
			if ui.TextInputBase(c.Key(playlist.ID), &query).Bind(&a.playlistSearchBox).Label("歌单内搜索").Placeholder("搜索此歌单").FontSize(12.5).Grow(1).MinWidth(0).Changed() {
				a.playlistQueries[playlist.ID] = query
				a.trackList.ScrollTo(0, ui.Start)
			}
			if query != "" && iconButton(c.Key("clear-playlist-search"), iconX, 11, a.pal.muted, "清除搜索").Clicked() {
				a.playlistQueries[playlist.ID] = ""
				a.trackList.ScrollTo(0, ui.Start)
			}
		})
		background, edge := a.pal.hover, ui.Transparent
		if field.FocusWithin() {
			background, edge = a.pal.bg, a.pal.accent.Alpha(.35)
		}
		field.Background(field.AnimateColor("search-bg", background, ui.Animation{Duration: 120 * time.Millisecond})).Border(1, edge)
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
