package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"vinyl/internal/lyrics"
)

// NetEaseAccount is a signed-in NetEase Cloud Music listener profile used only
// to read their playlist catalogue for cross-source migration into hybrid playlists.
type NetEaseAccount struct {
	UserID    int64  `json:"userId"`
	Nickname  string `json:"nickname"`
	AvatarURL string `json:"avatarUrl"`
}

// NetEasePlaylistSummary describes one playlist belonging to or subscribed by
// a NetEase Cloud Music account.
type NetEasePlaylistSummary struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	CoverURL   string `json:"coverUrl"`
	TrackCount int    `json:"trackCount"`
	Subscribed bool   `json:"subscribed"`
}

// NetEasePlaylistTrack holds the track metadata read from a NetEase playlist
// before matching against the user's local/cloud library and Spotify catalogue.
type NetEasePlaylistTrack struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Artists  []string `json:"artists"`
	Album    string   `json:"album"`
	PicURL   string   `json:"picUrl"`
	Duration int64    `json:"duration"` // milliseconds
	Aliases  []string `json:"aliases,omitempty"`
	Trans    []string `json:"trans,omitempty"`
}

// NetEasePlaylistDetail is a fetched NetEase playlist with its full track list.
type NetEasePlaylistDetail struct {
	ID         int64
	Name       string
	CoverURL   string
	TrackCount int
	Tracks     []NetEasePlaylistTrack
}

var (
	reNetEaseDigits        = regexp.MustCompile(`^\d{4,}$`)
	reNetEasePlaylistQuery = regexp.MustCompile(`(?i)playlist[^0-9]*[?&]id=(\d+)`)
	reNetEasePlaylistPath  = regexp.MustCompile(`(?i)playlist/(\d+)`)
	reNetEaseAnyID         = regexp.MustCompile(`(?i)[?&]id=(\d+)`)
	reNetEaseShortURL      = regexp.MustCompile(`https?://(?:163cn\.tv|y\.music\.163\.com|music\.163\.com)[^\s"']+`)
)

// ParseNetEasePlaylistID extracts a NetEase playlist ID from a bare numeric ID,
// a music.163.com URL, or a share snippet copied from the NetEase app.
func ParseNetEasePlaylistID(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, errors.New("请输入网易云歌单链接或歌单 ID")
	}
	if reNetEaseDigits.MatchString(s) {
		return strconv.ParseInt(s, 10, 64)
	}
	for _, re := range []*regexp.Regexp{reNetEasePlaylistQuery, reNetEasePlaylistPath, reNetEaseAnyID} {
		if m := re.FindStringSubmatch(s); len(m) == 2 {
			return strconv.ParseInt(m[1], 10, 64)
		}
	}
	return 0, errors.New("无法识别网易云歌单链接或 ID")
}

// ResolveNetEasePlaylistID parses raw directly or follows a 163cn.tv short
// share link to recover the underlying playlist ID.
func ResolveNetEasePlaylistID(ctx context.Context, raw string) (int64, error) {
	if id, err := ParseNetEasePlaylistID(raw); err == nil {
		return id, nil
	}
	short := reNetEaseShortURL.FindString(raw)
	if short == "" {
		return 0, errors.New("无法识别网易云歌单链接或 ID")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, short, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", UserAgent)
	res, err := Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.Request != nil && res.Request.URL != nil {
		if id, err := ParseNetEasePlaylistID(res.Request.URL.String()); err == nil {
			return id, nil
		}
	}
	return 0, errors.New("未能从短链接中解析出网易云歌单 ID")
}

func neteaseHTTPS(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "http://") {
		return "https://" + strings.TrimPrefix(raw, "http://")
	}
	return raw
}

func neteaseDo(ctx context.Context, method, path, cookie string, form url.Values, out any) error {
	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(reqCtx, method, neteaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Referer", "https://music.163.com/")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("netease: HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return err
	}
	var status struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Msg     string `json:"msg"`
	}
	if err := json.Unmarshal(b, &status); err != nil {
		return err
	}
	if status.Code != http.StatusOK {
		msg := strings.TrimSpace(status.Message)
		if msg == "" {
			msg = strings.TrimSpace(status.Msg)
		}
		if msg != "" {
			return fmt.Errorf("netease: code %d (%s)", status.Code, msg)
		}
		return fmt.Errorf("netease: code %d", status.Code)
	}
	return json.Unmarshal(b, out)
}

// NetEaseAccountInfo returns the signed-in NetEase user for cookie, or an error
// if the cookie is not authenticated.
func NetEaseAccountInfo(ctx context.Context, cookie string) (*NetEaseAccount, error) {
	if strings.TrimSpace(cookie) == "" {
		return nil, errors.New("未登录网易云音乐")
	}
	var res struct {
		Profile *struct {
			UserID    int64  `json:"userId"`
			Nickname  string `json:"nickname"`
			AvatarURL string `json:"avatarUrl"`
		} `json:"profile"`
	}
	if err := neteaseDo(ctx, http.MethodGet, "/api/nuser/account/get", cookie, nil, &res); err != nil {
		return nil, err
	}
	if res.Profile == nil || res.Profile.UserID == 0 {
		return nil, errors.New("网易云登录状态无效或尚未完成登录")
	}
	return &NetEaseAccount{
		UserID:    res.Profile.UserID,
		Nickname:  res.Profile.Nickname,
		AvatarURL: neteaseHTTPS(res.Profile.AvatarURL),
	}, nil
}

// NetEaseUserPlaylists lists created and subscribed playlists for uid.
func NetEaseUserPlaylists(ctx context.Context, cookie string, uid int64) ([]NetEasePlaylistSummary, error) {
	if uid <= 0 {
		return nil, errors.New("无效的网易云用户 ID")
	}
	var out []NetEasePlaylistSummary
	seen := map[int64]bool{}
	const pageSize = 100
	for offset := 0; offset < 500; offset += pageSize {
		var res struct {
			More     bool `json:"more"`
			Playlist []struct {
				ID         int64  `json:"id"`
				Name       string `json:"name"`
				CoverURL   string `json:"coverImgUrl"`
				TrackCount int    `json:"trackCount"`
				Subscribed bool   `json:"subscribed"`
			} `json:"playlist"`
		}
		q := url.Values{
			"uid":    {strconv.FormatInt(uid, 10)},
			"limit":  {strconv.Itoa(pageSize)},
			"offset": {strconv.Itoa(offset)},
		}
		if err := neteaseDo(ctx, http.MethodGet, "/api/user/playlist?"+q.Encode(), cookie, nil, &res); err != nil {
			if len(out) > 0 {
				break
			}
			return nil, err
		}
		for _, pl := range res.Playlist {
			if pl.ID == 0 || seen[pl.ID] {
				continue
			}
			seen[pl.ID] = true
			out = append(out, NetEasePlaylistSummary{
				ID:         pl.ID,
				Name:       strings.TrimSpace(pl.Name),
				CoverURL:   neteaseHTTPS(pl.CoverURL),
				TrackCount: pl.TrackCount,
				Subscribed: pl.Subscribed,
			})
		}
		if !res.More || len(res.Playlist) < pageSize {
			break
		}
	}
	return out, nil
}

type neteaseRawTrack struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Alia     []string `json:"alia"`
	Tns      []string `json:"tns"`
	Duration int64    `json:"dt"`
	Artists  []struct {
		Name string `json:"name"`
	} `json:"ar"`
	Album struct {
		Name   string `json:"name"`
		PicURL string `json:"picUrl"`
	} `json:"al"`
}

func (r neteaseRawTrack) toPlaylistTrack() NetEasePlaylistTrack {
	artists := make([]string, 0, len(r.Artists))
	for _, a := range r.Artists {
		if name := strings.TrimSpace(a.Name); name != "" {
			artists = append(artists, name)
		}
	}
	return NetEasePlaylistTrack{
		ID:       r.ID,
		Name:     strings.TrimSpace(r.Name),
		Artists:  artists,
		Album:    strings.TrimSpace(r.Album.Name),
		PicURL:   neteaseHTTPS(r.Album.PicURL),
		Duration: r.Duration,
		Aliases:  r.Alia,
		Trans:    r.Tns,
	}
}

// NetEasePlaylist fetches the playlist metadata and all track details for id.
// When /api/v6/playlist/detail omits tracks beyond its inline limit, the remaining
// trackIds are batch-fetched via /api/v3/song/detail.
func NetEasePlaylist(ctx context.Context, cookie string, id int64) (*NetEasePlaylistDetail, error) {
	if id <= 0 {
		return nil, errors.New("无效的网易云歌单 ID")
	}
	var res struct {
		Playlist *struct {
			ID         int64             `json:"id"`
			Name       string            `json:"name"`
			CoverURL   string            `json:"coverImgUrl"`
			TrackCount int               `json:"trackCount"`
			Tracks     []neteaseRawTrack `json:"tracks"`
			TrackIDs   []struct {
				ID int64 `json:"id"`
			} `json:"trackIds"`
		} `json:"playlist"`
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "n": {"1000"}}
	if err := neteaseDo(ctx, http.MethodGet, "/api/v6/playlist/detail?"+q.Encode(), cookie, nil, &res); err != nil {
		return nil, err
	}
	if res.Playlist == nil {
		return nil, errors.New("未找到该网易云歌单，可能已被删除或设为私密")
	}
	byID := make(map[int64]NetEasePlaylistTrack, len(res.Playlist.Tracks))
	for _, raw := range res.Playlist.Tracks {
		if raw.ID != 0 {
			byID[raw.ID] = raw.toPlaylistTrack()
		}
	}
	var missing []int64
	for _, item := range res.Playlist.TrackIDs {
		if item.ID != 0 {
			if _, ok := byID[item.ID]; !ok {
				missing = append(missing, item.ID)
			}
		}
	}
	const chunkSize = 400
	for start := 0; start < len(missing); start += chunkSize {
		end := min(start+chunkSize, len(missing))
		chunk := missing[start:end]
		entries := make([]map[string]int64, len(chunk))
		for i, sid := range chunk {
			entries[i] = map[string]int64{"id": sid}
		}
		encoded, err := json.Marshal(entries)
		if err != nil {
			continue
		}
		var detail struct {
			Songs []neteaseRawTrack `json:"songs"`
		}
		form := url.Values{"c": {string(encoded)}}
		if err := neteaseDo(ctx, http.MethodPost, "/api/v3/song/detail", cookie, form, &detail); err != nil {
			// Preserve already-fetched tracks rather than failing the whole import.
			break
		}
		for _, raw := range detail.Songs {
			if raw.ID != 0 {
				byID[raw.ID] = raw.toPlaylistTrack()
			}
		}
	}

	tracks := make([]NetEasePlaylistTrack, 0, max(len(res.Playlist.TrackIDs), len(res.Playlist.Tracks)))
	if len(res.Playlist.TrackIDs) > 0 {
		for _, item := range res.Playlist.TrackIDs {
			if tr, ok := byID[item.ID]; ok {
				tracks = append(tracks, tr)
			}
		}
	} else {
		for _, raw := range res.Playlist.Tracks {
			tracks = append(tracks, raw.toPlaylistTrack())
		}
	}
	return &NetEasePlaylistDetail{
		ID:         res.Playlist.ID,
		Name:       strings.TrimSpace(res.Playlist.Name),
		CoverURL:   neteaseHTTPS(res.Playlist.CoverURL),
		TrackCount: max(res.Playlist.TrackCount, len(tracks)),
		Tracks:     tracks,
	}, nil
}

// ToTrack converts a NetEase playlist track into a directly playable Sonora
// track backed by NetEase's playback API (with runtime fallback if unavailable).
func (t NetEasePlaylistTrack) ToTrack() Track {
	artists := make([]Artist, 0, len(t.Artists))
	for _, name := range t.Artists {
		if name = strings.TrimSpace(name); name != "" {
			artists = append(artists, Artist{Name: name})
		}
	}
	return Track{
		ID:       fmt.Sprintf("netease:%d", t.ID),
		Name:     t.Name,
		Artists:  artists,
		Album:    Album{Name: t.Album, PicURL: t.PicURL},
		Duration: t.Duration,
	}
}

var (
	// ErrNetEaseUnavailable indicates NetEase returned no playable audio URL for a song.
	ErrNetEaseUnavailable = errors.New("网易云该歌曲暂无可用音频")
	// ErrNetEaseTrialOnly indicates NetEase only returned a short VIP trial preview.
	ErrNetEaseTrialOnly = errors.New("网易云该歌曲仅支持试听")
)

// NetEaseSongMedia resolves the direct audio stream URL for a NetEase song ID
// using the listener's optional cookie. If NetEase returns no URL or only a
// short VIP trial preview, it returns ErrNetEaseUnavailable or ErrNetEaseTrialOnly
// so the caller can seamlessly fall back to local/cloud or Spotify playback.
func NetEaseSongMedia(ctx context.Context, cookie string, songID int64) (*Media, error) {
	if songID <= 0 {
		return nil, ErrNetEaseUnavailable
	}
	var res struct {
		Data []struct {
			ID            int64  `json:"id"`
			URL           string `json:"url"`
			Code          int    `json:"code"`
			Fee           int    `json:"fee"`
			Time          int64  `json:"time"`
			FreeTrialInfo *struct {
				Start int64 `json:"start"`
				End   int64 `json:"end"`
			} `json:"freeTrialInfo"`
		} `json:"data"`
	}
	q := url.Values{
		"ids": {fmt.Sprintf("[%d]", songID)},
		"br":  {"320000"},
	}
	if err := neteaseDo(ctx, http.MethodGet, "/api/song/enhance/player/url?"+q.Encode(), cookie, nil, &res); err != nil {
		return nil, err
	}
	if len(res.Data) == 0 {
		return nil, ErrNetEaseUnavailable
	}
	d := res.Data[0]
	if d.FreeTrialInfo != nil {
		return nil, ErrNetEaseTrialOnly
	}
	streamURL := strings.TrimSpace(d.URL)
	if d.Code != http.StatusOK || streamURL == "" {
		return nil, ErrNetEaseUnavailable
	}
	return &Media{
		File: streamURL,
		Headers: map[string]string{
			"Referer":    "https://music.163.com/",
			"User-Agent": UserAgent,
		},
		Duration: time.Duration(d.Time) * time.Millisecond,
	}, nil
}

// MatchCandidateForNetEase picks the best matching track from candidates
// (either local/cloud library tracks or Spotify search results) for a NetEase
// playlist entry, rejecting mismatched version tags (live/remix/instrumental).
func MatchCandidateForNetEase(want NetEasePlaylistTrack, candidates []Track, requirePlayable bool) (Track, bool) {
	if want.Name == "" || len(candidates) == 0 {
		return Track{}, false
	}
	type scored struct {
		track Track
		tier  int
		diff  int64
	}
	var hits []scored
	for _, cand := range candidates {
		if cand.Episode || cand.Name == "" {
			continue
		}
		if requirePlayable && !cand.Playable() {
			continue
		}
		if lyrics.HasExtraVersionTag(want.Name, want.Album, cand.Name) {
			continue
		}
		gotArtists := make([]string, 0, len(cand.Artists))
		for _, a := range cand.Artists {
			if name := strings.TrimSpace(a.Name); name != "" {
				gotArtists = append(gotArtists, name)
			}
		}
		artistOK := lyrics.ArtistsMatch(want.Artists, gotArtists)
		crossArtist := !artistOK && len(want.Artists) > 0 && len(gotArtists) > 0 &&
			lyrics.CrossScript(strings.Join(want.Artists, " "), strings.Join(gotArtists, " "))

		var diff int64
		if want.Duration > 0 && cand.Duration > 0 {
			diff = max(want.Duration-cand.Duration, cand.Duration-want.Duration)
		}
		if want.Duration > 0 && cand.Duration > 0 && diff > 25000 {
			continue
		}

		titleExact := sameTitle(cand.Name, want.Name)
		titleBilingual := func() bool {
			ok, _ := lyrics.BilingualParenMatch(want.Name, cand.Name)
			return ok
		}()
		titleAlias := neteaseAliasMatches(cand.Name, want.Aliases, want.Trans)
		albumOK := want.Album != "" && lyrics.AlbumMatches(want.Album, cand.Album.Name)

		tier := -1
		switch {
		case titleExact && (artistOK || len(want.Artists) == 0 || len(gotArtists) == 0) && diff <= 6000:
			tier = 0
		case (titleBilingual || titleAlias) && artistOK && diff <= 8000:
			tier = 1
		case titleExact && crossArtist && (albumOK || diff <= 4000):
			tier = 1
		case titleExact && artistOK && diff <= 20000:
			tier = 2
		case (titleBilingual || titleAlias) && (artistOK || crossArtist) && diff <= 15000:
			tier = 2
		case lyrics.CrossScript(want.Name, cand.Name) && artistOK && (albumOK || (want.Duration > 0 && cand.Duration > 0 && diff <= 4000)):
			tier = 3
		}
		if tier >= 0 {
			hits = append(hits, scored{track: cand, tier: tier, diff: diff})
		}
	}
	if len(hits) == 0 {
		return Track{}, false
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].tier != hits[j].tier {
			return hits[i].tier < hits[j].tier
		}
		if hits[i].track.Playable() != hits[j].track.Playable() {
			return hits[i].track.Playable()
		}
		return hits[i].diff < hits[j].diff
	})
	return hits[0].track, true
}

// NetEaseSearchQueries builds prioritized Spotify search queries for a NetEase track.
func NetEaseSearchQueries(t NetEasePlaylistTrack) []string {
	firstArtist := ""
	if len(t.Artists) > 0 {
		firstArtist = strings.TrimSpace(t.Artists[0])
	}
	add := func(list []string, title string) []string {
		title = strings.TrimSpace(title)
		if title == "" {
			return list
		}
		q := title
		if firstArtist != "" {
			q = title + " " + firstArtist
		}
		if !slices.Contains(list, q) {
			list = append(list, q)
		}
		return list
	}
	var queries []string
	if cleaned := cleanTitle(t.Name); cleaned != "" {
		queries = add(queries, cleaned)
	}
	queries = add(queries, t.Name)
	for _, alt := range append(slices.Clone(t.Trans), t.Aliases...) {
		if len(queries) >= 3 {
			break
		}
		queries = add(queries, cleanTitle(alt))
	}
	return queries
}
