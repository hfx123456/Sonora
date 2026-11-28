package spotify

import "testing"

func TestLiveGuestHome(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping live Spotify guest home probe in -short mode")
	}
	SetIdentity(Identity{})
	SignOut()
	RenewGuest = AnonymousIdentity
	defer func() { RenewGuest = nil; SignOut() }()
	h, err := HomeFeed("")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("greeting %q, %d sections, %d shortcuts", h.Greeting, len(h.Sections), len(h.Shortcuts))
	for i, s := range h.Sections {
		if i > 12 {
			break
		}
		t.Logf("  %q (%d items, recent=%v)", s.Title, len(s.Items), s.Recent)
	}
}
