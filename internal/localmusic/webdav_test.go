package localmusic

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"vinyl/internal/spotify"
)

func makeMinimalFLAC(sampleRate, totalSamples uint64, totalSize int) []byte {
	buf := make([]byte, max(totalSize, 42))
	copy(buf[0:4], "fLaC")
	// Last metadata block flag (0x80) | STREAMINFO (0x00), length 34.
	buf[4] = 0x80
	buf[5], buf[6], buf[7] = 0, 0, 34
	packed := (sampleRate << 44) | (totalSamples & ((1 << 36) - 1))
	binary.BigEndian.PutUint64(buf[18:26], packed)
	return buf
}

// WebDAV scanning discovers nested audio files via PROPFIND, parses metadata
// over HTTP Range without downloading full files, caches unchanged entries,
// loads companion LRC lyrics, and matches spotify:local: playlist URIs.
func TestWebDAVScanRangeAndCache(t *testing.T) {
	const (
		user = "alice"
		pass = "secret"
	)
	// 44100 Hz * 120 seconds = 2:00 FLAC padded to 512 KB.
	flacData := makeMinimalFLAC(44100, 44100*120, 512<<10)
	lrcData := []byte("[00:01.00]云盘测试歌词\n")

	var rangeCalls atomic.Int32
	var fullAudioCalls atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case "PROPFIND":
			switch r.URL.Path {
			case "/dav/Music", "/dav/Music/":
				if r.Header.Get("Depth") == "0" {
					w.WriteHeader(http.StatusMultiStatus)
					io.WriteString(w, `<?xml version="1.0"?><D:multistatus xmlns:D="DAV:"><D:response><D:href>/dav/Music/</D:href><D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`)
					return
				}
				w.WriteHeader(http.StatusMultiStatus)
				io.WriteString(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/Music/</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/Music/Sub/</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
</D:multistatus>`)
				return
			case "/dav/Music/Sub", "/dav/Music/Sub/":
				w.WriteHeader(http.StatusMultiStatus)
				fmt.Fprintf(w, `<?xml version="1.0"?>
<D:multistatus xmlns:D="DAV:">
  <D:response>
    <D:href>/dav/Music/Sub/</D:href>
    <D:propstat><D:prop><D:resourcetype><D:collection/></D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
  <D:response>
    <D:href>/dav/Music/Sub/Artist%%20-%%20Cloud%%20Song.flac</D:href>
    <D:propstat><D:prop><D:resourcetype/><D:getcontentlength>%d</D:getcontentlength><D:getetag>"v1"</D:getetag></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat>
  </D:response>
</D:multistatus>`, len(flacData))
				return
			}
		case http.MethodGet:
			if r.URL.Path == "/dav/Music/Sub/Artist - Cloud Song.lrc" {
				w.WriteHeader(http.StatusOK)
				w.Write(lrcData)
				return
			}
			if r.URL.Path == "/dav/Music/Sub/Artist - Cloud Song.flac" {
				rg := r.Header.Get("Range")
				if rg == "" {
					fullAudioCalls.Add(1)
					w.WriteHeader(http.StatusOK)
					w.Write(flacData)
					return
				}
				rangeCalls.Add(1)
				spec := strings.TrimPrefix(rg, "bytes=")
				loStr, hiStr, _ := strings.Cut(spec, "-")
				lo, _ := strconv.Atoi(loStr)
				hi, _ := strconv.Atoi(hiStr)
				if hi >= len(flacData) {
					hi = len(flacData) - 1
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", lo, hi, len(flacData)))
				w.WriteHeader(http.StatusPartialContent)
				w.Write(flacData[lo : hi+1])
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	src := CloudSource{ID: "test", Name: "我的云盘", URL: srv.URL + "/dav/Music", Username: user, Password: pass}
	if err := TestCloudSource(context.Background(), src); err != nil {
		t.Fatalf("TestCloudSource failed: %v", err)
	}
	bad := src
	bad.Password = "wrong"
	if err := TestCloudSource(context.Background(), bad); err == nil {
		t.Fatal("expected TestCloudSource to fail on wrong password")
	}

	tmp := t.TempDir()
	coverDir := filepath.Join(tmp, "covers")
	cachePath := filepath.Join(tmp, "cloud-index.json")

	tracks, warnings := ScanCloud(context.Background(), []CloudSource{src}, coverDir, cachePath)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(tracks) != 1 {
		t.Fatalf("got %d tracks, want 1", len(tracks))
	}
	tr := tracks[0]
	if tr.Name != "Cloud Song" || tr.ArtistNames() != "Artist" || tr.Duration != 120000 || !tr.Cloud() || !tr.Local() {
		t.Fatalf("unexpected track metadata: %+v", tr)
	}
	if fullAudioCalls.Load() != 0 {
		t.Fatalf("expected 0 full audio downloads, got %d", fullAudioCalls.Load())
	}
	if rangeCalls.Load() == 0 {
		t.Fatal("expected Range reads during initial scan")
	}

	// Companion LRC loads over WebDAV with registered credentials.
	if got := Lyrics(tr); !strings.Contains(got, "云盘测试歌词") {
		t.Fatalf("unexpected cloud lyrics: %q", got)
	}

	// Cloud tracks automatically satisfy spotify:local: playlist matching.
	spotify.SetLocalTracks(tracks)
	t.Cleanup(func() { spotify.SetLocalTracks(nil) })
	if matched, ok := spotify.MatchLocal("spotify:local:Artist::Cloud%20Song:120"); !ok || matched.CloudURL != tr.CloudURL {
		t.Fatalf("MatchLocal did not match cloud track: ok=%v matched=%+v", ok, matched)
	}

	// Second scan hits cloud-index.json cache and makes 0 Range requests.
	before := rangeCalls.Load()
	tracks2, warnings2 := ScanCloud(context.Background(), []CloudSource{src}, coverDir, cachePath)
	if len(warnings2) != 0 || len(tracks2) != 1 || tracks2[0].ID != tr.ID {
		t.Fatalf("cached scan failed: tracks=%+v warnings=%v", tracks2, warnings2)
	}
	if rangeCalls.Load() != before {
		t.Fatalf("expected cached scan to issue 0 new Range requests, got %d", rangeCalls.Load()-before)
	}
}

// UploadToCloud PUTs both the audio file and its companion LRC to WebDAV.
func TestWebDAVUploadToCloud(t *testing.T) {
	received := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "bob" || p != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPut {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		received[r.URL.Path] = body
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	dir := t.TempDir()
	audioPath := filepath.Join(dir, "demo.flac")
	lrcPath := filepath.Join(dir, "demo.lrc")
	if err := os.WriteFile(audioPath, []byte("flac-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lrcPath, []byte("[00:00.00]hi"), 0o600); err != nil {
		t.Fatal(err)
	}

	src := CloudSource{ID: "u", Name: "Cloud", URL: srv.URL + "/dav/Music", Username: "bob", Password: "pw"}
	targetURL, err := UploadToCloud(context.Background(), audioPath, src)
	if err != nil {
		t.Fatalf("UploadToCloud failed: %v", err)
	}
	if !strings.HasSuffix(targetURL, "/dav/Music/demo.flac") {
		t.Fatalf("unexpected targetURL: %s", targetURL)
	}
	if !bytes.Equal(received["/dav/Music/demo.flac"], []byte("flac-bytes")) {
		t.Fatalf("audio upload mismatch: %q", received["/dav/Music/demo.flac"])
	}
	if !bytes.Equal(received["/dav/Music/demo.lrc"], []byte("[00:00.00]hi")) {
		t.Fatalf("lrc upload mismatch: %q", received["/dav/Music/demo.lrc"])
	}
}
