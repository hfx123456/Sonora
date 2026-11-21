package fusion

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vinyl/internal/lyrics"
)

type alternateTitle struct {
	Title      string
	Album      string
	ClearAlbum bool
	ID         string
	Source     string
}

type providerCandidate struct {
	ID         string
	Source     string
	Title      string
	Aliases    []string
	Artists    []string
	Album      string
	DurationMs int64
	SourceRank int // 0 = QQ Music, 1 = NetEase
}

var resolveAlternateTitles = searchAlternateTitles

func searchAlternateTitles(ctx context.Context, req Request) []alternateTitle {
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return nil
	}
	clean := lyrics.CleanTitle(title)
	if clean == "" {
		clean = title
	}
	firstArtist := ""
	for _, a := range req.Artists {
		if s := strings.TrimSpace(a); s != "" {
			firstArtist = s
			break
		}
	}

	query := strings.TrimSpace(clean + " " + firstArtist)
	client := httpClientForProxy(req.Proxy)
	cands := queryProviders(ctx, client, query)
	ranked := rankAlternateTitles(req, cands)
	if len(ranked) == 0 && strings.TrimSpace(req.Album) != "" && firstArtist != "" {
		albumQuery := strings.TrimSpace(clean + " " + req.Album)
		if albumQuery != query {
			cands = append(cands, queryProviders(ctx, client, albumQuery)...)
			ranked = rankAlternateTitles(req, cands)
		}
	}
	return ranked
}

func queryProviders(ctx context.Context, client *http.Client, keyword string) []providerCandidate {
	if keyword == "" {
		return nil
	}
	qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var (
		wg  sync.WaitGroup
		qq  []providerCandidate
		ne  []providerCandidate
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		qq = searchQQMusicCandidates(qctx, client, keyword)
	}()
	go func() {
		defer wg.Done()
		ne = searchNetEaseCandidates(qctx, client, keyword)
	}()
	wg.Wait()
	return append(qq, ne...)
}

var (
	proxyClientsMu sync.Mutex
	proxyClients   = map[string]*http.Client{}
	directClient   = &http.Client{Timeout: 5 * time.Second}
)

func httpClientForProxy(proxy string) *http.Client {
	if proxy == "" {
		return directClient
	}
	proxyClientsMu.Lock()
	defer proxyClientsMu.Unlock()
	if client, ok := proxyClients[proxy]; ok {
		return client
	}
	parsed, err := url.Parse(proxy)
	if err != nil {
		return directClient
	}
	if len(proxyClients) >= 8 {
		for k, old := range proxyClients {
			if tr, ok := old.Transport.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
			delete(proxyClients, k)
		}
	}
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed)},
	}
	proxyClients[proxy] = client
	return client
}

func searchQQMusicCandidates(ctx context.Context, client *http.Client, keyword string) []providerCandidate {
	if cands := searchQQClientCP(ctx, client, keyword); len(cands) > 0 {
		return cands
	}
	return searchQQMobile(ctx, client, keyword)
}

func searchQQClientCP(ctx context.Context, client *http.Client, keyword string) []providerCandidate {
	endpoint := "https://c.y.qq.com/soso/fcgi-bin/client_search_cp?" + url.Values{
		"p":      {"1"},
		"n":      {"15"},
		"w":      {keyword},
		"format": {"json"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://y.qq.com/")

	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil
	}

	type cpSong struct {
		SongID    int64  `json:"songid"`
		SongName  string `json:"songname"`
		AlbumName string `json:"albumname"`
		Interval  int64  `json:"interval"`
		Singer    []struct {
			Name string `json:"name"`
		} `json:"singer"`
		Grp []struct {
			SongID    int64  `json:"songid"`
			SongName  string `json:"songname"`
			AlbumName string `json:"albumname"`
			Interval  int64  `json:"interval"`
			Singer    []struct {
				Name string `json:"name"`
			} `json:"singer"`
		} `json:"grp"`
	}
	var parsed struct {
		Code int `json:"code"`
		Data struct {
			Song struct {
				List []cpSong `json:"list"`
			} `json:"song"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.Code != 0 {
		return nil
	}
	toCand := func(songID int64, name, album string, interval int64, singers []struct{ Name string `json:"name"` }) (providerCandidate, bool) {
		title := strings.TrimSpace(name)
		if title == "" {
			return providerCandidate{}, false
		}
		var artists []string
		for _, a := range singers {
			if n := strings.TrimSpace(a.Name); n != "" {
				artists = append(artists, n)
			}
		}
		idStr := ""
		if songID > 0 {
			idStr = strconv.FormatInt(songID, 10)
		}
		return providerCandidate{
			ID:         idStr,
			Source:     "QQ 音乐 · Lyricify",
			Title:      title,
			Artists:    artists,
			Album:      strings.TrimSpace(album),
			DurationMs: interval * 1000,
			SourceRank: 0,
		}, true
	}
	var out []providerCandidate
	for _, s := range parsed.Data.Song.List {
		if c, ok := toCand(s.SongID, s.SongName, s.AlbumName, s.Interval, s.Singer); ok {
			out = append(out, c)
		}
		for _, sub := range s.Grp {
			if c, ok := toCand(sub.SongID, sub.SongName, sub.AlbumName, sub.Interval, sub.Singer); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func searchQQMobile(ctx context.Context, client *http.Client, keyword string) []providerCandidate {
	payload := map[string]any{
		"comm": map[string]string{
			"ct":             "11",
			"v":              "20080508",
			"tmeLoginType":   "0",
			"tmeLoginMethod": "0",
		},
		"music.search.SearchCgiService.DoSearchForQQMusicMobile": map[string]any{
			"module": "music.search.SearchCgiService",
			"method": "DoSearchForQQMusicMobile",
			"param": map[string]any{
				"query":        keyword,
				"highlight":    0,
				"searchid":     "",
				"sub_searchid": 0,
				"search_type":  0,
				"nqc_flag":     0,
				"sin":          0,
				"ein":          30,
				"page_num":     1,
				"num_per_page": 15,
				"cat":          2,
				"grp":          1,
				"remoteplace":  "",
				"from":         "",
				"multi_zhida":  1,
				"sem":          0,
				"related_data": "",
				"ambi_data":    "",
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://u6.y.qq.com/cgi-bin/musicu.fcg", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "QQMusic 20080508(android)")
	req.Header.Set("Referer", "https://c.y.qq.com/")

	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil
	}

	type qqSong struct {
		ID       json.Number `json:"id"`
		Title    string      `json:"title"`
		Name     string      `json:"name"`
		Subtitle string      `json:"subtitle"`
		Interval int64       `json:"interval"`
		Singer   []struct {
			Name string `json:"name"`
		} `json:"singer"`
		Album struct {
			Title string `json:"title"`
			Name  string `json:"name"`
		} `json:"album"`
		Grp []struct {
			ID       json.Number `json:"id"`
			Title    string      `json:"title"`
			Name     string      `json:"name"`
			Subtitle string      `json:"subtitle"`
			Interval int64       `json:"interval"`
			Singer   []struct {
				Name string `json:"name"`
			} `json:"singer"`
			Album struct {
				Title string `json:"title"`
				Name  string `json:"name"`
			} `json:"album"`
		} `json:"grp"`
	}
	var parsed struct {
		Mobile struct {
			Data struct {
				Body struct {
					List []qqSong `json:"item_song"`
				} `json:"body"`
			} `json:"data"`
		} `json:"music.search.SearchCgiService.DoSearchForQQMusicMobile"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return nil
	}

	cleanHL := func(s string) string {
		s = strings.ReplaceAll(s, "<em>", "")
		return strings.TrimSpace(strings.ReplaceAll(s, "</em>", ""))
	}
	toCand := func(id json.Number, title, name, sub string, interval int64, singers []struct{ Name string `json:"name"` }, alTitle, alName string) (providerCandidate, bool) {
		t := cleanHL(title)
		if t == "" {
			t = cleanHL(name)
		}
		if t == "" {
			return providerCandidate{}, false
		}
		var artists []string
		for _, a := range singers {
			if n := cleanHL(a.Name); n != "" {
				artists = append(artists, n)
			}
		}
		album := cleanHL(alTitle)
		if album == "" {
			album = cleanHL(alName)
		}
		var aliases []string
		if s := cleanHL(sub); s != "" {
			aliases = append(aliases, s)
		}
		return providerCandidate{
			ID:         strings.TrimSpace(id.String()),
			Source:     "QQ 音乐 · Lyricify",
			Title:      t,
			Aliases:    aliases,
			Artists:    artists,
			Album:      album,
			DurationMs: interval * 1000,
			SourceRank: 0,
		}, true
	}

	var out []providerCandidate
	for _, item := range parsed.Mobile.Data.Body.List {
		if c, ok := toCand(item.ID, item.Title, item.Name, item.Subtitle, item.Interval, item.Singer, item.Album.Title, item.Album.Name); ok {
			out = append(out, c)
		}
		for _, sub := range item.Grp {
			if c, ok := toCand(sub.ID, sub.Title, sub.Name, sub.Subtitle, sub.Interval, sub.Singer, sub.Album.Title, sub.Album.Name); ok {
				out = append(out, c)
			}
		}
	}
	return out
}

func searchNetEaseCandidates(ctx context.Context, client *http.Client, keyword string) []providerCandidate {
	endpoint := "https://music.163.com/api/cloudsearch/pc?" + url.Values{"s": {keyword}, "type": {"1"}, "limit": {"10"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://music.163.com/")

	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil
	}
	var parsed struct {
		Code   int `json:"code"`
		Result struct {
			Songs []struct {
				ID       int64    `json:"id"`
				Name     string   `json:"name"`
				Alia     []string `json:"alia"`
				Tns      []string `json:"tns"`
				Duration int64    `json:"dt"`
				Artists  []struct {
					Name string `json:"name"`
				} `json:"ar"`
				Album struct {
					Name string `json:"name"`
				} `json:"al"`
			} `json:"songs"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &parsed) != nil || parsed.Code != http.StatusOK {
		return nil
	}

	var out []providerCandidate
	for _, s := range parsed.Result.Songs {
		t := strings.TrimSpace(s.Name)
		if t == "" {
			continue
		}
		var artists []string
		for _, a := range s.Artists {
			if n := strings.TrimSpace(a.Name); n != "" {
				artists = append(artists, n)
			}
		}
		aliases := append([]string(nil), s.Alia...)
		aliases = append(aliases, s.Tns...)
		idStr := ""
		if s.ID > 0 {
			idStr = strconv.FormatInt(s.ID, 10)
		}
		out = append(out, providerCandidate{
			ID:         idStr,
			Source:     "网易云 · Lyricify",
			Title:      t,
			Aliases:    aliases,
			Artists:    artists,
			Album:      strings.TrimSpace(s.Album.Name),
			DurationMs: s.Duration,
			SourceRank: 1,
		})
	}
	return out
}

func rankAlternateTitles(req Request, cands []providerCandidate) []alternateTitle {
	wantFold := lyrics.FoldTitle(req.Title)
	if wantFold == "" {
		return nil
	}
	type scored struct {
		alt        alternateTitle
		tier       int
		sourceRank int
		diff       int64
	}
	var entries []scored
	add := func(title, album, id, source string, clearAlbum bool, tier, sourceRank int, diff int64) {
		title = strings.TrimSpace(title)
		if title == "" || strings.EqualFold(title, strings.TrimSpace(req.Title)) {
			return
		}
		entries = append(entries, scored{
			alt:        alternateTitle{Title: title, Album: album, ClearAlbum: clearAlbum, ID: id, Source: source},
			tier:       tier,
			sourceRank: sourceRank,
			diff:       diff,
		})
	}

	wantArtists := strings.Join(req.Artists, " ")
	for _, c := range cands {
		var diff int64
		if req.DurationMs > 0 && c.DurationMs > 0 {
			diff = max(req.DurationMs-c.DurationMs, c.DurationMs-req.DurationMs)
			if diff > 2500 {
				continue
			}
		}
		if lyrics.HasExtraVersionTag(req.Title, req.Album, c.Title) {
			continue
		}
		artistOK := lyrics.ArtistsMatch(req.Artists, c.Artists)
		albumOK := lyrics.AlbumMatches(req.Album, c.Album)
		crossArtist := !artistOK && wantArtists != "" && len(c.Artists) > 0 &&
			lyrics.CrossScript(wantArtists, strings.Join(c.Artists, " "))

		if ok, baseTitle := lyrics.BilingualParenMatch(req.Title, c.Title); ok && artistOK {
			tier := 1
			if albumOK {
				tier = 0
			}
			add(c.Title, c.Album, c.ID, c.Source, true, tier, c.SourceRank, diff)
			if baseTitle != "" && lyrics.FoldTitle(baseTitle) != wantFold {
				add(baseTitle, c.Album, "", "", true, 2, c.SourceRank, diff)
			}
			continue
		}

		aliasMatched := false
		if artistOK {
			for _, alias := range c.Aliases {
				if f := lyrics.FoldTitle(alias); f != "" && f == wantFold {
					aliasMatched = true
					break
				}
			}
		}
		if aliasMatched {
			tier := 1
			if albumOK {
				tier = 0
			}
			add(c.Title, c.Album, c.ID, c.Source, true, tier, c.SourceRank, diff)
			continue
		}

		if req.DurationMs > 0 && c.DurationMs > 0 && lyrics.CrossScript(req.Title, c.Title) && albumOK && (artistOK || crossArtist) {
			add(c.Title, c.Album, c.ID, c.Source, artistOK, 2, c.SourceRank, diff)
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].tier != entries[j].tier {
			return entries[i].tier < entries[j].tier
		}
		if entries[i].sourceRank != entries[j].sourceRank {
			return entries[i].sourceRank < entries[j].sourceRank
		}
		return entries[i].diff < entries[j].diff
	})

	var out []alternateTitle
	var seen []string
	for _, e := range entries {
		key := strings.ToLower(e.alt.Title) + "\x00" + e.alt.ID
		if slices.Contains(seen, key) {
			continue
		}
		seen = append(seen, key)
		out = append(out, e.alt)
		if len(out) >= 5 {
			break
		}
	}
	return out
}
