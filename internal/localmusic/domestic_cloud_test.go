package localmusic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Baidu Netdisk OAuth flow lists files, resolves dlink for Range metadata and
// playback with pan.baidu.com User-Agent, loads companion LRC, and uploads via
// precreate + superfile2 + create.
func TestBaiduOAuthScanResolveLyricsAndUpload(t *testing.T) {
	flacData := makeMinimalFLAC(44100, 44100*110, 256<<10)
	lrcData := []byte("[00:01.00]百度网盘歌词\n")
	var uploadedChunk []byte

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "bd-access",
				"refresh_token": "bd-refresh",
				"expires_in":    2592000,
			})
			return
		case r.URL.Path == "/rest/2.0/xpan/nas" && r.URL.Query().Get("method") == "uinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"baidu_name": "baidu_user"})
			return
		case r.URL.Path == "/rest/2.0/xpan/file" && r.URL.Query().Get("method") == "list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errno": 0,
				"list": []map[string]any{
					{"fs_id": 1001, "path": "/Music/Baidu Artist - Baidu Song.flac", "server_filename": "Baidu Artist - Baidu Song.flac", "size": len(flacData), "isdir": 0, "md5": "bm1"},
					{"fs_id": 1002, "path": "/Music/Baidu Artist - Baidu Song.lrc", "server_filename": "Baidu Artist - Baidu Song.lrc", "size": len(lrcData), "isdir": 0, "md5": "bm2"},
				},
			})
			return
		case r.URL.Path == "/rest/2.0/xpan/multimedia" && r.URL.Query().Get("method") == "filemetas":
			fsids := r.URL.Query().Get("fsids")
			if strings.Contains(fsids, "1001") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"errno": 0,
					"list":  []map[string]any{{"fs_id": 1001, "dlink": srvURL + "/dlink/1001"}},
				})
				return
			}
			if strings.Contains(fsids, "1002") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"errno": 0,
					"list":  []map[string]any{{"fs_id": 1002, "dlink": srvURL + "/dlink/1002"}},
				})
				return
			}
		case r.URL.Path == "/dlink/1001":
			if r.Header.Get("User-Agent") != baiduUA || r.URL.Query().Get("access_token") != "bd-access" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			loStr, hiStr, _ := strings.Cut(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
			lo, _ := strconv.Atoi(loStr)
			hi, _ := strconv.Atoi(hiStr)
			hi = min(hi, len(flacData)-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi, len(flacData)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(flacData[lo : hi+1])
			return
		case r.URL.Path == "/dlink/1002":
			if r.Header.Get("User-Agent") != baiduUA {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(lrcData)
			return
		case r.URL.Path == "/rest/2.0/xpan/file" && r.URL.Query().Get("method") == "precreate":
			_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "uploadid": "up-id-1"})
			return
		case r.URL.Path == "/rest/2.0/pcs/superfile2":
			f, _, err := r.FormFile("file")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			uploadedChunk, _ = io.ReadAll(f)
			f.Close()
			_ = json.NewEncoder(w).Encode(map[string]any{"md5": "ok"})
			return
		case r.URL.Path == "/rest/2.0/xpan/file" && r.URL.Query().Get("method") == "create":
			_ = json.NewEncoder(w).Encode(map[string]any{"errno": 0, "fs_id": 2001})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	srvURL = srv.URL

	prevAuth, prevTok, prevPan, prevUp := baiduAuthEndpoint, baiduTokenEndpoint, baiduPanAPIBase, baiduUploadAPIBase
	baiduAuthEndpoint = srv.URL + "/authorize"
	baiduTokenEndpoint = srv.URL + "/token"
	baiduPanAPIBase = srv.URL + "/rest/2.0"
	baiduUploadAPIBase = srv.URL + "/rest/2.0"
	defer func() {
		baiduAuthEndpoint, baiduTokenEndpoint, baiduPanAPIBase, baiduUploadAPIBase = prevAuth, prevTok, prevPan, prevUp
	}()

	res, err := AuthorizeCloudOAuth(context.Background(), CloudTypeBaidu, "bd-key", "bd-secret", func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=bd-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			if resp, err := http.Get(cb); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("AuthorizeCloudOAuth Baidu failed: %v", err)
	}
	if res.RefreshToken != "bd-refresh" || res.Email != "baidu_user" {
		t.Fatalf("unexpected Baidu oauth result: %+v", res)
	}

	src := CloudSource{
		ID:           "bd-1",
		Type:         CloudTypeBaidu,
		Name:         "我的百度网盘",
		Folder:       "/Music",
		ClientID:     "bd-key",
		ClientSecret: "bd-secret",
		RefreshToken: res.RefreshToken,
	}
	tmp := t.TempDir()
	tracks, warns := ScanCloud(context.Background(), []CloudSource{src}, filepath.Join(tmp, "covers"), filepath.Join(tmp, "idx.json"))
	if len(warns) != 0 || len(tracks) != 1 {
		t.Fatalf("ScanCloud Baidu failed: tracks=%+v warns=%v", tracks, warns)
	}
	tr := tracks[0]
	if tr.Name != "Baidu Song" || tr.ArtistNames() != "Baidu Artist" || tr.Duration != 110000 || tr.CloudURL != "baidu://bd-1/1001" {
		t.Fatalf("unexpected Baidu track: %+v", tr)
	}
	if got := Lyrics(tr); !strings.Contains(got, "百度网盘歌词") {
		t.Fatalf("unexpected Baidu lyrics: %q", got)
	}

	localFile := filepath.Join(tmp, "bd-up.flac")
	if err := os.WriteFile(localFile, []byte("bd-flac-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UploadToCloud(context.Background(), localFile, src); err != nil {
		t.Fatalf("UploadToCloud Baidu failed: %v", err)
	}
	if !bytes.Equal(uploadedChunk, []byte("bd-flac-data")) {
		t.Fatalf("Baidu upload mismatch: %q", uploadedChunk)
	}
}

// Quark Drive verifies Cookie, resolves folders, scans audio metadata over
// HTTP Range via file/download, and loads companion LRC lyrics.
func TestQuarkScanResolveAndLyrics(t *testing.T) {
	const cookie = "__pus=demo-quark-cookie"
	flacData := makeMinimalFLAC(44100, 44100*75, 256<<10)
	lrcData := []byte("[00:02.00]夸克网盘歌词\n")

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != cookie || r.Header.Get("Referer") != quarkReferer {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/1/clouddrive/file/sort":
			pdir := r.URL.Query().Get("pdir_fid")
			if pdir == "0" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"data": map[string]any{
						"list": []map[string]any{{"fid": "fid-music", "file_name": "Music", "dir": true}},
					},
					"metadata": map[string]any{"_total": 1},
				})
				return
			}
			if pdir == "fid-music" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"data": map[string]any{
						"list": []map[string]any{
							{"fid": "qsong1", "file_name": "Quark Artist - Quark Track.flac", "size": len(flacData), "dir": false, "md5": "qm1"},
							{"fid": "qlrc1", "file_name": "Quark Artist - Quark Track.lrc", "size": len(lrcData), "dir": false, "md5": "qm2"},
						},
					},
					"metadata": map[string]any{"_total": 2},
				})
				return
			}
		case r.URL.Path == "/1/clouddrive/file/download" && r.Method == http.MethodPost:
			var reqBody struct {
				FIDs []string `json:"fids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reqBody)
			if len(reqBody.FIDs) == 1 {
				fid := reqBody.FIDs[0]
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"data": []map[string]any{{"fid": fid, "download_url": srvURL + "/cdn/" + fid}},
				})
				return
			}
		case r.URL.Path == "/cdn/qsong1":
			loStr, hiStr, _ := strings.Cut(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
			lo, _ := strconv.Atoi(loStr)
			hi, _ := strconv.Atoi(hiStr)
			hi = min(hi, len(flacData)-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi, len(flacData)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(flacData[lo : hi+1])
			return
		case r.URL.Path == "/cdn/qlrc1":
			w.WriteHeader(http.StatusOK)
			w.Write(lrcData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	srvURL = srv.URL

	prevAPI := quarkAPIBase
	quarkAPIBase = srv.URL + "/1/clouddrive"
	defer func() { quarkAPIBase = prevAPI }()

	if err := TestQuarkSource(context.Background(), cookie); err != nil {
		t.Fatalf("TestQuarkSource failed: %v", err)
	}
	if err := TestQuarkSource(context.Background(), "bad-cookie"); err == nil {
		t.Fatal("expected TestQuarkSource to fail on invalid cookie")
	}

	src := CloudSource{
		ID:       "qk-1",
		Type:     CloudTypeQuark,
		Name:     "我的夸克网盘",
		Folder:   "Music",
		Password: cookie,
	}
	tmp := t.TempDir()
	tracks, warns := ScanCloud(context.Background(), []CloudSource{src}, filepath.Join(tmp, "covers"), filepath.Join(tmp, "idx.json"))
	if len(warns) != 0 || len(tracks) != 1 {
		t.Fatalf("ScanCloud Quark failed: tracks=%+v warns=%v", tracks, warns)
	}
	tr := tracks[0]
	if tr.Name != "Quark Track" || tr.ArtistNames() != "Quark Artist" || tr.Duration != 75000 || tr.CloudURL != "quark://qk-1/qsong1" {
		t.Fatalf("unexpected Quark track: %+v", tr)
	}
	if got := Lyrics(tr); !strings.Contains(got, "夸克网盘歌词") {
		t.Fatalf("unexpected Quark lyrics: %q", got)
	}
}

// 115 Drive verifies Cookie, resolves folders by name, scans audio metadata
// over HTTP Range via files/download pickcode, and loads companion LRC lyrics.
func Test115ScanResolveAndLyrics(t *testing.T) {
	const cookie = "UID=123; CID=456; SEID=789"
	flacData := makeMinimalFLAC(44100, 44100*64, 256<<10)
	lrcData := []byte("[00:01.00]115网盘歌词\n")

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != cookie {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/files":
			cid := r.URL.Query().Get("cid")
			if cid == "0" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"state": true,
					"count": 1,
					"data":  []map[string]any{{"cid": "999", "n": "Music"}},
				})
				return
			}
			if cid == "999" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"state": true,
					"count": 2,
					"data": []map[string]any{
						{"fid": "f1", "cid": "999", "n": "115 Artist - 115 Song.flac", "s": len(flacData), "pc": "pcSong1", "sha": "s1", "te": "1700000000"},
						{"fid": "f2", "cid": "999", "n": "115 Artist - 115 Song.lrc", "s": len(lrcData), "pc": "pcLrc1", "sha": "s2", "te": "1700000000"},
					},
				})
				return
			}
		case r.URL.Path == "/files/download":
			pc := r.URL.Query().Get("pickcode")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"state":    true,
				"file_url": srvURL + "/dl/" + pc,
			})
			return
		case r.URL.Path == "/dl/pcSong1":
			loStr, hiStr, _ := strings.Cut(strings.TrimPrefix(r.Header.Get("Range"), "bytes="), "-")
			lo, _ := strconv.Atoi(loStr)
			hi, _ := strconv.Atoi(hiStr)
			hi = min(hi, len(flacData)-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi, len(flacData)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(flacData[lo : hi+1])
			return
		case r.URL.Path == "/dl/pcLrc1":
			w.WriteHeader(http.StatusOK)
			w.Write(lrcData)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	srvURL = srv.URL

	prevAPI := p115APIBase
	p115APIBase = srv.URL
	defer func() { p115APIBase = prevAPI }()

	if err := Test115Source(context.Background(), cookie); err != nil {
		t.Fatalf("Test115Source failed: %v", err)
	}
	if err := Test115Source(context.Background(), "bad"); err == nil {
		t.Fatal("expected Test115Source to fail on invalid cookie")
	}

	src := CloudSource{
		ID:       "p115-1",
		Type:     CloudType115,
		Name:     "我的 115 网盘",
		Folder:   "Music",
		Password: cookie,
	}
	tmp := t.TempDir()
	tracks, warns := ScanCloud(context.Background(), []CloudSource{src}, filepath.Join(tmp, "covers"), filepath.Join(tmp, "idx.json"))
	if len(warns) != 0 || len(tracks) != 1 {
		t.Fatalf("ScanCloud 115 failed: tracks=%+v warns=%v", tracks, warns)
	}
	tr := tracks[0]
	if tr.Name != "115 Song" || tr.ArtistNames() != "115 Artist" || tr.Duration != 64000 || tr.CloudURL != "p115://p115-1/pcSong1" {
		t.Fatalf("unexpected 115 track: %+v", tr)
	}
	if got := Lyrics(tr); !strings.Contains(got, "115网盘歌词") {
		t.Fatalf("unexpected 115 lyrics: %q", got)
	}
}
