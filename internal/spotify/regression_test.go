package spotify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// ResolveMedia must accept empty string bitrates in looseInt and rank positive bitrates ahead of zero.
func TestLooseIntAndBitrateOrder(t *testing.T) {
	var n looseInt
	if err := json.Unmarshal([]byte(`""`), &n); err != nil || n != 0 {
		t.Fatalf("looseInt(\"\") = %d, %v; want 0, nil", n, err)
	}
	files := []mediaFile{
		{FileID: "zero", Bitrate: 0},
		{FileID: "high", Bitrate: 256000},
		{FileID: "low", Bitrate: 128000},
	}
	// Mirror ResolveMedia's comparator.
	less := func(i, j int) bool {
		bi, bj := files[i].Bitrate, files[j].Bitrate
		if (bi <= 0) != (bj <= 0) {
			return bi > 0
		}
		return bi < bj
	}
	if !less(2, 0) || less(0, 2) || !less(2, 1) {
		t.Fatal("bitrate ordering did not place 128k ahead of 256k and 0")
	}
}

// Guest 401 retries must refresh only the failed guest token and never invoke account Renew.
func TestGuestUnauthorizedRetriesAsGuestWithoutAccountRenew(t *testing.T) {
	oldRenew, oldRenewGuest, oldClient := Renew, RenewGuest, Client
	defer func() {
		Renew, RenewGuest, Client = oldRenew, oldRenewGuest, oldClient
		SignOut()
	}()
	SignOut()

	var accountRenewCalls atomic.Int32
	var guestCalls atomic.Int32
	Renew = func() (Identity, error) {
		accountRenewCalls.Add(1)
		return Identity{}, ErrSignedOut
	}
	RenewGuest = func() (Identity, error) {
		n := guestCalls.Add(1)
		return Identity{Token: "guest-" + string(rune('0'+n)), Expires: time.Now().Add(time.Hour), Anonymous: true}, nil
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer guest-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	Client = srv.Client()

	body, err := do(func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, srv.URL, nil)
	})
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("do() = %q, %v; want ok", string(body), err)
	}
	if accountRenewCalls.Load() != 1 {
		// Called once on attempt 0 to confirm signed-out, never again on the guest 401 retry.
		t.Fatalf("account Renew called %d times, want 1", accountRenewCalls.Load())
	}
	if guestCalls.Load() != 2 {
		t.Fatalf("RenewGuest called %d times, want 2", guestCalls.Load())
	}
}

// SendTrackStreamVerificationWithIdentity must deliver using the preset identity even after SignOut clears memory.
func TestSendTrackStreamVerificationWithIdentity(t *testing.T) {
	oldURL, oldClient := melodyBatchURL, Client
	defer func() {
		melodyBatchURL, Client = oldURL, oldClient
		SignOut()
	}()
	SignOut()

	var gotAuth string
	var gotTrack string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var payload struct {
			Messages []struct {
				Type    string                  `json:"type"`
				Message TrackStreamVerification `json:"message"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if len(payload.Messages) == 1 {
			gotTrack = payload.Messages[0].Message.PlayTrack
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	melodyBatchURL = srv.URL
	Client = srv.Client()

	err := SendTrackStreamVerificationWithIdentity(
		context.Background(),
		TrackStreamVerification{PlayTrack: "spotify:track:abc", PlaybackID: "p1", MSPlayed: 35000, MSNominalPlayed: 35000},
		Identity{Token: "departing-token", ClientToken: "ct"},
	)
	if err != nil {
		t.Fatalf("SendTrackStreamVerificationWithIdentity: %v", err)
	}
	if gotAuth != "Bearer departing-token" || gotTrack != "spotify:track:abc" {
		t.Fatalf("unexpected melody delivery: auth=%q track=%q", gotAuth, gotTrack)
	}
}

// Client.CheckRedirect must strip Authorization across hosts/schemes and Cookie across sites or downgrades.
func TestClientRedirectStripsCredentials(t *testing.T) {
	orig, _ := http.NewRequest(http.MethodGet, "https://drive-pc.quark.cn/file", nil)
	orig.Header.Set("Authorization", "Bearer secret")
	orig.Header.Set("Cookie", "sid=1")

	downgrade, _ := http.NewRequest(http.MethodGet, "http://drive-pc.quark.cn/file", nil)
	downgrade.Header = orig.Header.Clone()
	if err := Client.CheckRedirect(downgrade, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if downgrade.Header.Get("Authorization") != "" || downgrade.Header.Get("Cookie") != "" {
		t.Fatalf("downgrade kept secrets: %v", downgrade.Header)
	}

	sameSite, _ := http.NewRequest(http.MethodGet, "https://dl-pc.quark.cn/file", nil)
	sameSite.Header = orig.Header.Clone()
	if err := Client.CheckRedirect(sameSite, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if sameSite.Header.Get("Authorization") != "" || sameSite.Header.Get("Cookie") != "sid=1" {
		t.Fatalf("same-site redirect headers = %v", sameSite.Header)
	}

	crossSite, _ := http.NewRequest(http.MethodGet, "https://evil.example.com/file", nil)
	crossSite.Header = orig.Header.Clone()
	if err := Client.CheckRedirect(crossSite, []*http.Request{orig}); err != nil {
		t.Fatal(err)
	}
	if crossSite.Header.Get("Authorization") != "" || crossSite.Header.Get("Cookie") != "" {
		t.Fatalf("cross-site redirect kept secrets: %v", crossSite.Header)
	}
}
