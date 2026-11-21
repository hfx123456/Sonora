package spotify

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"vinyl/internal/lyrics"
)

// A song with an English catalog title can find its translation by a Japanese
// lyric fragment even when the provider combines rows Spotify splits apart.
func TestNetEaseTranslationForFindsOriginalTitleByLyric(t *testing.T) {
	old := Client
	t.Cleanup(func() { Client = old })
	track := Track{
		Name: "Rainy Morning (Movie Edit)", Duration: 156160,
		Artists: []Artist{{Name: "Example Band"}}, Album: Album{Name: "A Film"},
	}
	lines := lyrics.Build("[00:15]窓を開けたら\n[00:21]街が見えた\n[00:28]風が吹いて\n[00:34]雲が流れた", "", "")
	var requests []string
	Client = &http.Client{Transport: lyricsTransport(func(req *http.Request) (*http.Response, error) {
		var body string
		query := req.URL.Query()
		switch req.URL.Path {
		case "/api/cloudsearch/pc":
			body = `{"code":200,"result":{"songs":[]}}`
			if query.Get("type") == "1006" && query.Get("s") == "窓を開けたら" {
				body = `{"code":200,"result":{"songs":[
					{"id":1,"name":"雨の朝","ar":[{"name":"Different Artist"}],"dt":156160},
					{"id":2,"name":"雨の朝 (Live)","ar":[{"name":"Example Band"}],"dt":156160},
					{"id":3,"name":"雨の朝","ar":[{"name":"Example Band"}],"dt":247000},
					{"id":4,"name":"雨の朝","ar":[{"name":"Example Band"}],"dt":156160},
					{"id":5,"name":"雨の朝","ar":[{"name":"Example Band"}],"dt":156163}
				]}}`
			}
		case "/api/song/lyric":
			id := query.Get("id")
			requests = append(requests, id)
			switch id {
			case "4":
				body = `{"code":200,"lrc":{"lyric":"[00:15]窓を開けたら\n[00:21]雨が降っていた\n[00:28]夜は静かだった"},"tlyric":{"lyric":"[00:15]打开窗"}}`
			case "5":
				body = `{"code":200,"lrc":{"lyric":"[00:15]窓を開けたら、街が見えた\n[00:28]風が吹いて 雲が流れた"},"tlyric":{"lyric":"[00:15]推开窗便看见街道\n[00:28]风吹云动"}}`
			default:
				t.Fatalf("fetched mismatched song %s", id)
			}
		default:
			t.Fatalf("unexpected path %s", req.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}

	synced, translation, err := NetEaseTranslationFor(track, lines)
	got := lyrics.Build(synced, translation, "")
	if err != nil || len(got) != 2 || got[0].Translation != "推开窗便看见街道" || strings.Join(requests, ",") != "4,5" {
		t.Fatalf("requests=%v lyrics=%+v err=%v", requests, got, err)
	}
}

// NetEase is asked by name and artist only, and of its songs the one of
// the same title nearest in length gives the lyrics: not a cover a little
// shorter, a remix, or another song as long.
func TestNetEaseLyrics(t *testing.T) {
	old := Client
	defer func() { Client = old }()
	track := Track{Name: "梁山伯與茱麗葉", Artists: []Artist{{Name: "卓文萱"}}, Duration: 232896}
	songs := `{"code":200,"result":{"songs":[{"id":1,"name":"梁山伯与茱丽叶(Remix版)","dt":205213},{"id":2,"name":"梁山伯与茱丽叶（cover：曹格 卓文萱）","dt":230991},{"id":4,"name":"我的歌","dt":232896},{"id":3,"name":"梁山伯与茱丽叶","dt":232896}]}}`
	for _, c := range []struct {
		name, search, lyric string
		synced, plain       bool
		asked               string
		err                 error
	}{
		{"timed", songs, `{"code":200,"lrc":{"lyric":"[00:00.000] 作词 : 曹格\n[00:16.939]我的心想唱首歌给你听"}}`, true, false, "3", nil},
		{"plain", songs, `{"code":200,"lrc":{"lyric":"我的心想唱首歌给你听\n歌词是如此的甜蜜"}}`, false, true, "3", nil},
		{"pure", songs, `{"code":200,"pureMusic":true,"lrc":{"lyric":""}}`, false, false, "3", nil},
		{"far", `{"code":200,"result":{"songs":[{"id":1,"name":"梁山伯与茱丽叶","dt":240000}]}}`, "", false, false, "", nil},
		{"asked-too-often", `{"code":405,"message":"操作频繁"}`, "", false, false, "", &LyricsError{"netease", 405}},
		{"refused", `{"code":-460,"message":"Cheating"}`, "", false, false, "", &LyricsError{"netease", -460}},
	} {
		asked := ""
		Client = &http.Client{Transport: lyricsTransport(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "music.163.com" || req.Header.Get("Referer") != "https://music.163.com/" || req.Header.Get("Authorization") != "" || req.Header.Get("client-token") != "" {
				t.Fatal("not as NetEase's page asks, or with Spotify's credentials")
			}
			body := c.search
			switch req.URL.Path {
			case "/api/cloudsearch/pc":
				if req.URL.Query().Get("s") != "梁山伯與茱麗葉 卓文萱" {
					t.Fatalf("searched %q", req.URL.Query().Get("s"))
				}
			case "/api/song/lyric":
				asked, body = req.URL.Query().Get("id"), c.lyric
			default:
				t.Fatal("asked", req.URL.Path)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		synced, plain, err := NetEaseLyrics(track)
		var le *LyricsError
		if (synced != "") != c.synced || (plain != "") != c.plain || asked != c.asked || (c.err == nil) != (err == nil) || c.err != nil && (!errors.As(err, &le) || *le != *c.err.(*LyricsError)) {
			t.Errorf("%s: synced %q plain %q, lyrics of %q, err %v", c.name, synced, plain, asked, err)
		}
	}
}

func TestSameTitle(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"梁山伯與茱麗葉", "梁山伯与茱丽叶", true},
		{"無緣", "无缘", true},
		{"周旋", "周旋 (Live)", true},
		{"Drenched", "drenched", true},
		{"Song - Remastered 2011", "Song", true},
		{"周旋", "旋转木马", false},
		{"七里香", "晴天啊", false},
		{"爱我", "恨你", false},
		{"Rain", "Pain", false},
		{"(Intro)", "(Intro)", false},
	} {
		if got := sameTitle(c.a, c.b); got != c.same {
			t.Errorf("sameTitle(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestParseIdentityZeroExpiresAndSignOutEpoch(t *testing.T) {
	id, ok := ParseIdentity(`{"token":{"accessToken":"tok-zero","accessTokenExpirationTimestampMs":0,"isAnonymous":false}}`)
	if !ok || id.Token != "tok-zero" || id.Expires.IsZero() {
		t.Fatalf("ParseIdentity zero expires failed: ok=%v id=%+v", ok, id)
	}
	SignOut()
	defer SignOut()
	started, release := make(chan struct{}), make(chan struct{})
	oldRenew := Renew
	Renew = func() (Identity, error) {
		close(started)
		<-release
		return Identity{Token: "stale-account", Expires: id.Expires}, nil
	}
	defer func() { Renew = oldRenew }()

	done := make(chan Identity, 1)
	go func() {
		got, _ := RenewedIdentity()
		done <- got
	}()
	<-started
	SignOut()
	close(release)
	if got := <-done; got.Token != "" {
		t.Fatalf("SignOut allowed in-flight Renew to resurrect token %q", got.Token)
	}
}

// NetEase asked too often, or a site busy, may answer later; a lookup
// turned down will not.
func TestLyricsErrorTemporary(t *testing.T) {
	for _, c := range []struct {
		err  LyricsError
		want bool
	}{
		{LyricsError{"lrclib", 503}, true},
		{LyricsError{"lrclib", 429}, true},
		{LyricsError{"netease", 405}, true},
		{LyricsError{"netease", 406}, true},
		{LyricsError{"lrclib", 400}, false},
		{LyricsError{"lrclib", 405}, false},
		{LyricsError{"netease", -460}, false},
	} {
		if got := c.err.Temporary(); got != c.want {
			t.Errorf("%v: temporary %v", c.err, got)
		}
	}
}

// The translation comes, timed as the lyrics are, only with timed lyrics.
func TestNetEaseTranslation(t *testing.T) {
	old := Client
	defer func() { Client = old }()
	track := Track{Name: "Shape of You", Artists: []Artist{{Name: "Ed Sheeran"}}, Duration: 233712}
	for _, c := range []struct {
		name, lyric string
		want        bool
	}{
		{"translated", `{"code":200,"lrc":{"lyric":"[00:09.40]The club isn't the best place"},"tlyric":{"lyric":"[00:09.40]俱乐部不是最好的地方"}}`, true},
		{"none", `{"code":200,"lrc":{"lyric":"[00:09.40]The club isn't the best place"},"tlyric":{"lyric":""}}`, false},
		{"plain", `{"code":200,"lrc":{"lyric":"The club isn't the best place"},"tlyric":{"lyric":"俱乐部不是最好的地方"}}`, false},
	} {
		Client = &http.Client{Transport: lyricsTransport(func(req *http.Request) (*http.Response, error) {
			body := `{"code":200,"result":{"songs":[{"id":1,"name":"Shape of You","dt":233712}]}}`
			if req.URL.Path == "/api/song/lyric" {
				if req.URL.Query().Get("tv") != "-1" {
					t.Fatal("translation not asked for")
				}
				body = c.lyric
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		synced, translation, err := NetEaseTranslation(track)
		if err != nil || (synced != "" && translation != "") != c.want || (synced == "") != (translation == "") {
			t.Errorf("%s: %q %q %v", c.name, synced, translation, err)
		}
	}
}

// A title with "(feat. ...)" or " - ..." that misses NetEase's search falls
// back to the cleaned title so translated lyrics are still found.
func TestNetEaseTranslationFallsBackToCleanTitle(t *testing.T) {
	old := Client
	defer func() { Client = old }()
	track := Track{Name: "Echo (feat. blackbear)", Artists: []Artist{{Name: "Maroon 5"}, {Name: "blackbear"}}, Duration: 178620}
	var queries []string
	Client = &http.Client{Transport: lyricsTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/cloudsearch/pc":
			q := req.URL.Query().Get("s")
			queries = append(queries, q)
			if q == "Echo Maroon 5" {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"result":{"songs":[{"id":1852069343,"name":"Echo","dt":178620}]}}`))}, nil
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"result":{"songs":[{"id":1,"name":"Other","dt":200000}]}}`))}, nil
		case "/api/song/lyric":
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"lrc":{"lyric":"[00:07.313]You got me"},"tlyric":{"lyric":"[00:07.313]你点燃了我"}}`))}, nil
		}
		t.Fatalf("unexpected path %s", req.URL.Path)
		return nil, nil
	})}
	synced, tr, err := NetEaseTranslation(track)
	if err != nil || synced == "" || tr == "" {
		t.Fatalf("got synced=%q tr=%q err=%v", synced, tr, err)
	}
	if len(queries) != 2 || queries[0] != "Echo (feat. blackbear) Maroon 5" || queries[1] != "Echo Maroon 5" {
		t.Fatalf("queries=%v", queries)
	}
}

// A Romanized title such as "Haru Haru" must match NetEase's original Korean
// entry "하루하루" when artist, album, and duration align, skipping Japanese or
// live variants; when album differs (compilation), NetEaseTranslationMatching
// still checks cross-script candidates via the caller's lyric matcher.
func TestNetEaseCrossScriptAndAlias(t *testing.T) {
	old := Client
	defer func() { Client = old }()
	songs := `{"code":200,"result":{"songs":[` +
		`{"id":22673344,"name":"Haru Haru (Japanese Version)","ar":[{"name":"BIGBANG"}],"al":{"name":"THE BEST OF BIGBANG"},"dt":255773},` +
		`{"id":999,"name":"Other Song","ar":[{"name":"BIGBANG"}],"al":{"name":"Stand Up"},"dt":256360},` +
		`{"id":22673504,"name":"하루하루","alia":["一天一天"],"ar":[{"name":"BIGBANG"}],"al":{"name":"Stand Up"},"dt":256360}` +
		`]}}`
	Client = &http.Client{Transport: lyricsTransport(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Path {
		case "/api/cloudsearch/pc":
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(songs))}, nil
		case "/api/song/lyric":
			switch req.URL.Query().Get("id") {
			case "22673504":
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"lrc":{"lyric":"[00:01.08]떠나가"},"tlyric":{"lyric":"[00:01.08]离开吧"}}`))}, nil
			case "22673344":
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"lrc":{"lyric":"[00:01.08]Japanese"},"tlyric":{"lyric":"[00:01.08]日语"}}`))}, nil
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"code":200,"lrc":{"lyric":""},"tlyric":{"lyric":""}}`))}, nil
	})}

	standUp := Track{Name: "Haru Haru", Artists: []Artist{{Name: "BIGBANG"}}, Album: Album{Name: "Stand Up"}, Duration: 256359}
	synced, tr, err := NetEaseTranslation(standUp)
	if err != nil || !strings.Contains(synced, "떠나가") || !strings.Contains(tr, "离开吧") {
		t.Fatalf("cross-script album match failed: synced=%q tr=%q err=%v", synced, tr, err)
	}

	compilation := Track{Name: "Haru Haru", Artists: []Artist{{Name: "BIGBANG"}}, Album: Album{Name: "Number 1"}, Duration: 256359}
	if s, _, _ := NetEaseTranslation(compilation); s != "" {
		t.Fatalf("unexpected unverified cross-script match on compilation album: %q", s)
	}
	synced, tr, err = NetEaseTranslationMatching(compilation, func(s, _ string) bool {
		return strings.Contains(s, "떠나가")
	})
	if err != nil || !strings.Contains(synced, "떠나가") || !strings.Contains(tr, "离开吧") {
		t.Fatalf("verified cross-script compilation match failed: synced=%q tr=%q err=%v", synced, tr, err)
	}
}
