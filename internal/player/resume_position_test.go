package player

import (
	"net/http"
	"testing"
	"time"
)

// A Connect resume with no explicit seek must keep a restored session's position while opening audio.
func TestConnectResumeKeepsSavedPosition(t *testing.T) {
	posted, playback, _ := licensed(t, func(http.ResponseWriter, *http.Request) {})
	playback.stop()
	playback.pending = 90 * time.Second
	playback.ApplyConnect(*playback.Current(), -1, false)
	if playback.Position() != 90*time.Second {
		t.Fatal("Connect resumed the saved song from zero")
	}
	next(t, posted)
}

// Startup media events at zero must not erase the restored position before the pending seek lands.
func TestResumePositionIgnoresPreSeekEvents(t *testing.T) {
	output := &webOut{w: &Web{cmds: make(chan string, 10)}, dur: 5 * time.Minute}
	output.Seek(90 * time.Second)
	for _, kind := range []string{"time", "waiting", "pause", "playing"} {
		output.event(kind, 0, "", 0)
		if output.Position() != 90*time.Second {
			t.Fatal("startup event erased the restored position")
		}
	}
	output.event("seeked", 90*time.Second, "", 0)
	output.event("time", 91*time.Second, "", 0)
	if output.Position() != 91*time.Second || output.seekPending != nil {
		t.Fatal("seek confirmation did not restore normal progress")
	}
}
