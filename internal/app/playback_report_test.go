package app

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

// playbackTracker.bind must set machineID and stateID even when inactive or matched via LinkedFrom,
// and finalizing must emit both track_data_finalized and TrackStreamVerification for Spotify tracks.
func TestPlaybackTrackerBindAndFinalize(t *testing.T) {
	base := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	pt := newPlaybackTracker(base)

	// Case 1: bind on an inactive tracker (e.g. resuming restored session).
	pt.bind("spotify:track:t1", "", "m1", "s1", 10_000, 180_000, 256000, 11, true, base, true)
	if ms := pt.update(true, 45_000, 180_000, 256000, 11, base.Add(35*time.Second)); ms != "played_threshold_reached" {
		t.Fatalf("milestone = %q, want played_threshold_reached", ms)
	}
	// Milestone remains pending until explicitly confirmed.
	if ms := pt.update(true, 46_000, 180_000, 256000, 11, base.Add(36*time.Second)); ms != "played_threshold_reached" {
		t.Fatalf("unconfirmed milestone dropped: %q", ms)
	}
	pt.confirmMilestone("played_threshold_reached")
	if ms := pt.update(true, 47_000, 180_000, 256000, 11, base.Add(37*time.Second)); ms != "" {
		t.Fatalf("confirmed milestone fired twice: %q", ms)
	}

	rpt, tsv := pt.finalize(47_000, false, "s2", base.Add(37*time.Second))
	if rpt == nil || rpt.DebugSource != "track_data_finalized" {
		t.Fatalf("expected track_data_finalized report, got %+v", rpt)
	}
	if tsv == nil || tsv.PlayTrack != "spotify:track:t1" || tsv.PlaybackID != "s1" || tsv.MSPlayed != 37_000 || tsv.NextPlaybackID != "s2" {
		t.Fatalf("unexpected tsv: %+v", tsv)
	}

	// Case 2: LinkedFrom relinked track preserves msPlayed and sets machineID/stateID.
	pt.start("spotify:track:orig1", "", "", 0, 200_000, 0, 0, true, base)
	pt.update(true, 12_000, 200_000, 0, 0, base.Add(12*time.Second))
	pt.bind("spotify:track:canon1", "orig1", "m2", "s2", 12_000, 200_000, 128000, 10, true, base.Add(12*time.Second), false)
	rpt2, tsv2 := pt.finalize(20_000, false, "", base.Add(20*time.Second))
	if rpt2 == nil || tsv2 == nil || tsv2.PlayTrack != "spotify:track:canon1" || tsv2.PlaybackID != "s2" || tsv2.MSPlayed != 20_000 {
		t.Fatalf("relinked track bind/finalize failed: rpt=%+v tsv=%+v", rpt2, tsv2)
	}

	// Case 3: Podcast episode does not emit TrackStreamVerification.
	pt.start("spotify:episode:ep1", "m3", "s3", 0, 600_000, 0, 0, true, base)
	_, epTSV := pt.finalize(40_000, false, "", base.Add(40*time.Second))
	if epTSV != nil {
		t.Fatalf("episode should not emit track_stream_verification: %+v", epTSV)
	}
}

// Switching from a playing Spotify track to a local track must finalize the Spotify track before state.local turns true.
func TestSwitchSpotifyToLocalFinalizesSpotifyTrack(t *testing.T) {
	oldReport := connectReport
	oldTSV := sendTrackStreamVerification
	oldNow := connectNow
	defer func() {
		connectReport = oldReport
		sendTrackStreamVerification = oldTSV
		connectNow = oldNow
	}()

	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	connectNow = func() time.Time { return now }

	var mu sync.Mutex
	var reported []spotify.ConnectStateReport
	var tsvs []spotify.TrackStreamVerification
	doneReport := make(chan struct{}, 2)
	doneTSV := make(chan struct{}, 2)

	connectReport = func(_ *spotify.Connect, r spotify.ConnectStateReport) error {
		mu.Lock()
		reported = append(reported, r)
		mu.Unlock()
		doneReport <- struct{}{}
		return nil
	}
	sendTrackStreamVerification = func(_ context.Context, ev spotify.TrackStreamVerification) error {
		mu.Lock()
		tsvs = append(tsvs, ev)
		mu.Unlock()
		doneTSV <- struct{}{}
		return nil
	}

	a := newTestApp()
	state := &connectState{
		client:   spotify.NewConnect("dev1", "Sonora", nil),
		observer: true,
		receiver: true,
		jobs:     make(chan func() error, 16),
		reports:  make(chan func() error, 16),
		done:     make(chan struct{}),
		tracker:  newPlaybackTracker(now),
	}
	defer close(state.done)
	go a.runConnectJobs(state, state.jobs)
	a.connect = state

	state.tracker.start("spotify:track:sp1", "m1", "s1", 0, 180_000, 128000, 10, true, now)
	now = now.Add(35 * time.Second)

	localTrack := spotify.Track{ID: "local:abc", Name: "Local Song", LocalPath: "/tmp/a.mp3"}
	a.connectControl("play", player.QueueRequest{Tracks: []spotify.Track{localTrack}, Start: 0})

	select {
	case <-doneReport:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for track_data_finalized")
	}
	select {
	case <-doneTSV:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TrackStreamVerification")
	}

	mu.Lock()
	defer mu.Unlock()
	if !state.local {
		t.Fatal("state.local was not set to true for local track")
	}
	if len(reported) == 0 || reported[0].DebugSource != "track_data_finalized" {
		t.Fatalf("expected track_data_finalized, got %+v", reported)
	}
	if len(tsvs) == 0 || tsvs[0].PlayTrack != "spotify:track:sp1" || tsvs[0].MSPlayed != 35_000 {
		t.Fatalf("unexpected TSV on Spotify->local switch: %+v", tsvs)
	}
}

// Routine periodic progress pings must not overwrite an unstarted high-priority lifecycle report.
func TestPriorityConnectReportNotOverwrittenByProgress(t *testing.T) {
	var job latestConnectJob
	var ran []string
	var pending func() error
	enqueue := func(fn func() error) bool {
		pending = fn
		return true
	}
	if !queueLatestConnectPriority(&job, enqueue, true, func() error { ran = append(ran, "before_track_load"); return nil }) {
		t.Fatal("priority job failed to queue")
	}
	if queueLatestConnectPriority(&job, enqueue, false, func() error { ran = append(ran, "progress"); return nil }) {
		t.Fatal("low-priority progress should not overwrite queued priority job")
	}
	_ = pending()
	if len(ran) != 1 || ran[0] != "before_track_load" {
		t.Fatalf("ran = %v, want [before_track_load]", ran)
	}
}

// replace_state pendingPlay adoption must preserve local shuffle/repeat instead of letting stale state.options overwrite them.
func TestReplaceStatePreservesLocalShuffleAndRepeat(t *testing.T) {
	a := newTestApp()
	a.pl.SetShuffleMode(player.ShuffleOn)
	a.pl.SetRepeatMode(player.RepeatOne)
	falseVal := false
	state := &connectState{
		client:      spotify.NewConnect("dev1", "Sonora", nil),
		observer:    true,
		receiver:    true,
		pendingPlay: "spotify:track:t1",
		options:     spotify.ConnectOptions{Shuffle: &falseVal, RepeatContext: &falseVal, RepeatTrack: &falseVal},
		jobs:        make(chan func() error, 8),
		reports:     make(chan func() error, 8),
		done:        make(chan struct{}),
		tracker:     newPlaybackTracker(time.Now()),
	}
	defer close(state.done)
	a.connect = state

	cmdPayload, _ := json.Marshal(map[string]any{
		"type":      "replace_state",
		"state_ref": map[string]any{"state_index": 0, "paused": false},
		"state_machine": map[string]any{
			"state_machine_id": "m1",
			"tracks":           []any{map[string]any{"metadata": map[string]any{"uri": "spotify:track:t1", "name": "Song", "duration": 180000}}},
			"states":           []any{map[string]any{"state_id": "s1", "track": 0}},
			"attributes": map[string]any{
				"options": map[string]any{"shuffling_context": false, "repeating_context": false, "repeating_track": false},
			},
		},
	})
	a.connectEvent(spotify.ConnectEvent{Kind: "command", Data: cmdPayload})
	if !a.pl.Shuffle() || a.pl.Repeat() != player.RepeatOne {
		t.Fatalf("local modes overwritten: shuffle=%v repeat=%v", a.pl.Shuffle(), a.pl.Repeat())
	}
}
