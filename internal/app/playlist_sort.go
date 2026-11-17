package app

import (
	"cmp"
	"slices"
	"strings"

	"vinyl/internal/spotify"
)

type playlistSort int

const (
	playlistOriginal playlistSort = iota
	playlistByTitle
	playlistByArtist
	playlistByAlbum
	playlistByDuration
)

var playlistSortLabels = [...]string{"默认顺序", "歌曲名称", "歌手名称", "专辑名称", "时长从短到长"}

type playlistOrderCache struct {
	source []spotify.Track
	order  playlistSort
	tracks []spotify.Track
}

// orderedPlaylistTracks keeps a view order without changing Spotify's order
// or the playing queue; only choosing a song starts that order playing.
func (a *app) orderedPlaylistTracks(pl *spotify.Playlist) []spotify.Track {
	order := a.playlistSorts[pl.ID]
	cache := &a.playlistOrderCache
	// Refreshes and edits replace the source slice, so progress updates and
	// hover frames can reuse the sorted list without sorting it again.
	sameSource := len(cache.source) == len(pl.Tracks) && (len(pl.Tracks) == 0 || &cache.source[0] == &pl.Tracks[0])
	if cache.order != order || !sameSource {
		*cache = playlistOrderCache{source: pl.Tracks, order: order, tracks: sortPlaylistTracks(pl.Tracks, order)}
		a.playlistFilterCache = trackFilterCache{}
	}
	return cache.tracks
}

func sortPlaylistTracks(tracks []spotify.Track, order playlistSort) []spotify.Track {
	if order == playlistOriginal {
		return tracks
	}
	out := slices.Clone(tracks)
	slices.SortStableFunc(out, func(left, right spotify.Track) int {
		if order == playlistByDuration {
			return cmp.Compare(left.Duration, right.Duration)
		}
		var l, r string
		switch order {
		case playlistByTitle:
			l, r = left.Name, right.Name
		case playlistByArtist:
			l, r = left.ArtistNames(), right.ArtistNames()
		case playlistByAlbum:
			l, r = left.Album.Name, right.Album.Name
		}
		return strings.Compare(strings.ToLower(l), strings.ToLower(r))
	})
	return out
}
