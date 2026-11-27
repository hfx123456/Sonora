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
	"sync/atomic"
	"testing"

	"vinyl/internal/spotify"
)

// Cached tracks from before per-source URLs must keep the account recorded in the index.
func TestCachedOAuthTracksKeepTheirOwnSource(t *testing.T) {
	first := CloudSource{ID: "first", Type: CloudTypeOneDrive}
	second := CloudSource{ID: "second", Type: CloudTypeOneDrive}
	index := &cloudIndexCache{Entries: map[string]cloudIndexEntry{}}
	for _, source := range []CloudSource{first, second} {
		audio := oauthVirtualURL(source.Type, "", source.ID+"-song")
		lrc := oauthVirtualURL(source.Type, "", source.ID+"-lyrics")
		index.Entries[audio] = cloudIndexEntry{SourceID: source.ID, Track: spotify.Track{
			ID: source.ID, CloudURL: audio, CloudLyricsURL: lrc,
		}}
	}
	cachePath := filepath.Join(t.TempDir(), "index.json")
	saveCloudIndex(cachePath, index)
	tracks := LoadCachedCloud(cachePath, []CloudSource{first, second})
	if len(tracks) != 2 {
		t.Fatalf("cached cloud tracks: %d", len(tracks))
	}
	for _, track := range tracks {
		if track.CloudURL != "onedrive://"+track.ID+"/"+track.ID+"-song" ||
			track.CloudLyricsURL != "onedrive://"+track.ID+"/"+track.ID+"-lyrics" {
			t.Errorf("track %s lost its source: %s, %s", track.ID, track.CloudURL, track.CloudLyricsURL)
		}
	}
}

// OneDrive OAuth PKCE flow exchanges code for refresh_token, scans nested folders
// via Range without full downloads, loads companion LRC, and uploads files.
func TestOneDriveOAuthScanRangeLyricsAndUpload(t *testing.T) {
	flacData := makeMinimalFLAC(44100, 44100*95, 256<<10)
	lrcData := []byte("[00:02.00]OneDrive 歌词\n")
	var uploaded []byte
	var rangeCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token" && r.Method == http.MethodPost:
			_ = r.ParseForm()
			if r.FormValue("client_id") != "od-client" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "od-access-1",
				"refresh_token": "od-refresh-1",
				"expires_in":    3600,
			})
			return
		case r.URL.Path == "/v1.0/me":
			if r.Header.Get("Authorization") != "Bearer od-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"displayName": "Alice",
				"mail":        "alice@outlook.com",
			})
			return
		case strings.HasSuffix(r.URL.Path, "/me/drive/root:/Music:/children"):
			if r.Header.Get("Authorization") != "Bearer od-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"value": []map[string]any{
					{"id": "song1", "name": "OD Artist - OD Track.flac", "size": len(flacData), "eTag": "e1"},
					{"id": "lrc1", "name": "OD Artist - OD Track.lrc", "size": len(lrcData), "eTag": "e2"},
				},
			})
			return
		case r.URL.Path == "/v1.0/me/drive/items/song1/content":
			if r.Header.Get("Authorization") != "Bearer od-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			rg := r.Header.Get("Range")
			if rg == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			rangeCalls.Add(1)
			loStr, hiStr, _ := strings.Cut(strings.TrimPrefix(rg, "bytes="), "-")
			lo, _ := strconv.Atoi(loStr)
			hi, _ := strconv.Atoi(hiStr)
			hi = min(hi, len(flacData)-1)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi, len(flacData)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(flacData[lo : hi+1])
			return
		case r.URL.Path == "/v1.0/me/drive/items/lrc1/content":
			if r.Header.Get("Authorization") != "Bearer od-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(lrcData)
			return
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, ":/content"):
			if r.Header.Get("Authorization") != "Bearer od-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "up1"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	prevAuth, prevToken, prevAPI := oneDriveAuthEndpoint, oneDriveTokenEndpoint, oneDriveAPIBase
	oneDriveAuthEndpoint = srv.URL + "/authorize"
	oneDriveTokenEndpoint = srv.URL + "/token"
	oneDriveAPIBase = srv.URL + "/v1.0"
	defer func() {
		oneDriveAuthEndpoint, oneDriveTokenEndpoint, oneDriveAPIBase = prevAuth, prevToken, prevAPI
	}()

	res, err := AuthorizeCloudOAuth(context.Background(), CloudTypeOneDrive, "od-client", "", func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		redirect := q.Get("redirect_uri")
		// Browser requests other than the valid callback must leave authorization pending.
		for _, path := range []string{"/apple-touch-icon.png", "/?state=unrelated"} {
			resp, err := http.Get(redirect + path)
			if err != nil {
				return err
			}
			resp.Body.Close()
		}
		cb := redirect + "?code=test-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			resp, err := http.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("AuthorizeCloudOAuth failed: %v", err)
	}
	if res.RefreshToken != "od-refresh-1" || res.Email != "alice@outlook.com" {
		t.Fatalf("unexpected oauth result: %+v", res)
	}

	src := CloudSource{
		ID:           "od-1",
		Type:         CloudTypeOneDrive,
		Name:         "我的 OneDrive",
		Folder:       "/Music",
		ClientID:     "od-client",
		RefreshToken: res.RefreshToken,
	}
	tmp := t.TempDir()
	tracks, warns := ScanCloud(context.Background(), []CloudSource{src}, filepath.Join(tmp, "covers"), filepath.Join(tmp, "idx.json"))
	if len(warns) != 0 || len(tracks) != 1 {
		t.Fatalf("ScanCloud OneDrive failed: tracks=%+v warns=%v", tracks, warns)
	}
	tr := tracks[0]
	if tr.Name != "OD Track" || tr.ArtistNames() != "OD Artist" || tr.Duration != 95000 || rangeCalls.Load() == 0 {
		t.Fatalf("unexpected OneDrive track: %+v rangeCalls=%d", tr, rangeCalls.Load())
	}
	if got := Lyrics(tr); !strings.Contains(got, "OneDrive 歌词") {
		t.Fatalf("unexpected OneDrive lyrics: %q", got)
	}
	spotify.SetLocalTracks(tracks)
	t.Cleanup(func() { spotify.SetLocalTracks(nil) })
	if matched, ok := spotify.MatchLocal("spotify:local:OD%20Artist::OD%20Track:95"); !ok || matched.CloudURL != tr.CloudURL {
		t.Fatalf("OneDrive track did not match spotify:local URI: ok=%v", ok)
	}

	localFile := filepath.Join(tmp, "upload.flac")
	if err := os.WriteFile(localFile, []byte("od-flac"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UploadToCloud(context.Background(), localFile, src); err != nil {
		t.Fatalf("UploadToCloud OneDrive failed: %v", err)
	}
	if !bytes.Equal(uploaded, []byte("od-flac")) {
		t.Fatalf("OneDrive upload mismatch: %q", uploaded)
	}
}

// Google Drive OAuth PKCE flow resolves folder name, scans via Range, loads
// companion LRC, and supports resumable upload.
func TestGoogleDriveOAuthScanRangeLyricsAndUpload(t *testing.T) {
	flacData := makeMinimalFLAC(48000, 48000*80, 256<<10)
	lrcData := []byte("[00:03.00]Google Drive 歌词\n")
	var uploaded []byte

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token" && r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "gd-access-1",
				"refresh_token": "gd-refresh-1",
				"expires_in":    3600,
			})
			return
		case r.URL.Path == "/userinfo":
			_ = json.NewEncoder(w).Encode(map[string]any{"email": "bob@gmail.com", "name": "Bob"})
			return
		case r.URL.Path == "/drive/v3/files" && r.Method == http.MethodGet:
			if r.Header.Get("Authorization") != "Bearer gd-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			q := r.URL.Query().Get("q")
			if strings.Contains(q, "name = 'Music'") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{{"id": "folder-music", "name": "Music"}},
				})
				return
			}
			if strings.Contains(q, "'folder-music' in parents") {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"files": []map[string]any{
						{"id": "gf1", "name": "GD Artist - GD Song.flac", "mimeType": "audio/flac", "size": strconv.Itoa(len(flacData)), "md5Checksum": "m1"},
						{"id": "gl1", "name": "GD Artist - GD Song.lrc", "mimeType": "text/plain", "size": strconv.Itoa(len(lrcData)), "md5Checksum": "m2"},
					},
				})
				return
			}
		case r.URL.Path == "/drive/v3/files/gf1" && r.URL.Query().Get("alt") == "media":
			if r.Header.Get("Authorization") != "Bearer gd-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
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
		case r.URL.Path == "/drive/v3/files/gl1" && r.URL.Query().Get("alt") == "media":
			if r.Header.Get("Authorization") != "Bearer gd-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(lrcData)
			return
		case r.URL.Path == "/upload/drive/v3/files" && r.Method == http.MethodPost:
			if r.Header.Get("Authorization") != "Bearer gd-access-1" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Location", srvURL+"/upload-session/1")
			w.WriteHeader(http.StatusOK)
			return
		case r.URL.Path == "/upload-session/1" && r.Method == http.MethodPut:
			uploaded, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "created-1"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	srvURL = srv.URL

	prevAuth, prevToken, prevAPI, prevUp, prevUser := googleAuthEndpoint, googleTokenEndpoint, googleDriveAPIBase, googleUploadAPIBase, googleUserInfoURL
	googleAuthEndpoint = srv.URL + "/authorize"
	googleTokenEndpoint = srv.URL + "/token"
	googleDriveAPIBase = srv.URL + "/drive/v3"
	googleUploadAPIBase = srv.URL + "/upload/drive/v3"
	googleUserInfoURL = srv.URL + "/userinfo"
	defer func() {
		googleAuthEndpoint, googleTokenEndpoint, googleDriveAPIBase, googleUploadAPIBase, googleUserInfoURL = prevAuth, prevToken, prevAPI, prevUp, prevUser
	}()

	res, err := AuthorizeCloudOAuth(context.Background(), CloudTypeGoogleDrive, "gd-client", "gd-secret", func(authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		cb := q.Get("redirect_uri") + "?code=gd-code&state=" + url.QueryEscape(q.Get("state"))
		go func() {
			resp, err := http.Get(cb)
			if err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatalf("AuthorizeCloudOAuth Google Drive failed: %v", err)
	}
	if res.RefreshToken != "gd-refresh-1" || res.Email != "bob@gmail.com" {
		t.Fatalf("unexpected Google Drive oauth result: %+v", res)
	}

	src := CloudSource{
		ID:           "gd-1",
		Type:         CloudTypeGoogleDrive,
		Name:         "我的 Google Drive",
		Folder:       "Music",
		ClientID:     "gd-client",
		ClientSecret: "gd-secret",
		RefreshToken: res.RefreshToken,
	}
	tmp := t.TempDir()
	tracks, warns := ScanCloud(context.Background(), []CloudSource{src}, filepath.Join(tmp, "covers"), filepath.Join(tmp, "idx.json"))
	if len(warns) != 0 || len(tracks) != 1 {
		t.Fatalf("ScanCloud Google Drive failed: tracks=%+v warns=%v", tracks, warns)
	}
	tr := tracks[0]
	if tr.Name != "GD Song" || tr.ArtistNames() != "GD Artist" || tr.Duration != 80000 {
		t.Fatalf("unexpected Google Drive track: %+v", tr)
	}
	if got := Lyrics(tr); !strings.Contains(got, "Google Drive 歌词") {
		t.Fatalf("unexpected Google Drive lyrics: %q", got)
	}

	localFile := filepath.Join(tmp, "gd-upload.flac")
	if err := os.WriteFile(localFile, []byte("gd-flac"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UploadToCloud(context.Background(), localFile, src); err != nil {
		t.Fatalf("UploadToCloud Google Drive failed: %v", err)
	}
	if !bytes.Equal(uploaded, []byte("gd-flac")) {
		t.Fatalf("Google Drive upload mismatch: %q", uploaded)
	}
}
