package spotify

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// /api/token answers with the token it gave until that runs out: one
// handed back with less than renewAhead left is used until just before it
// ends, where every request once renewed it again.
func TestARenewalHandedBackIsKept(t *testing.T) {
	oldRenew := Renew
	defer func() { Renew = oldRenew; SignOut() }()
	var renewals atomic.Int32
	ends := time.Now().Add(90 * time.Second)
	Renew = func() (Identity, error) {
		renewals.Add(1)
		return Identity{Token: "same", ClientToken: "c", Expires: ends}, nil
	}
	SetIdentity(Identity{Token: "same", ClientToken: "c", Expires: ends})
	for range 20 {
		id, err := CurrentIdentity()
		if err != nil || id.Token != "same" {
			t.Fatalf("CurrentIdentity = %+v, %v", id, err)
		}
	}
	if n := renewals.Load(); n != 1 {
		t.Fatalf("%d renewals for 20 requests, want the one that handed it back", n)
	}

	// In its last seconds, it is renewed again: by then Spotify gives a new
	// one.
	ends = time.Now().Add(3 * time.Second)
	SetIdentity(Identity{Token: "same", ClientToken: "c", Expires: ends})
	Renew = func() (Identity, error) {
		renewals.Add(1)
		return Identity{Token: "next", ClientToken: "c", Expires: time.Now().Add(time.Hour)}, nil
	}
	if id, err := CurrentIdentity(); err != nil || id.Token != "next" {
		t.Fatalf("CurrentIdentity = %+v, %v; want the next token", id, err)
	}
}

func TestFetchLibraryTracksRenewsFullOnUnauthorized(t *testing.T) {
	oldRenew, oldFull, oldURL := Renew, RenewFull, pathfinderURL
	defer func() {
		Renew, RenewFull, pathfinderURL = oldRenew, oldFull, oldURL
		SignOut()
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer full-tok" {
			w.Write([]byte(`{"data":{"me":{"library":{"tracks":{"items":[{"track":{"_uri":"spotify:track:1","data":{"__typename":"Track","uri":"spotify:track:1","name":"Song","playability":{"playable":true}}}}]}}}}}`))
			return
		}
		w.Write([]byte(`{"data":{"me":null},"errors":[{"message":"Exception while fetching data (/me) : User is not authorized"}]}`))
	}))
	defer srv.Close()
	pathfinderURL = srv.URL

	SetIdentity(Identity{Token: "cookie-tok", ClientToken: "c", Expires: time.Now().Add(time.Hour)})
	var fullCalls atomic.Int32
	RenewFull = func() (Identity, error) {
		fullCalls.Add(1)
		return Identity{Token: "full-tok", ClientToken: "c", Expires: time.Now().Add(time.Hour)}, nil
	}

	pl, err := PlaylistDetail(LikedURI)
	if err != nil {
		t.Fatalf("PlaylistDetail(LikedURI) = %v", err)
	}
	if len(pl.Tracks) != 1 || pl.Tracks[0].ID != "1" {
		t.Fatalf("got tracks %+v, want 1 track", pl.Tracks)
	}
	if fullCalls.Load() != 1 {
		t.Fatalf("RenewFull called %d times, want 1", fullCalls.Load())
	}
}
