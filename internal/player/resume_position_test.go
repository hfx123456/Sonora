package player

import (
	"net/http"
	"testing"
	"time"

	"vinyl/internal/spotify"
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
	if output.pos != 91*time.Second || output.Position() < 91*time.Second || output.Position() > 92*time.Second || output.seekPending != nil {
		t.Fatalf("seek confirmation did not restore normal progress: pos=%v position=%v pending=%v", output.pos, output.Position(), output.seekPending)
	}
}

// A late time event cannot undo a completed seek, and a late pause still changes playback state.
func TestWebPositionAndStateHandleOutOfOrderEvents(t *testing.T) {
	output := &webOut{w: &Web{cmds: make(chan string, 10)}, dur: time.Minute, running: true}
	output.Seek(35 * time.Second)
	output.event("seeked", 35*time.Second, "", 2)
	output.event("time", 5*time.Second, "", 1)
	if output.pos != 35*time.Second {
		t.Fatalf("pre-seek event restored the old position: %v", output.pos)
	}
	output.event("time", 36*time.Second, "", 4)
	output.event("pause", 35*time.Second, "", 3)
	if output.running || output.Position() != 36*time.Second {
		t.Fatalf("late pause: playing=%v, position=%v", output.running, output.Position())
	}
}

// Rapid back-to-back seeks must not jump back when the first seek's seeked event or a system-pause arrives late.
func TestRapidBackToBackSeekIgnoresOlderSeeked(t *testing.T) {
	output := &webOut{w: &Web{cmds: make(chan string, 10)}, dur: 3 * time.Minute, running: true, playing: true}
	output.Seek(20 * time.Second) // seekSeq = 1
	output.Seek(95 * time.Second) // seekSeq = 2
	output.eventWithSeek("seeked", 20*time.Second, "", 1, 1)
	if output.Position() != 95*time.Second || output.seekPending == nil {
		t.Fatalf("stale seeked jumped position to %v (pending=%v)", output.Position(), output.seekPending)
	}
	output.eventWithSeek("system-pause", 20*time.Second, "", output.asked, 1)
	if output.Position() != 95*time.Second {
		t.Fatalf("stale system-pause jumped position to %v", output.Position())
	}
	output.eventWithSeek("seeked", 95*time.Second, "", 2, 2)
	if output.Position() != 95*time.Second || output.seekPending != nil {
		t.Fatalf("latest seeked did not settle: pos=%v pending=%v", output.Position(), output.seekPending)
	}
}

// Remote Connect mirror updates must backfill track duration without regressing positive durations to zero.
func TestMirrorConnectBackfillsTrackDuration(t *testing.T) {
	p := New(func(fn func()) { fn() })
	track := spotify.Track{ID: "t1", Name: "Song", Duration: 0}
	p.MirrorConnect(track, 12*time.Second, true)
	if p.Duration() != 0 || p.Position() < 12*time.Second {
		t.Fatalf("initial zero-duration mirror: dur=%v pos=%v", p.Duration(), p.Position())
	}
	track.Duration = 180_000
	p.MirrorConnect(track, 15*time.Second, true)
	if p.Duration() != 180*time.Second {
		t.Fatalf("duration after backfill = %v, want 3m", p.Duration())
	}
	p.ConnectQueue([]spotify.Track{{ID: "t1", Name: "Song", Duration: 0}})
	if p.Duration() != 180*time.Second {
		t.Fatalf("ConnectQueue with zero duration regressed duration to %v", p.Duration())
	}
}

// Toggling shuffle off under Connect must clear smartShuffle instead of clearing it when turning shuffle on.
func TestToggleShuffleClearsSmartShuffleUnderConnect(t *testing.T) {
	p := New(func(fn func()) { fn() })
	p.shuffle = true
	p.smartShuffle = true
	p.Control = func(action string, value any) bool {
		return action == "shuffle"
	}
	p.ToggleShuffle()
	if p.smartShuffle {
		t.Fatal("turning shuffle off under Connect left smartShuffle enabled")
	}
}
