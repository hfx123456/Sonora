package player

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"vinyl/internal/spotify"
)

// A temporary certificate gateway error used to reach the audio page before a successful retry.
func TestCertificateRequestRecoversFromBadGateway(t *testing.T) {
	attempts := 0
	req := httptest.NewRequest(http.MethodGet, "https://example.test/application-certificate", nil)
	res, err := requestCertificate(req, func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("certificate"))}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK || attempts != 2 {
		t.Fatalf("certificate response %d after %d attempts", res.StatusCode, attempts)
	}
}

// An exhausted certificate request keeps the audio page and retries the same song once.
func TestCertificateFailureRetriesSameTrack(t *testing.T) {
	ch, p, waits := licensed(t, func(http.ResponseWriter, *http.Request) {})
	web := p.Web
	p.out.Seek(12 * time.Second)
	report := func(gen string) {
		t.Helper()
		page(p, http.MethodPost, "/event", `{"gen":`+gen+`,"type":"error","msg":"keySystemError keySystemServerCertificateRequestFailed"}`)
		next(t, ch)
	}
	report("1")
	if len(*waits) != 1 || p.Web != web || !p.Playing() || p.Current().ID != "1" || p.Position() != 12*time.Second {
		t.Fatal("certificate failure lost the active page, song, or position")
	}
	(*waits)[0]()
	next(t, ch)
	next(t, ch)
	if p.out == nil || p.Current().ID != "1" || p.Position() != 12*time.Second {
		t.Fatal("retry did not reopen the song at its saved position")
	}
	report("2")
	if len(*waits) != 1 || p.Web != web || p.Playing() || p.Current().ID != "1" || p.Position() != 12*time.Second {
		t.Fatal("persistent failure restarted the page, advanced the queue, or lost the position")
	}
}

// A page that never becomes ready must release playback so the same song can be retried.
func TestAudioPageStartupTimeout(t *testing.T) {
	ch, playback, _ := licensed(t, func(w http.ResponseWriter, r *http.Request) {})
	web := playback.Web
	web.ready, web.done = make(chan struct{}), make(chan struct{})
	timeout := make(chan time.Time, 1)
	timeout <- time.Time{}
	if web.awaitReady(timeout) {
		t.Fatal("unresponsive audio page became ready")
	}
	next(t, ch)
	if playback.Playing() || playback.Ready() || playback.Web != nil || playback.Current().ID != "1" || playback.Err() == "" {
		t.Fatal("startup failure left playback stuck or advanced to another song")
	}
}

// A ready or closed page must not fail as a startup timeout.
func TestAudioPageReadyOrClosed(t *testing.T) {
	for _, ready := range []bool{false, true} {
		web := &Web{ready: make(chan struct{}), done: make(chan struct{})}
		if ready {
			close(web.ready)
		} else {
			close(web.done)
		}
		if web.awaitReady(make(chan time.Time)) != ready || web.browserErr != "" {
			t.Fatal("normal readiness or shutdown reported a backend failure")
		}
	}
}

// A dead Chrome backend must stop on the same song and restart from its position on retry.
func TestBrowserExitStopsAndRestartsPlayback(t *testing.T) {
	ch, p, _ := licensed(t, func(w http.ResponseWriter, r *http.Request) {})
	closed, starts := false, 0
	p.Web.stopBrowser = func() { closed = true }
	p.NewWeb = func() (*Web, error) {
		starts++
		web, err := listen()
		if err == nil {
			t.Cleanup(web.Close)
		}
		return web, err
	}
	p.out.Seek(12 * time.Second)
	p.Web.browserFailed()
	next(t, ch)
	if !closed || p.playing || p.loading || p.at != 0 || p.out != nil || p.Web != nil || p.err == "" {
		t.Fatalf("backend failure did not stop and release playback: %+v", p)
	}
	p.TogglePlay()
	next(t, ch)
	if starts != 1 || !p.playing || p.loading || p.at != 0 || p.out == nil || p.Position() != 12*time.Second || p.err != "" {
		t.Fatalf("retry did not restart the same song at its position: starts=%d player=%+v", starts, p)
	}
}

// Unsupported key systems used to skip every song, including after MV fallback.
func TestUnsupportedKeySystemStopsQueue(t *testing.T) {
	for _, video := range []bool{false, true} {
		for _, msg := range []string{"keySystemError keySystemNoAccess", "keySystemError keySystemNoEME"} {
			ch, p, _ := licensed(t, func(w http.ResponseWriter, r *http.Request) {})
			p.showsVideo, p.video = video, video
			page(p, http.MethodPost, "/event", `{"gen":1,"type":"error","msg":"`+msg+`"}`)
			next(t, ch)
			if p.playing || p.at != 0 || p.loading || p.err == "" || p.showsVideo != video || p.gen != 1 {
				t.Fatalf("video=%v error=%s: advanced, retried, or lost error: %+v", video, msg, p)
			}
		}
	}
}

// Repeated playback errors must stop after three failures rather than drain a long queue.
func TestConsecutivePlaybackFailuresStop(t *testing.T) {
	ch, p, _ := licensed(t, func(w http.ResponseWriter, r *http.Request) {})
	p.queue = append(p.queue, spotify.Track{ID: "4"}, spotify.Track{ID: "5"})
	p.order = append(p.order, 3, 4)
	for gen := 1; gen <= 3; gen++ {
		page(p, http.MethodPost, "/event", `{"gen":`+strconv.Itoa(gen)+`,"type":"error","msg":"media 3"}`)
		next(t, ch)
		next(t, ch) // the network is up
		if gen < 3 {
			if !p.Loading() || p.at != gen {
				t.Fatalf("failure %d did not advance once", gen)
			}
			next(t, ch)
		}
	}
	if p.playing || p.at != 2 || p.loading || p.gen != 3 || p.err == "" {
		t.Fatalf("advanced past consecutive failure limit: %+v", p)
	}
}

// A video backend error while showsVideo is true must close p.Video and fall back to audio without panicking on nil p.Web.
func TestVideoBackendErrorFallsBackWithoutClosingAudioWeb(t *testing.T) {
	ch, p, _ := licensed(t, func(w http.ResponseWriter, r *http.Request) {})
	audioWeb := p.Web
	videoClosed, audioClosed := false, false
	audioWeb.stopBrowser = func() { audioClosed = true }
	p.Video = audioWeb
	p.Web = nil // simulate audio web not yet initialized
	p.video, p.showsVideo = true, true
	p.Video.stopBrowser = func() { videoClosed = true }

	p.Video.browserFailed()
	next(t, ch)
	if !videoClosed || audioClosed || p.Video != nil || p.showsVideo || p.video {
		t.Fatalf("video backendError did not isolate Video backend: videoClosed=%v audioClosed=%v Video=%v showsVideo=%v", videoClosed, audioClosed, p.Video, p.showsVideo)
	}
}
