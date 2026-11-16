package spotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"vinyl/internal/lyrics"
)

// neteaseURL is NetEase Cloud Music's; tests swap it.
var neteaseURL = "https://music.163.com"

var lrcTimed = regexp.MustCompile(`(?m)^\[\d+:\d+`)

// NetEaseLyrics looks t up on NetEase Cloud Music by its name and first
// artist and gives the lyrics, timed or plain, of the song of t's title
// whose length is nearest t's, within 2 seconds: NetEase has most Chinese
// songs, of the same recordings as Spotify's, their lengths the same to the
// millisecond. Only the name, the artist and the length leave; NetEase's
// text is in simplified characters.
func NetEaseLyrics(t Track) (synced, plain string, err error) {
	lyric, _, err := neteaseLyrics(t, nil)
	switch {
	case err != nil || lyric == "":
		return "", "", err
	case lrcTimed.MatchString(lyric):
		return lyric, "", nil
	}
	return "", lyric, nil
}

// NetEaseTranslation gives t's timed lyrics on NetEase with their
// translation into Chinese, timed as they are, when it has one: most songs
// in other languages have.
func NetEaseTranslation(t Track) (synced, translation string, err error) {
	return NetEaseTranslationMatching(t, nil)
}

// NetEaseTranslationMatching behaves like NetEaseTranslation, and when match is
// non-nil also checks artist- and duration-matched cross-script candidates whose
// album name differs (such as compilations), accepting the first candidate for
// which match(synced, translation) returns true.
func NetEaseTranslationMatching(t Track, match func(synced, translation string) bool) (synced, translation string, err error) {
	lyric, tr, err := neteaseLyrics(t, match)
	if err != nil || !lrcTimed.MatchString(lyric) || !lrcTimed.MatchString(tr) {
		return "", "", err
	}
	return lyric, tr, nil
}

type neteaseSong struct {
	ID       int64    `json:"id"`
	Name     string   `json:"name"`
	Alia     []string `json:"alia"`
	Tns      []string `json:"tns"`
	Artists  []struct {
		Name string `json:"name"`
	} `json:"ar"`
	Album struct {
		Name string `json:"name"`
	} `json:"al"`
	Duration int64 `json:"dt"`
}

// neteaseLyrics gives the lyrics of t on NetEase and their translation,
// "" for music without words.
func neteaseLyrics(t Track, match func(synced, translation string) bool) (lyric, translation string, err error) {
	if t.Episode || t.Name == "" || t.Length() <= 0 {
		return "", "", nil
	}
	queries := []string{t.Name}
	if cleaned := cleanTitle(t.Name); cleaned != "" && cleaned != strings.TrimSpace(t.Name) {
		queries = append(queries, cleaned)
	}
	seen := map[int64]bool{}
	for _, q := range queries {
		ids, err := searchNetEaseCandidates(q, t, match != nil)
		if err != nil {
			return "", "", err
		}
		for _, id := range ids {
			if seen[id] {
				continue
			}
			seen[id] = true
			lrc, tr, pure, err := fetchNetEaseSongLyrics(id)
			if err != nil {
				return "", "", err
			}
			if pure {
				if match == nil {
					return "", "", nil
				}
				continue
			}
			if match == nil {
				return lrc, tr, nil
			}
			if lrcTimed.MatchString(lrc) && lrcTimed.MatchString(tr) && match(lrc, tr) {
				return lrc, tr, nil
			}
		}
	}
	return "", "", nil
}

func fetchNetEaseSongLyrics(id int64) (lyric, translation string, pureMusic bool, err error) {
	var res struct {
		Lrc struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		Tlyric struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		PureMusic bool `json:"pureMusic"`
	}
	if err := netease("/api/song/lyric?id="+strconv.FormatInt(id, 10)+"&lv=1&tv=-1", &res); err != nil {
		return "", "", false, err
	}
	if res.PureMusic {
		return "", "", true, nil
	}
	return strings.TrimSpace(res.Lrc.Lyric), strings.TrimSpace(res.Tlyric.Lyric), false, nil
}

func searchNetEaseCandidates(title string, t Track, allowUnmatchedAlbumCrossScript bool) ([]int64, error) {
	q := title
	if len(t.Artists) > 0 {
		q += " " + t.Artists[0].Name
	}
	var found struct {
		Result struct {
			Songs []neteaseSong `json:"songs"`
		} `json:"result"`
	}
	// Not search/get/web, which turns a fifth search in a few seconds
	// down for minutes.
	if err := netease("/api/cloudsearch/pc?"+url.Values{"s": {q}, "type": {"1"}, "limit": {"10"}}.Encode(), &found); err != nil {
		return nil, err
	}
	return rankNetEaseSongs(found.Result.Songs, t, allowUnmatchedAlbumCrossScript), nil
}

func rankNetEaseSongs(songs []neteaseSong, t Track, allowUnmatchedAlbumCrossScript bool) []int64 {
	var wantArtists []string
	for _, a := range t.Artists {
		if a.Name != "" {
			wantArtists = append(wantArtists, a.Name)
		}
	}
	type ranked struct {
		id   int64
		tier int
		diff int64
	}
	var cands []ranked
	for _, s := range songs {
		d := max(s.Duration-t.Duration, t.Duration-s.Duration)
		if d >= 2001 {
			continue
		}
		var gotArtists []string
		for _, a := range s.Artists {
			if a.Name != "" {
				gotArtists = append(gotArtists, a.Name)
			}
		}
		artistOK := lyrics.ArtistsMatch(wantArtists, gotArtists)
		if lyrics.HasExtraVersionTag(t.Name, t.Album.Name, s.Name) {
			continue
		}

		tier := -1
		switch {
		case sameTitle(s.Name, t.Name):
			tier = 0
		case artistOK && func() bool { ok, _ := lyrics.BilingualParenMatch(t.Name, s.Name); return ok }():
			tier = 1
		case artistOK && neteaseAliasMatches(t.Name, s.Alia, s.Tns):
			tier = 1
		case artistOK && lyrics.CrossScript(t.Name, s.Name):
			if lyrics.AlbumMatches(t.Album.Name, s.Album.Name) {
				tier = 2
			} else if allowUnmatchedAlbumCrossScript {
				tier = 3
			}
		}
		if tier >= 0 {
			cands = append(cands, ranked{id: s.ID, tier: tier, diff: d})
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].tier != cands[j].tier {
			return cands[i].tier < cands[j].tier
		}
		return cands[i].diff < cands[j].diff
	})
	var ids []int64
	limit := 1
	if allowUnmatchedAlbumCrossScript {
		limit = 3
	}
	for _, c := range cands {
		if !slices.Contains(ids, c.id) {
			ids = append(ids, c.id)
			if len(ids) >= limit {
				break
			}
		}
	}
	return ids
}

func neteaseAliasMatches(want string, alia, tns []string) bool {
	for _, list := range [][]string{alia, tns} {
		for _, item := range list {
			if sameTitle(item, want) {
				return true
			}
		}
	}
	return false
}

// netease gets path from NetEase into out, as its web page does, without
// signing in.
func netease(path string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, neteaseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Referer", "https://music.163.com/")
	res, err := Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &LyricsError{"netease", res.StatusCode}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return err
	}
	// It turns some requests down with 200 and a code of its own.
	var status struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(b, &status); err != nil {
		return err
	}
	if status.Code != http.StatusOK {
		return &LyricsError{"netease", status.Code}
	}
	return json.Unmarshal(b, out)
}

// sameTitle reports whether two titles name the same song: the same
// letters, without what is in brackets or after " - ", or, in Chinese, as
// many characters, NetEase writing in simplified ones what Spotify may in
// traditional (梁山伯與茱麗葉, 梁山伯与茱丽叶).
func sameTitle(a, b string) bool {
	a, b = foldTitle(a), foldTitle(b)
	if a == "" || b == "" {
		return false
	}
	han := func(r rune) bool { return unicode.Is(unicode.Han, r) }
	return a == b || len([]rune(a)) == len([]rune(b)) && strings.IndexFunc(a, han) >= 0 && strings.IndexFunc(b, han) >= 0
}

func cleanTitle(s string) string { return lyrics.CleanTitle(s) }
func foldTitle(s string) string  { return lyrics.FoldTitle(s) }
