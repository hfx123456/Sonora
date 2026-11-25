package localmusic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"vinyl/internal/spotify"
)

func TestDownloadToCloudCacheRejectsOversizedAndTruncated(t *testing.T) {
	dir := t.TempDir()
	SetCloudCacheDir(dir)
	prevCap := maxCloudCacheBytes
	maxCloudCacheBytes = 1024
	defer func() { maxCloudCacheBytes = prevCap }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oversized-header":
			w.Header().Set("Content-Length", "2048")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 2048))
		case "/oversized-chunked":
			w.WriteHeader(http.StatusOK)
			fl, _ := w.(http.Flusher)
			chunk := make([]byte, 600)
			_, _ = w.Write(chunk)
			if fl != nil {
				fl.Flush()
			}
			_, _ = w.Write(chunk) // 1200 > 1024 with chunked encoding
		case "/truncated":
			// Advertise 512 bytes, write 200 and hijack/close early.
			w.Header().Set("Content-Length", "512")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(make([]byte, 200))
		case "/ok":
			data := []byte("complete-audio-payload")
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(data)
		}
	}))
	defer srv.Close()

	ctx := context.Background()
	for _, tc := range []struct {
		id   string
		path string
	}{
		{"cloud:over-hdr", "/oversized-header"},
		{"cloud:over-chunked", "/oversized-chunked"},
		{"cloud:trunc", "/truncated"},
	} {
		dst, err := downloadToCloudCache(ctx, tc.id, srv.URL+tc.path, nil)
		if err == nil || dst != "" {
			t.Fatalf("%s: expected error and empty dst, got dst=%q err=%v", tc.id, dst, err)
		}
		if got := CachedCloudAudio(tc.id); got != "" {
			t.Fatalf("%s: truncated/oversized file should not be cached at %q", tc.id, got)
		}
	}

	dst, err := downloadToCloudCache(ctx, "cloud:ok", srv.URL+"/ok", nil)
	if err != nil || dst == "" {
		t.Fatalf("complete download failed: dst=%q err=%v", dst, err)
	}
	if CachedCloudAudio("cloud:ok") != dst {
		t.Fatalf("expected complete file cached at %q", dst)
	}
}

func TestCacheAllCloudTracksReportsFailures(t *testing.T) {
	dir := t.TempDir()
	SetCloudCacheDir(filepath.Join(dir, "audio"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	src := CloudSource{ID: "src503", Type: CloudTypeWebDAV, Name: "TestDAV", URL: srv.URL + "/dav"}
	SetCloudSources([]CloudSource{src})

	track := spotify.Track{
		ID:          "cloud:fail-503",
		Name:        "Unavailable",
		CloudURL:    srv.URL + "/dav/Unavailable.mp3",
		CloudSource: "TestDAV",
	}

	var cbDone, cbOK, cbFail, cbTotal int
	var cbEnriched *spotify.Track
	stats := CacheAllCloudTracks(context.Background(), []spotify.Track{track}, filepath.Join(dir, "covers"), filepath.Join(dir, "cloud-index.json"),
		func(done, succeeded, failed, total int, enriched *spotify.Track) {
			cbDone, cbOK, cbFail, cbTotal, cbEnriched = done, succeeded, failed, total, enriched
		})

	if stats.Total != 1 || stats.Succeeded != 0 || stats.Failed != 1 {
		t.Fatalf("stats=%+v, want Total=1 Succeeded=0 Failed=1", stats)
	}
	if cbDone != 1 || cbOK != 0 || cbFail != 1 || cbTotal != 1 || cbEnriched != nil {
		t.Fatalf("callback=(%d,%d,%d,%d,%v), want (1,0,1,1,nil)", cbDone, cbOK, cbFail, cbTotal, cbEnriched)
	}
	if got := CachedCloudAudio(track.ID); got != "" {
		t.Fatalf("failed download should not be cached, got %q", got)
	}
}

func TestScanCloudPreservesOfflineAndPrunesEmpty(t *testing.T) {
	dir := t.TempDir()
	coverDir := filepath.Join(dir, "covers")
	cachePath := filepath.Join(dir, "cloud-index.json")

	// mode: 0 = 1 file, 1 = offline (503), 2 = empty directory
	var mode atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PROPFIND" {
			switch mode.Load() {
			case 0:
				w.WriteHeader(http.StatusMultiStatus)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/dav/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
  <d:response>
    <d:href>/dav/SongA.mp3</d:href>
    <d:propstat><d:prop><d:getcontentlength>4096</d:getcontentlength><d:getetag>"v1"</d:getetag></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
</d:multistatus>`))
			case 1:
				http.Error(w, "offline", http.StatusServiceUnavailable)
			case 2:
				w.WriteHeader(http.StatusMultiStatus)
				_, _ = w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/dav/</d:href>
    <d:propstat><d:prop><d:resourcetype><d:collection/></d:resourcetype></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat>
  </d:response>
</d:multistatus>`))
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, 64))
	}))
	defer srv.Close()

	src := CloudSource{ID: "dav-offline-test", Type: CloudTypeWebDAV, Name: "OfficeDAV", URL: srv.URL + "/dav/"}
	ctx := context.Background()

	// 1. Initial online scan indexes 1 track.
	tracks, warns := ScanCloud(ctx, []CloudSource{src}, coverDir, cachePath)
	if len(tracks) != 1 {
		t.Fatalf("initial scan: got %d tracks (warns=%v), want 1", len(tracks), warns)
	}

	// 2. Offline refresh fails: cached track must remain visible and stay in disk index.
	mode.Store(1)
	offlineTracks, offlineWarns := ScanCloud(ctx, []CloudSource{src}, coverDir, cachePath)
	if len(offlineTracks) != 1 {
		t.Fatalf("offline scan: got %d tracks (warns=%v), want 1 preserved offline track", len(offlineTracks), offlineWarns)
	}
	hasOfflineHint := false
	for _, w := range offlineWarns {
		if strings.Contains(w, "离线曲库") {
			hasOfflineHint = true
			break
		}
	}
	if !hasOfflineHint {
		t.Fatalf("offline scan warnings=%v, expected offline library hint", offlineWarns)
	}
	if cached := LoadCachedCloud(cachePath, []CloudSource{src}); len(cached) != 1 {
		t.Fatalf("disk index after offline scan: got %d tracks, want 1", len(cached))
	}

	// 3. Server succeeds with empty directory: deleted track must be pruned from disk index too.
	mode.Store(2)
	emptyTracks, emptyWarns := ScanCloud(ctx, []CloudSource{src}, coverDir, cachePath)
	if len(emptyTracks) != 0 || len(emptyWarns) != 0 {
		t.Fatalf("empty scan: got %d tracks warns=%v, want 0 tracks and 0 warnings", len(emptyTracks), emptyWarns)
	}
	if cached := LoadCachedCloud(cachePath, []CloudSource{src}); len(cached) != 0 {
		t.Fatalf("disk index after successful empty scan: got %d tracks, want 0 (deleted song must not reappear)", len(cached))
	}
}
