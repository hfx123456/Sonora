package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A temporary image failure must retry after its cooldown instead of leaving sidebar covers blank for the whole session.
func TestArtworkFailureRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { calls.Add(1); writer.WriteHeader(500) }))
	defer server.Close()
	posted := make(chan func(), 2)
	cache := newArtCache(func(fn func()) { posted <- fn })
	now := time.Unix(100, 0)
	cache.now = func() time.Time { return now }
	cache.get(server.URL, false)
	select {
	case fn := <-posted:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("first image request did not finish")
	}
	now = now.Add(29 * time.Second)
	cache.get(server.URL, false)
	if calls.Load() != 1 {
		t.Fatal("cover requests ignored the cooldown")
	}
	now = now.Add(2 * time.Second)
	cache.get(server.URL, false)
	select {
	case fn := <-posted:
		fn()
	case <-time.After(3 * time.Second):
		t.Fatal("failed cover was never retried")
	}
	if calls.Load() != 2 {
		t.Fatal("cover retry did not request the image")
	}
}
