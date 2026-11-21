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
	tracks, changed := orderedTracks(&a.playlistOrderCache, pl.Tracks, a.playlistSorts[pl.ID])
	if changed {
		a.playlistFilterCache = trackFilterCache{}
	}
	return tracks
}

// orderedLocalTracks keeps a display and playback sort order for local music
// without altering the scanned slice.
func (a *app) orderedLocalTracks() []spotify.Track {
	tracks, changed := orderedTracks(&a.localOrderCache, a.localTracks, a.localSort)
	if changed {
		a.localFilterCache = trackFilterCache{}
	}
	return tracks
}

func orderedTracks(cache *playlistOrderCache, source []spotify.Track, order playlistSort) ([]spotify.Track, bool) {
	// Refreshes and edits replace the source slice, so progress updates and
	// hover frames can reuse the sorted list without sorting it again.
	sameSource := len(cache.source) == len(source) && (len(source) == 0 || &cache.source[0] == &source[0])
	if cache.order != order || !sameSource {
		*cache = playlistOrderCache{source: source, order: order, tracks: sortPlaylistTracks(source, order)}
		return cache.tracks, true
	}
	return cache.tracks, false
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
