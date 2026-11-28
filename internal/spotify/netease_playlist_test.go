package spotify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseNetEasePlaylistID(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"3778678", 3778678},
		{"https://music.163.com/#/playlist?id=3778678", 3778678},
		{"https://music.163.com/playlist?id=24381616&userid=12345", 24381616},
		{"https://y.music.163.com/m/playlist?id=99887766", 99887766},
		{"分享创建的歌单「深夜循环」: https://music.163.com/playlist/54321098/12345/?userid=1", 54321098},
	}
	for _, tc := range cases {
		got, err := ParseNetEasePlaylistID(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseNetEasePlaylistID(%q) = (%d, %v), want %d", tc.in, got, err, tc.want)
		}
	}
	if _, err := ParseNetEasePlaylistID("not a playlist"); err == nil {
		t.Fatal("expected error for invalid input")
	}
}

func TestNetEasePlaylistFetchesRemainingTrackIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v6/playlist/detail":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"playlist": map[string]any{
					"id":          1001,
					"name":        "测试歌单",
					"coverImgUrl": "http://p1.music.126.net/cover.jpg",
					"trackCount":  2,
					"tracks": []map[string]any{{
						"id": 10, "name": "晴天", "dt": 269000,
						"ar": []map[string]any{{"name": "周杰伦"}},
						"al": map[string]any{"name": "叶惠美", "picUrl": "http://p1.music.126.net/a.jpg"},
					}},
					"trackIds": []map[string]any{{"id": 10}, {"id": 20}},
				},
			})
		case "/api/v3/song/detail":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"songs": []map[string]any{{
					"id": 20, "name": "七里香", "dt": 299000,
					"ar": []map[string]any{{"name": "周杰伦"}},
					"al": map[string]any{"name": "七里香", "picUrl": "http://p1.music.126.net/b.jpg"},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := neteaseURL
	neteaseURL = srv.URL
	defer func() { neteaseURL = prev }()

	pl, err := NetEasePlaylist(context.Background(), "", 1001)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Name != "测试歌单" || pl.CoverURL != "https://p1.music.126.net/cover.jpg" || len(pl.Tracks) != 2 {
		t.Fatalf("unexpected playlist: %+v", pl)
	}
	if pl.Tracks[1].Name != "七里香" || pl.Tracks[1].PicURL != "https://p1.music.126.net/b.jpg" {
		t.Fatalf("missing second track from /api/v3/song/detail: %+v", pl.Tracks[1])
	}
}

func TestMatchCandidateForNetEase(t *testing.T) {
	want := NetEasePlaylistTrack{
		ID:       100,
		Name:     "晴天",
		Artists:  []string{"周杰伦"},
		Album:    "叶惠美",
		Duration: 269000,
	}
	candidates := []Track{
		{ID: "remix", Name: "晴天 (DJ版)", Artists: []Artist{{Name: "周杰伦"}}, Duration: 269000},
		{ID: "wrong-artist", Name: "晴天", Artists: []Artist{{Name: "翻唱歌手"}}, Duration: 269000},
		{ID: "exact", Name: "晴天", Artists: []Artist{{Name: "周杰倫"}}, Album: Album{Name: "葉惠美"}, Duration: 269500},
	}
	got, ok := MatchCandidateForNetEase(want, candidates, true)
	if !ok || got.ID != "exact" {
		t.Fatalf("MatchCandidateForNetEase = (%+v, %v), want exact", got, ok)
	}
}

func TestNetEaseSongMediaDirectTrialAndUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("ids") {
		case "[101]":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": []map[string]any{{
					"id":   101,
					"code": 200,
					"url":  "http://m701.music.126.net/full.mp3",
					"time": 240000,
				}},
			})
		case "[102]":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": []map[string]any{{
					"id":            102,
					"code":          200,
					"url":           "http://m701.music.126.net/trial.mp3",
					"time":          30000,
					"freeTrialInfo": map[string]any{"start": 0, "end": 30},
				}},
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": []map[string]any{{
					"id":   103,
					"code": 404,
					"url":  "",
				}},
			})
		}
	}))
	defer srv.Close()

	prev := neteaseURL
	neteaseURL = srv.URL
	defer func() { neteaseURL = prev }()

	m, err := NetEaseSongMedia(context.Background(), "", 101)
	if err != nil || m == nil || m.File != "http://m701.music.126.net/full.mp3" {
		t.Fatalf("full track = (%+v, %v)", m, err)
	}
	if _, err := NetEaseSongMedia(context.Background(), "", 102); err != ErrNetEaseTrialOnly {
		t.Fatalf("trial track err = %v, want ErrNetEaseTrialOnly", err)
	}
	if _, err := NetEaseSongMedia(context.Background(), "", 103); err != ErrNetEaseUnavailable {
		t.Fatalf("unavailable track err = %v, want ErrNetEaseUnavailable", err)
	}
}
