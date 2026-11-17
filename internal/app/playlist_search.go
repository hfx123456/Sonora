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

const playlistToolbarHeight = 80

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
	row := ui.Row(c.Key("playlist-search")).Height(52).AlignItems(ui.Center).Gap(12)
	row.Children(func() {
		field, s := a.searchField(c.Key("search-capsule"), playlist.ID, "clear-playlist-search", &a.playlistSearchBox, &query, "歌单内搜索", "搜索此歌单")
		field.Width(300).Shrink(1)
		if s.changed || s.cleared {
			a.playlistQueries[playlist.ID] = query
			a.trackList.ScrollTo(0, ui.Start)
		}
		if query != "" {
			ui.Text(c, fmt.Sprintf("找到 %d 首", len(cachedFilterTracks(&a.playlistFilterCache, playlist.ID, a.orderedPlaylistTracks(playlist), query)))).FontSize(12).TextColor(a.pal.muted).SingleLine().Shrink(1)
		}
		ui.Box(c).Grow(1)
		order := a.playlistSorts[playlist.ID]
		b := ui.ButtonBase(c.Key("playlist-sort")).Height(32).PaddingX(10).Gap(6).AlignItems(ui.Center).Radius(8).
			Label("歌单排序").Tooltip("更改显示和播放顺序")
		if b.Hovered() {
			b.Background(a.pal.hover)
		}
		b.Children(func() {
			ui.Text(c, playlistSortLabels[order]).FontSize(12).FontWeight(500).TextColor(a.pal.muted).SingleLine()
			ui.Icon(c, iconDown).Size(12, 12).TextColor(a.pal.muted)
		})
		b.Menu(func(m *ui.Menu) {
			for i, label := range playlistSortLabels {
				if m.Item(label).Checked(order == playlistSort(i)).Chosen() {
					if a.playlistSorts == nil {
						a.playlistSorts = map[string]playlistSort{}
					}
					a.playlistSorts[playlist.ID] = playlistSort(i)
					a.trackList.ScrollTo(0, ui.Start)
				}
			}
		})
	})
}

// playlistColumns uses the same column widths as the song rows below it.
func (a *app) playlistColumns(c *ui.Context, pl *spotify.Playlist) {
	w, _ := c.Size()
	isShow := strings.HasPrefix(pl.ID, "spotify:show:")
	timeWidth, timeLabel := float32(44), "时长"
	if isShow {
		timeWidth, timeLabel = 148, "发布日期 / 时长"
	}
	ui.Row(c).Height(28).Shrink(0).PaddingX(12).Gap(14).AlignItems(ui.Center).
		BorderWidth(0, 0, 1, 0).BorderColor(a.pal.hairline).Children(func() {
		ui.Text(c, "#").Width(26).TextAlign(ui.Center).FontSize(11.5).TextColor(a.pal.faint)
		ui.Text(c, "歌曲 / 歌手").Grow(3).Basis(0).MinWidth(0).FontSize(11.5).TextColor(a.pal.muted)
		if w-sidebarWidth > 820 {
			ui.Text(c, "专辑").Grow(2).Basis(0).MinWidth(0).FontSize(11.5).TextColor(a.pal.muted)
		}
		ui.Box(c).Width(32)
		ui.Text(c, timeLabel).Width(timeWidth).Shrink(0).TextAlign(ui.End).FontSize(11.5).TextColor(a.pal.muted)
	})
}

func (a *app) playPlaylistMatch(id string, match spotify.Track) {
	playlist := a.playlists[id]
	if playlist == nil {
		return
	}
	tracks := a.orderedPlaylistTracks(playlist)
	for index, track := range tracks {
		if track.ID == match.ID && (match.UID == "" || track.UID == match.UID) {
			a.playFrom(spotify.HomeItem{List: playlist}, tracks, index)
			return
		}
	}
}
