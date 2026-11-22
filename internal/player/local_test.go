package player

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vinyl/internal/spotify"
)

// Local media serves seeking ranges without requesting Spotify and disappears when its output closes.
func TestLocalFileRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 歌曲.mp3")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	web, err := listen()
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	output := web.Open(&spotify.Media{LocalPath: path, Duration: time.Minute})
	get := func(target string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "http://"+web.addr+target, nil)
		request.Header.Set("Range", "bytes=3-5")
		response := httptest.NewRecorder()
		web.ServeHTTP(response, request)
		return response
	}
	response := get("/m/1/0")
	if response.Code != http.StatusPartialContent || response.Body.String() != "345" || response.Header().Get("Content-Range") != "bytes 3-5/10" {
		t.Fatalf("range: %d %s %v", response.Code, response.Body.String(), response.Header())
	}
	if response := get("/m/1/99"); response.Code != http.StatusNotFound {
		t.Fatal("served an arbitrary part")
	}
	output.Close()
	if response := get("/m/1/0"); response.Code != http.StatusNotFound {
		t.Fatal("closed output still served the file")
	}
}

// Local audio uses the decoder's duration and stays out of the network recovery loop.
func TestLocalDurationAndFailure(t *testing.T) {
	web, err := listen()
	if err != nil {
		t.Fatal(err)
	}
	defer web.Close()
	output := web.Open(&spotify.Media{LocalPath: "unused"}).(*webOut)
	output.event("duration", 83*time.Second, "", 0)
	player := New(func(func()) {})
	player.track = &spotify.Track{ID: "local:test", LocalPath: "unused"}
	player.out = output
	if player.Duration() != 83*time.Second {
		t.Fatal("decoder duration was ignored")
	}
	called := false
	player.unlessOffline(false, func() { called = true })
	if !called || player.loading {
		t.Fatal("local failure waited for the network")
	}
}
