package app

import (
	"cmp"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/localmusic"
	"vinyl/internal/spotify"
)

type hybridTrackRecord struct {
	UID     string        `json:"uid"`
	AddedAt int64         `json:"addedAt"`
	Track   spotify.Track `json:"track"`
}

type hybridPlaylistEntry struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	CreatedAt   int64               `json:"createdAt"`
	UpdatedAt   int64               `json:"updatedAt"`
	Tracks      []hybridTrackRecord `json:"tracks,omitempty"`
}

type hybridPlaylistFile struct {
	Version   int                   `json:"version"`
	Playlists []hybridPlaylistEntry `json:"playlists"`
}

type hybridPlaylistStore struct {
	path    string
	entries []hybridPlaylistEntry
}

var statHybridLocalFile = os.Stat

func hybridPlaylistsPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hybrid-playlists.json"), nil
}

func loadHybridPlaylistsFrom(path string) hybridPlaylistStore {
	st := hybridPlaylistStore{path: path}
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var raw hybridPlaylistFile
	if err := json.Unmarshal(data, &raw); err != nil || raw.Version != 1 {
		return st
	}
	for _, entry := range raw.Playlists {
		if !spotify.IsHybridPlaylistID(entry.ID) || strings.TrimSpace(entry.Name) == "" {
			continue
		}
		for i := range entry.Tracks {
			if entry.Tracks[i].UID == "" {
				entry.Tracks[i].UID = "h-" + randomHexID(6)
			}
			entry.Tracks[i].Track.UID = entry.Tracks[i].UID
		}
		st.entries = append(st.entries, entry)
	}
	return st
}

func (s *hybridPlaylistStore) save() error {
	if s.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(hybridPlaylistFile{Version: 1, Playlists: s.entries}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return writeAtomic(s.path, b)
}

func (a *app) loadHybridPlaylists() {
	path, err := hybridPlaylistsPath()
	if err != nil {
		return
	}
	a.hybridStore = loadHybridPlaylistsFrom(path)
	a.refreshHybridPlaylists()
}

func normalizeLocalPathKey(path string) string {
	clean := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(clean)
	}
	return clean
}

func trackIdentityKey(name, artists string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return ""
	}
	return n + "\n" + strings.ToLower(strings.TrimSpace(artists))
}

type hybridHydrationIndex struct {
	byID      map[string]spotify.Track
	byPath    map[string]spotify.Track
	byName    map[string]spotify.Track
	ambiguous map[string]bool
}

func buildHybridHydrationIndex(localTracks []spotify.Track) hybridHydrationIndex {
	idx := hybridHydrationIndex{
		byID:      make(map[string]spotify.Track, len(localTracks)),
		byPath:    make(map[string]spotify.Track, len(localTracks)),
		byName:    make(map[string]spotify.Track, len(localTracks)),
		ambiguous: map[string]bool{},
	}
	for _, tr := range localTracks {
		if tr.ID != "" {
			idx.byID[tr.ID] = tr
		}
		if tr.LocalPath != "" {
			idx.byPath[normalizeLocalPathKey(tr.LocalPath)] = tr
		}
		if !tr.Cloud() {
			if nk := trackIdentityKey(tr.Name, tr.ArtistNames()); nk != "" {
				if _, exists := idx.byName[nk]; exists {
					idx.ambiguous[nk] = true
				} else {
					idx.byName[nk] = tr
				}
			}
		}
	}
	return idx
}

func hydrateHybridTrack(tr spotify.Track, idx hybridHydrationIndex, signedIn, cloudOffline bool) spotify.Track {
	uid := tr.UID
	switch {
	case tr.Cloud():
		if live, ok := idx.byID[tr.ID]; ok {
			tr = live
		}
		if localmusic.CachedCloudAudio(tr.ID) != "" {
			tr.Unplayable = false
			tr.LocalError = ""
		} else if cloudOffline {
			tr.Unplayable = true
			tr.LocalError = "云盘离线且未缓存"
		} else if tr.CloudURL == "" {
			tr.Unplayable = true
			tr.LocalError = "云盘来源已移除"
		}
	case tr.Local():
		if live, ok := idx.byID[tr.ID]; ok {
			tr = live
		} else if tr.LocalPath != "" {
			if live, ok := idx.byPath[normalizeLocalPathKey(tr.LocalPath)]; ok {
				tr = live
			} else if info, err := statHybridLocalFile(tr.LocalPath); err == nil && info != nil && !info.IsDir() {
				tr.Unplayable = false
				tr.LocalError = ""
			} else if nk := trackIdentityKey(tr.Name, tr.ArtistNames()); nk != "" && !idx.ambiguous[nk] {
				if matched, ok := idx.byName[nk]; ok {
					tr = matched
				} else {
					tr.Unplayable = true
					tr.LocalError = "本地文件缺失"
				}
			} else {
				tr.Unplayable = true
				tr.LocalError = "本地文件缺失"
			}
		} else {
			tr.Unplayable = true
			tr.LocalError = "本地文件缺失"
		}
	default:
		if !signedIn {
			tr.Unplayable = true
			tr.LocalError = "需登录 Spotify"
		} else if tr.LocalError == "需登录 Spotify" {
			tr.Unplayable = false
			tr.LocalError = ""
		}
	}
	tr.UID = uid
	return tr
}

func hybridCoverURL(tracks []spotify.Track) string {
	for _, tr := range tracks {
		if tr.Album.PicURL != "" {
			return tr.Album.PicURL
		}
	}
	return ""
}

func (a *app) buildHybridPlaylist(entry hybridPlaylistEntry, idx hybridHydrationIndex) *spotify.Playlist {
	signedIn := a.profile != nil
	tracks := make([]spotify.Track, len(entry.Tracks))
	for i, rec := range entry.Tracks {
		tr := rec.Track
		tr.UID = rec.UID
		tracks[i] = hydrateHybridTrack(tr, idx, signedIn, a.cloudOffline)
	}
	return &spotify.Playlist{
		ID:          entry.ID,
		Name:        entry.Name,
		Description: entry.Description,
		CoverURL:    hybridCoverURL(tracks),
		TrackCount:  len(tracks),
		Tracks:      tracks,
	}
}

func (a *app) refreshHybridPlaylists() {
	if a.playlists == nil {
		a.playlists = map[string]*spotify.Playlist{}
	}
	idx := buildHybridHydrationIndex(a.localTracks)
	hybrids := make([]spotify.Playlist, 0, len(a.hybridStore.entries))
	for _, entry := range a.hybridStore.entries {
		pl := a.buildHybridPlaylist(entry, idx)
		a.playlists[pl.ID] = pl
		hybrids = append(hybrids, *pl)
	}
	a.hybrids = hybrids
	if a.route.page == pagePlaylist && spotify.IsHybridPlaylistID(a.route.playlist) {
		a.playlistOrderCache = playlistOrderCache{}
		a.playlistFilterCache = trackFilterCache{}
	}
}

func (a *app) hybridEntryIndex(id string) int {
	return slices.IndexFunc(a.hybridStore.entries, func(e hybridPlaylistEntry) bool { return e.ID == id })
}

func (a *app) saveHybridStore() {
	if err := a.hybridStore.save(); err != nil {
		log.Printf("hybrid playlists: %v", err)
	}
}

func (a *app) newHybridPlaylist(tr *spotify.Track) {
	a.naming = nameDialog{open: true, add: tr, hybrid: true}
}

func (a *app) createHybridPlaylist(name string, add *spotify.Track) *spotify.Playlist {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	now := time.Now().Unix()
	entry := hybridPlaylistEntry{
		ID:        spotify.HybridPlaylistPrefix + randomHexID(8),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
	a.hybridStore.entries = slices.Insert(a.hybridStore.entries, 0, entry)
	a.refreshHybridPlaylists()
	a.saveHybridStore()
	a.expandSidebarSection(sidebarSectionHybrid)
	a.open(route{page: pagePlaylist, playlist: entry.ID})
	if add != nil {
		a.addToHybrid(entry.ID, add)
	} else {
		a.toast = "已创建混合歌单"
	}
	return a.playlists[entry.ID]
}

func (a *app) renameHybridPlaylist(id, name string) {
	name = strings.TrimSpace(name)
	i := a.hybridEntryIndex(id)
	if i < 0 || name == "" || a.hybridStore.entries[i].Name == name {
		return
	}
	a.hybridStore.entries[i].Name = name
	a.hybridStore.entries[i].UpdatedAt = time.Now().Unix()
	a.refreshHybridPlaylists()
	a.saveHybridStore()
}

func (a *app) deleteHybridPlaylist(id string) {
	i := a.hybridEntryIndex(id)
	if i < 0 {
		return
	}
	a.hybridStore.entries = slices.Delete(a.hybridStore.entries, i, i+1)
	delete(a.playlists, id)
	a.unpinPlaylist(id)
	gone := func(r route) bool { return r.page == pagePlaylist && r.playlist == id }
	a.history = slices.DeleteFunc(a.history, gone)
	a.forward = slices.DeleteFunc(a.forward, gone)
	if gone(a.route) {
		r := route{page: pageHome}
		if n := len(a.history); n > 0 {
			r, a.history = a.history[n-1], a.history[:n-1]
		}
		a.showAgain(r)
	}
	a.refreshHybridPlaylists()
	a.saveHybridStore()
	a.toast = "已删除混合歌单"
}

func (a *app) addToHybrid(pid string, tr *spotify.Track) {
	if tr == nil || tr.Episode {
		return
	}
	i := a.hybridEntryIndex(pid)
	if i < 0 {
		return
	}
	entry := &a.hybridStore.entries[i]
	if slices.ContainsFunc(entry.Tracks, func(rec hybridTrackRecord) bool { return rec.Track.ID == tr.ID }) {
		a.toast = "歌曲已在混合歌单中"
		return
	}
	snap := *tr
	uid := "h-" + randomHexID(6)
	snap.UID = uid
	// Do not persist transient offline/auth warnings in the stored snapshot.
	if snap.LocalError == "需登录 Spotify" || snap.LocalError == "云盘离线且未缓存" {
		snap.LocalError = ""
		snap.Unplayable = false
	}
	now := time.Now().Unix()
	entry.Tracks = append(entry.Tracks, hybridTrackRecord{UID: uid, AddedAt: now, Track: snap})
	entry.UpdatedAt = now
	a.refreshHybridPlaylists()
	a.saveHybridStore()
	a.toast = "已添加到混合歌单"
}

func (a *app) removeFromHybrid(pid, uid string) {
	if uid == "" {
		return
	}
	i := a.hybridEntryIndex(pid)
	if i < 0 {
		return
	}
	entry := &a.hybridStore.entries[i]
	ti := slices.IndexFunc(entry.Tracks, func(rec hybridTrackRecord) bool { return rec.UID == uid })
	if ti < 0 {
		return
	}
	entry.Tracks = slices.Delete(slices.Clone(entry.Tracks), ti, ti+1)
	entry.UpdatedAt = time.Now().Unix()
	a.refreshHybridPlaylists()
	a.saveHybridStore()
	a.toast = "已从混合歌单移除"
}

func (a *app) moveHybridTrack(pid, uid string, delta int) {
	if uid == "" || delta == 0 {
		return
	}
	i := a.hybridEntryIndex(pid)
	if i < 0 {
		return
	}
	entry := &a.hybridStore.entries[i]
	ti := slices.IndexFunc(entry.Tracks, func(rec hybridTrackRecord) bool { return rec.UID == uid })
	tj := ti + delta
	if ti < 0 || tj < 0 || tj >= len(entry.Tracks) {
		return
	}
	next := slices.Clone(entry.Tracks)
	next[ti], next[tj] = next[tj], next[ti]
	entry.Tracks = next
	entry.UpdatedAt = time.Now().Unix()
	a.refreshHybridPlaylists()
	a.saveHybridStore()
}

func (a *app) hybridTrackPosition(pid, uid string) (index, total int) {
	i := a.hybridEntryIndex(pid)
	if i < 0 {
		return -1, 0
	}
	tracks := a.hybridStore.entries[i].Tracks
	return slices.IndexFunc(tracks, func(rec hybridTrackRecord) bool { return rec.UID == uid }), len(tracks)
}

func playableTracks(tracks []spotify.Track) []spotify.Track {
	out := make([]spotify.Track, 0, len(tracks))
	for _, tr := range tracks {
		if tr.Playable() {
			out = append(out, tr)
		}
	}
	return out
}

func countPlayableTracks(tracks []spotify.Track) int {
	n := 0
	for i := range tracks {
		if tracks[i].Playable() {
			n++
		}
	}
	return n
}

func hybridTrackSourceLabel(tr *spotify.Track) (label string, offlineCached bool) {
	switch {
	case tr.Cloud():
		return cmp.Or(strings.TrimSpace(tr.CloudSource), "云盘"), localmusic.CachedCloudAudio(tr.ID) != ""
	case tr.Local():
		return "本机", false
	default:
		return "Spotify", false
	}
}
