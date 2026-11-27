package app

import (
	"strings"
	"time"

	"vinyl/internal/spotify"
)

const playlistSyncInterval = 5 * time.Minute

type playlistSyncState struct {
	pending []string
	queued  map[string]bool
	forced  map[string]bool
	checked map[string]time.Time
	active  string
}

func (state *playlistSyncState) enqueue(id string, force bool, now time.Time) {
	if id != spotify.LikedURI && !strings.HasPrefix(id, "spotify:playlist:") {
		return
	}
	if state.queued == nil {
		state.queued = map[string]bool{}
	}
	if state.forced == nil {
		state.forced = map[string]bool{}
	}
	if state.checked == nil {
		state.checked = map[string]time.Time{}
	}
	if state.queued[id] {
		if force {
			state.forced[id] = true
		}
		return
	}
	if !force && (state.active == id || !state.checked[id].IsZero() && now.Sub(state.checked[id]) < playlistSyncInterval) {
		return
	}
	state.pending = append(state.pending, id)
	state.queued[id] = true
	if force {
		state.forced[id] = true
	}
}

func (state *playlistSyncState) takeNext() (string, bool) {
	if state.active != "" || len(state.pending) == 0 {
		return "", false
	}
	id := state.pending[0]
	state.pending = state.pending[1:]
	delete(state.queued, id)
	forced := state.forced[id]
	delete(state.forced, id)
	state.active = id
	return id, forced
}

func (state *playlistSyncState) next() string {
	id, _ := state.takeNext()
	return id
}

func (state *playlistSyncState) complete(id string, now time.Time) {
	if state.checked == nil {
		state.checked = map[string]time.Time{}
	}
	state.checked[id] = now
	if state.active == id {
		state.active = ""
	}
}

func (a *app) syncPlaylistCaches(now time.Time) {
	if a.playlistCacheRoot == "" || a.profile == nil || a.quitting {
		return
	}
	a.playlistSync.enqueue(spotify.LikedURI, false, now)
	for _, lists := range [][]spotify.Playlist{a.mine, a.saved} {
		for _, playlist := range lists {
			a.playlistSync.enqueue(playlist.ID, false, now)
		}
	}
	a.startPlaylistSync()
}

func (a *app) cachePlaylistSoon(id string) {
	if a.playlistCacheRoot == "" || a.profile == nil || a.quitting {
		return
	}
	a.playlistSync.enqueue(id, true, time.Now())
	a.startPlaylistSync()
}

func (a *app) startPlaylistSync() {
	if a.playlistCacheRoot == "" || a.profile == nil || a.quitting {
		return
	}
	id, forced := a.playlistSync.takeNext()
	if id == "" {
		return
	}
	if a.loadingPl[id] {
		if forced {
			if a.plRefreshAgain == nil {
				a.plRefreshAgain = map[string]bool{}
			}
			a.plRefreshAgain[id] = true
		}
		return
	}
	a.requestPlaylist(id, false, true)
}
