package fusion

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
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

type lrclibCandidate struct {
	ID           int64   `json:"id"`
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	AlbumName    string  `json:"albumName"`
	Duration     float64 `json:"duration"`
	SyncedLyrics string  `json:"syncedLyrics"`
	PlainLyrics  string  `json:"plainLyrics"`
}

// runNativeBridge executes the full lyrics lookup and parsing pipeline purely
// in Go, replacing the legacy embedded .NET Lyricify-Lyrics-Helper subprocess.
func runNativeBridge(ctx context.Context, _ string, req Request) ([]byte, Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, Result{}, err
	}

	var local Result
	if strings.TrimSpace(req.Raw) != "" {
		local = convertLyrics(req.Raw, req.Translation, "本地歌词")
		hasTrans := hasRealTranslation(local.Lines)
		hasSynced := false
		hasZh := false
		for _, ln := range local.Lines {
			if !ln.Unsynced {
				hasSynced = true
			}
			if isChineseText(ln.Text) {
				hasZh = true
			}
		}
		if hasTrans || (hasSynced && hasZh) || req.ParseOnly {
			if len(local.Lines) == 0 {
				return nil, Result{}, errors.New("未找到匹配歌词，可载入本地双语歌词文件")
			}
			return encodeResult(local), local, nil
		}
	}

	if strings.TrimSpace(req.Title) == "" {
		if len(local.Lines) > 0 {
			return encodeResult(local), local, nil
		}
		return nil, Result{}, errors.New("未找到匹配歌词，可载入本地双语歌词文件")
	}

	client := httpClientForProxy(normalizeProxy(req.Proxy))
	var (
		wg  sync.WaitGroup
		ne  Result
		qq  Result
		lrc Result
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		ne = searchProviderLyrics(ctx, client, "netease", req)
	}()
	go func() {
		defer wg.Done()
		qq = searchProviderLyrics(ctx, client, "qq", req)
	}()
	go func() {
		defer wg.Done()
		lrc = searchLRCLIBLyrics(ctx, client, req)
	}()
	wg.Wait()

	best := pickBestResult([]Result{ne, qq, lrc, local})
	if len(best.Lines) == 0 {
		return nil, Result{}, errors.New("未找到匹配歌词，可载入本地双语歌词文件")
	}
	return encodeResult(best), best, nil
}

func normalizeProxy(proxy string) string {
	if strings.HasPrefix(strings.ToLower(proxy), "socks5h://") {
		return "socks5://" + proxy[len("socks5h://"):]
	}
	return proxy
}

func buildQueries(req Request) []string {
	title := strings.TrimSpace(req.Title)
	artist := strings.TrimSpace(strings.Join(req.Artists, " "))
	album := strings.TrimSpace(req.Album)
	raw := []string{
		strings.TrimSpace(title + " " + artist + " " + album),
		strings.TrimSpace(title + " " + album),
		strings.TrimSpace(title + " " + artist),
	}
	var out []string
	for _, q := range raw {
		if q != "" && !slices.Contains(out, q) {
			out = append(out, q)
		}
	}
	return out
}

func searchProviderLyrics(ctx context.Context, client *http.Client, provider string, req Request) Result {
	queries := buildQueries(req)
	if len(queries) == 0 {
		return Result{}
	}
	qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	batches := make([][]providerCandidate, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(idx int, kw string) {
			defer wg.Done()
			if provider == "qq" {
				batches[idx] = searchQQMusicCandidates(qctx, client, kw)
			} else {
				batches[idx] = searchNetEaseCandidates(qctx, client, kw)
			}
		}(i, q)
	}
	wg.Wait()

	seen := map[string]bool{}
	type scoredCand struct {
		cand  providerCandidate
		score int
	}
	var accepted []scoredCand
	for _, batch := range batches {
		for _, cand := range batch {
			if !acceptCandidate(req, cand) {
				continue
			}
			key := cand.ID
			if key == "" {
				key = cand.Title + "\x00" + strings.Join(cand.Artists, ",") + "\x00" + cand.Album
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			accepted = append(accepted, scoredCand{cand: cand, score: scoreCandidate(req, cand)})
		}
	}
	if len(accepted) == 0 {
		return Result{}
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		return accepted[i].score > accepted[j].score
	})
	if len(accepted) > 3 {
		accepted = accepted[:3]
	}

	fctx, fcancel := context.WithTimeout(ctx, 9*time.Second)
	defer fcancel()
	results := make([]Result, len(accepted))
	var fwg sync.WaitGroup
	for i, sc := range accepted {
		fwg.Add(1)
		go func(idx int, c providerCandidate) {
			defer fwg.Done()
			if provider == "qq" {
				if raw, tr, ok := fetchQQMusicLyricsByID(fctx, client, c.ID); ok {
					results[idx] = convertLyrics(raw, tr, "QQ 音乐")
				}
			} else {
				results[idx] = fetchNetEaseBestResult(fctx, client, c.ID)
			}
		}(i, sc.cand)
	}
	fwg.Wait()
	return pickBestResult(results)
}

func fetchNetEaseBestResult(ctx context.Context, client *http.Client, songID string) Result {
	if songID == "" {
		return Result{}
	}
	endpoint := "https://music.163.com/api/song/lyric?id=" + url.QueryEscape(songID) + "&lv=1&tv=-1&yv=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Result{}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://music.163.com/")
	res, err := client.Do(req)
	if err != nil {
		return Result{}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return Result{}
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return Result{}
	}
	var parsed struct {
		Code      int  `json:"code"`
		PureMusic bool `json:"pureMusic"`
		Lrc       struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		Tlyric struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		Yrc struct {
			Lyric string `json:"lyric"`
		} `json:"yrc"`
		Ytlrc struct {
			Lyric string `json:"lyric"`
		} `json:"ytlrc"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Code != http.StatusOK || parsed.PureMusic {
		return Result{}
	}
	var variants []Result
	if yrc := strings.TrimSpace(parsed.Yrc.Lyric); hasSubstantiveYRC(yrc) {
		tr := strings.TrimSpace(parsed.Ytlrc.Lyric)
		if tr == "" {
			tr = strings.TrimSpace(parsed.Tlyric.Lyric)
		}
		variants = append(variants, convertLyrics(yrc, tr, "网易云"))
	}
	if lrc := strings.TrimSpace(parsed.Lrc.Lyric); lrc != "" {
		tr := strings.TrimSpace(parsed.Tlyric.Lyric)
		if tr == "" {
			tr = strings.TrimSpace(parsed.Ytlrc.Lyric)
		}
		variants = append(variants, convertLyrics(lrc, tr, "网易云"))
	}
	return pickBestResult(variants)
}

func searchLRCLIBLyrics(ctx context.Context, client *http.Client, req Request) Result {
	queries := buildQueries(req)
	if len(queries) == 0 {
		return Result{}
	}
	qctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	batches := make([][]lrclibCandidate, len(queries))
	var wg sync.WaitGroup
	for i, q := range queries {
		wg.Add(1)
		go func(idx int, kw string) {
			defer wg.Done()
			batches[idx] = queryLRCLIB(qctx, client, kw)
		}(i, q)
	}
	wg.Wait()

	seen := map[int64]bool{}
	type scoredLrc struct {
		item  lrclibCandidate
		score int
	}
	var accepted []scoredLrc
	for _, batch := range batches {
		for _, item := range batch {
			if seen[item.ID] {
				continue
			}
			cand := providerCandidate{
				ID:         strconv.FormatInt(item.ID, 10),
				Source:     "LRCLIB",
				Title:      item.TrackName,
				Artists:    splitArtists(item.ArtistName),
				Album:      item.AlbumName,
				DurationMs: int64(math.Round(item.Duration * 1000)),
			}
			if !acceptCandidate(req, cand) {
				continue
			}
			seen[item.ID] = true
			accepted = append(accepted, scoredLrc{item: item, score: scoreCandidate(req, cand)})
		}
	}
	if len(accepted) == 0 {
		return Result{}
	}
	sort.SliceStable(accepted, func(i, j int) bool {
		return accepted[i].score > accepted[j].score
	})
	if len(accepted) > 3 {
		accepted = accepted[:3]
	}
	var results []Result
	for _, sc := range accepted {
		raw := strings.TrimSpace(sc.item.SyncedLyrics)
		if raw == "" {
			raw = strings.TrimSpace(sc.item.PlainLyrics)
		}
		if raw != "" {
			results = append(results, convertLyrics(raw, "", "LRCLIB"))
		}
	}
	return pickBestResult(results)
}

func queryLRCLIB(ctx context.Context, client *http.Client, keyword string) []lrclibCandidate {
	endpoint := "https://lrclib.net/api/search?q=" + url.QueryEscape(keyword)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Sonora/1.0 (https://github.com/hfx123456/Sonora)")
	res, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return nil
	}
	var out []lrclibCandidate
	if json.Unmarshal(body, &out) != nil {
		return nil
	}
	return out
}

func splitArtists(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '、' || r == '/' || r == '&' || r == ';'
	})
	var out []string
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// acceptCandidate ports BridgePolicy.Accept: validates duration (<=2.5s),
// rejects unrequested version tags (Live/Remix/Japanese/etc.), and supports
// cross-script artist/title and bilingual parenthetical matches.
func acceptCandidate(req Request, cand providerCandidate) bool {
	if req.DurationMs > 0 && cand.DurationMs > 0 && absInt64(req.DurationMs-cand.DurationMs) > 2500 {
		return false
	}
	if lyrics.HasExtraVersionTag(req.Title, req.Album, cand.Title) {
		return false
	}

	titleScore := nameMatchLevel(req.Title, cand.Title)
	artistMatch := lyrics.ArtistsMatch(req.Artists, cand.Artists)
	wantArtist := strings.TrimSpace(strings.Join(req.Artists, " "))
	gotArtist := strings.TrimSpace(strings.Join(cand.Artists, " "))

	if titleScore >= 2 {
		if artistMatch {
			return true
		}
		albumScore := nameMatchLevel(req.Album, cand.Album)
		return titleScore >= 3 && albumScore >= 3 &&
			req.DurationMs > 0 && cand.DurationMs > 0 &&
			wantArtist != "" && gotArtist != "" &&
			lyrics.CrossScript(wantArtist, gotArtist)
	}

	if !artistMatch {
		return false
	}
	if ok, _ := lyrics.BilingualParenMatch(req.Title, cand.Title); ok {
		return true
	}
	return req.DurationMs > 0 && cand.DurationMs > 0 &&
		lyrics.AlbumMatches(req.Album, cand.Album) &&
		strings.TrimSpace(req.Title) != "" && strings.TrimSpace(cand.Title) != "" &&
		lyrics.CrossScript(req.Title, cand.Title)
}

// nameMatchLevel returns 0 (no match), 2 (high), 3 (very high), 4 (perfect).
func nameMatchLevel(want, got string) int {
	w := strings.TrimSpace(want)
	g := strings.TrimSpace(got)
	if w == "" || g == "" {
		return 0
	}
	if strings.EqualFold(w, g) {
		return 4
	}
	wf := lyrics.FoldTitle(w)
	gf := lyrics.FoldTitle(g)
	if wf == "" || gf == "" {
		return 0
	}
	if wf == gf {
		return 4
	}
	wc := lyrics.FoldTitle(lyrics.CleanTitle(w))
	gc := lyrics.FoldTitle(lyrics.CleanTitle(g))
	if wc != "" && gc != "" && wc == gc {
		return 3
	}
	if strings.Contains(gf, wf) || strings.Contains(wf, gf) {
		shorter := min(len([]rune(wf)), len([]rune(gf)))
		longer := max(len([]rune(wf)), len([]rune(gf)))
		if shorter >= 2 && float64(shorter)/float64(longer) >= 0.6 {
			return 2
		}
	}
	return 0
}

func scoreCandidate(req Request, cand providerCandidate) int {
	score := nameMatchLevel(req.Title, cand.Title) * 10
	if lyrics.ArtistsMatch(req.Artists, cand.Artists) {
		score += 20
	}
	if lyrics.AlbumMatches(req.Album, cand.Album) {
		score += 10
	}
	if req.DurationMs > 0 && cand.DurationMs > 0 {
		diff := absInt64(req.DurationMs - cand.DurationMs)
		if diff <= 1000 {
			score += 5
		} else if diff <= 2500 {
			score += 2
		}
	}
	return score
}

func pickBestResult(results []Result) Result {
	var best Result
	for _, r := range results {
		if len(r.Lines) == 0 {
			continue
		}
		if len(best.Lines) == 0 || betterResult(r, best) {
			best = r
		}
	}
	return best
}

func encodeResult(res Result) []byte {
	var reply response
	reply.Source = res.Source
	for _, ln := range res.Lines {
		item := struct {
			At          int64  `json:"at"`
			End         int64  `json:"end"`
			Text        string `json:"text"`
			Translation string `json:"translation"`
			Unsynced    bool   `json:"unsynced"`
			Words       []struct {
				Text  string `json:"text"`
				Start int64  `json:"start"`
				End   int64  `json:"end"`
			} `json:"words"`
		}{
			At:          ln.At.Milliseconds(),
			End:         ln.End.Milliseconds(),
			Text:        ln.Text,
			Translation: ln.Translation,
			Unsynced:    ln.Unsynced,
		}
		for _, w := range ln.Words {
			item.Words = append(item.Words, struct {
				Text  string `json:"text"`
				Start int64  `json:"start"`
				End   int64  `json:"end"`
			}{
				Text:  w.Text,
				Start: w.Start.Milliseconds(),
				End:   w.End.Milliseconds(),
			})
		}
		reply.Lines = append(reply.Lines, item)
	}
	raw, _ := json.Marshal(reply)
	return raw
}
