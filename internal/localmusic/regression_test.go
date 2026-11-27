package localmusic

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Repeated OAuth refresh token rotations (R0 -> R1 -> R2) must not be rolled back by stale SetCloudSources(R1).
func TestMultiRotationPreservesNewestRefreshToken(t *testing.T) {
	SetCloudSources([]CloudSource{{
		ID: "od-rot", Type: CloudTypeOneDrive, ClientID: "cid", RefreshToken: "R0",
	}})
	oauthRegistry.RLock()
	entry := oauthRegistry.byID["od-rot"]
	oauthRegistry.RUnlock()
	if entry == nil {
		t.Fatal("entry not registered")
	}
	// Simulate rotation R0 -> R1 -> R2.
	entry.mu.Lock()
	entry.knownRefreshes["R0"] = struct{}{}
	entry.knownRefreshes["R1"] = struct{}{}
	entry.knownRefreshes["R2"] = struct{}{}
	entry.refreshToken = "R2"
	entry.mu.Unlock()

	// Stale SetCloudSources carrying R1 must preserve R2.
	SetCloudSources([]CloudSource{{
		ID: "od-rot", Type: CloudTypeOneDrive, ClientID: "cid", RefreshToken: "R1",
	}})
	if got := CurrentOAuthRefreshToken("od-rot"); got != "R2" {
		t.Fatalf("refresh token rolled back to %q, want R2", got)
	}
}

// An anonymous nested WebDAV mount must not inherit Basic Auth credentials from its parent mount.
func TestWebDAVAnonymousSubmountDoesNotInheritParentAuth(t *testing.T) {
	SetCloudSources([]CloudSource{
		{ID: "parent", Type: CloudTypeWebDAV, URL: "https://nas.example/dav", Username: "u", Password: "secret"},
		{ID: "anon", Type: CloudTypeWebDAV, URL: "https://nas.example/dav/public"},
	})
	if h := CloudHeadersForURL("https://nas.example/dav/private/a.mp3"); h["Authorization"] == "" {
		t.Fatal("parent mount lost its Authorization header")
	}
	if h := CloudHeadersForURL("https://nas.example/dav/public/a.mp3"); len(h) != 0 {
		t.Fatalf("anonymous nested mount inherited parent auth: %v", h)
	}
}

// WebDAV PROPFIND must refuse cross-origin or out-of-scope redirects.
func TestWebDAVPropfindRefusesOutOfScopeRedirect(t *testing.T) {
	outside := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("PROPFIND followed cross-origin redirect with auth=%q", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer outside.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, outside.URL+"/steal", http.StatusFound)
	}))
	defer origin.Close()

	scope, _ := url.Parse(origin.URL + "/music/")
	_, err := propfindDir(context.Background(), CloudSource{URL: scope.String(), Username: "u", Password: "p"}, scope.String(), scope)
	if err == nil {
		t.Fatal("PROPFIND succeeded across cross-origin redirect")
	}
}

// Google Drive must propagate HTTP errors and escape backslashes; Quark must reject Chinese names as raw FIDs.
func TestGoogleDriveAndQuarkFolderValidation(t *testing.T) {
	if got := escapeDriveQuery(`a\b'c`); got != `a\\b\'c` {
		t.Fatalf("escapeDriveQuery = %q", got)
	}
	if isLikelyQuarkFID("我的无损音乐收藏合集") {
		t.Fatal("Chinese folder name wrongly treated as Quark FID")
	}
	if !isLikelyQuarkFID("a1b2c3d4e5f60718") {
		t.Fatal("valid 16-char ASCII Quark FID rejected")
	}
	oldBase := googleDriveAPIBase
	defer func() { googleDriveAPIBase = oldBase }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	googleDriveAPIBase = srv.URL
	if _, err := resolveGoogleDriveFolder(context.Background(), "Music", "Bearer tok"); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected HTTP 500 error, got %v", err)
	}
}

// High-sample-rate long FLAC files must not overflow int64 duration math.
func TestFLACDurationNoInt64Overflow(t *testing.T) {
	// 20 hours at 192 kHz -> 13,824,000,000 samples (> math.MaxInt64 / 1e9).
	const rate = uint64(192000)
	const samples = uint64(20 * 3600 * 192000)
	buf := make([]byte, 64)
	copy(buf[:4], "fLaC")
	packed := (rate << 44) | (samples & ((1 << 36) - 1))
	binary.BigEndian.PutUint64(buf[18:26], packed)
	got := duration(bytes.NewReader(buf), int64(len(buf)), ".flac")
	want := 20 * time.Hour
	if got != want {
		t.Fatalf("duration = %v, want %v", got, want)
	}
}
