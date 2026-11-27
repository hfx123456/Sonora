package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"vinyl/internal/spotify"
)

// connectNow is time.Now; tests swap it for deterministic playback duration math.
var connectNow = time.Now

// sendTrackStreamVerification is spotify.SendTrackStreamVerification; tests swap it.
var sendTrackStreamVerification = spotify.SendTrackStreamVerification

// sendTrackStreamVerificationWithID sends a TSV with a pre-captured identity
// snapshot (used when signing out so credentials remain valid until delivery).
var sendTrackStreamVerificationWithID = spotify.SendTrackStreamVerificationWithIdentity

const (
	startedPlayingThreshold  int64 = 1000
	playedCountThreshold     int64 = 31000
	progressReportInterval         = 15 * time.Second
	maxFlushStepMs           int64 = int64(30 * time.Minute / time.Millisecond)
	harmonyInternalContext         = "spotify:internal:harmony-play-tracks"
)

func keySystemName() string {
	if runtime.GOOS == "darwin" {
		return "fairplay"
	}
	return "widevine"
}

func normalizeConnectContextURI(raw string) string {
	uri := strings.TrimSpace(raw)
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		uri = uri[:i]
	}
	for _, prefix := range []string{
		"spotify:playlist:",
		"spotify:album:",
		"spotify:artist:",
		"spotify:show:",
		"spotify:user:",
	} {
		if strings.HasPrefix(uri, prefix) && len(uri) > len(prefix) {
			return uri
		}
	}
	return harmonyInternalContext
}

func (a *app) contextURIFor(from spotify.HomeItem) string {
	id := strings.TrimSpace(from.ID())
	if id == "" {
		return ""
	}
	if id == spotify.LikedURI {
		if a.profile != nil && a.profile.UserID != "" {
			return "spotify:user:" + a.profile.UserID + ":collection"
		}
		return ""
	}
	if from.Artist != nil && !strings.Contains(id, ":") {
		return "spotify:artist:" + id
	}
	return normalizeConnectContextURI(id)
}

func randomHexID(bytesLen int) string {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(buf)
}

// playbackTracker tracks active listening duration (excluding pauses and seek
// jumps) for a single loaded track so Spotify receives track-playback lifecycle
// transitions and Melody TrackStreamVerification telemetry.
type playbackTracker struct {
	mu           sync.Mutex
	sessionID    string
	seqID        int64
	active       bool
	uri          string
	machineID    string
	stateID      string
	startPosMs   int64
	lastPosMs    int64
	durationMs   int64
	bitrate      int
	format       int
	msPlayed     int64
	playingSince time.Time
	played       bool
	startedSent  bool
	threshSent   bool
	prevEndPos   *int64
	tsvWG        sync.WaitGroup
}

func newPlaybackTracker(now time.Time) *playbackTracker {
	return &playbackTracker{
		sessionID: formatMillisString(now),
	}
}

func formatMillisString(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return strconv.FormatInt(t.UnixMilli(), 10)
}

func (pt *playbackTracker) flushLocked(now time.Time) {
	if !pt.active || pt.playingSince.IsZero() {
		return
	}
	delta := now.Sub(pt.playingSince).Milliseconds()
	maxDelta := maxFlushStepMs
	if pt.durationMs > 0 && pt.durationMs*2 > maxDelta {
		maxDelta = pt.durationMs * 2
	}
	// Ignore wall-clock leaps across OS sleep/hibernation so suspended time
	// is never attributed as listening duration.
	if delta > 0 && delta <= maxDelta {
		pt.msPlayed += delta
		pt.played = true
	}
	pt.playingSince = now
}

func (pt *playbackTracker) takePrevPosLocked() *int64 {
	if pt.prevEndPos == nil {
		return nil
	}
	v := *pt.prevEndPos
	pt.prevEndPos = nil
	return &v
}

func (pt *playbackTracker) isActive() bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.active
}

func (pt *playbackTracker) currentURI() string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if !pt.active {
		return ""
	}
	return pt.uri
}

// start initializes tracking for a newly loaded track and returns the consumed
// previous_position (the ending position of the previous track, if any).
func (pt *playbackTracker) start(uri, machineID, stateID string, startPosMs, durMs int64, bitrate, format int, playing bool, now time.Time) *int64 {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.active = uri != ""
	pt.uri = uri
	pt.machineID = machineID
	pt.stateID = stateID
	pt.startPosMs = max(0, startPosMs)
	pt.lastPosMs = max(0, startPosMs)
	pt.durationMs = max(0, durMs)
	pt.bitrate = bitrate
	pt.format = format
	pt.msPlayed = 0
	pt.played = playing
	pt.startedSent = false
	pt.threshSent = false
	if playing && pt.active {
		pt.playingSince = now
	} else {
		pt.playingSince = time.Time{}
	}
	return pt.takePrevPosLocked()
}

// bind attaches the Connect state machine identity to an already-running local
// track without losing elapsed msPlayed.
func (pt *playbackTracker) bind(uri, linkedFrom, machineID, stateID string, posMs, durMs int64, bitrate, format int, playing bool, now time.Time, takePrev bool) *int64 {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	matched := pt.active && (pt.uri == uri || (linkedFrom != "" && pt.uri == "spotify:track:"+linkedFrom))
	if !matched {
		pt.active = uri != ""
		pt.uri = uri
		pt.machineID = machineID
		pt.stateID = stateID
		pt.startPosMs = max(0, posMs)
		pt.lastPosMs = max(0, posMs)
		pt.durationMs = max(0, durMs)
		pt.bitrate = bitrate
		pt.format = format
		pt.msPlayed = 0
		pt.played = playing
		pt.startedSent = false
		pt.threshSent = false
		if playing && pt.active {
			pt.playingSince = now
		} else {
			pt.playingSince = time.Time{}
		}
		if takePrev {
			return pt.takePrevPosLocked()
		}
		return nil
	}
	pt.uri = uri
	pt.machineID = machineID
	pt.stateID = stateID
	if playing {
		if pt.playingSince.IsZero() {
			pt.playingSince = now
		} else {
			pt.flushLocked(now)
		}
		pt.played = true
	} else if !pt.playingSince.IsZero() {
		pt.flushLocked(now)
		pt.playingSince = time.Time{}
	}
	if posMs >= 0 {
		pt.lastPosMs = posMs
	}
	if durMs > 0 {
		pt.durationMs = durMs
	}
	if bitrate > 0 {
		pt.bitrate = bitrate
	}
	if format > 0 {
		pt.format = format
	}
	if takePrev {
		return pt.takePrevPosLocked()
	}
	return nil
}

// update advances elapsed played time while actively playing and returns a
// pending lifecycle debug_source milestone ("started_playing" or
// "played_threshold_reached") if one has been crossed and not yet confirmed.
func (pt *playbackTracker) update(playing bool, posMs, durMs int64, bitrate, format int, now time.Time) string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if !pt.active {
		return ""
	}
	if playing {
		if pt.playingSince.IsZero() {
			pt.playingSince = now
		} else {
			pt.flushLocked(now)
		}
		pt.played = true
	} else if !pt.playingSince.IsZero() {
		pt.flushLocked(now)
		pt.playingSince = time.Time{}
	}
	if posMs >= 0 {
		pt.lastPosMs = posMs
	}
	if durMs > 0 {
		pt.durationMs = durMs
	}
	if bitrate > 0 {
		pt.bitrate = bitrate
	}
	if format > 0 {
		pt.format = format
	}
	if !pt.threshSent && pt.msPlayed >= playedCountThreshold {
		return "played_threshold_reached"
	}
	if !pt.startedSent && pt.msPlayed >= startedPlayingThreshold {
		return "started_playing"
	}
	return ""
}

// confirmMilestone records that a lifecycle milestone was actually queued for
// delivery so a dropped report while Connect is still binding can be retried.
func (pt *playbackTracker) confirmMilestone(name string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if !pt.active {
		return
	}
	switch name {
	case "played_threshold_reached":
		pt.threshSent = true
		pt.startedSent = true
	case "started_playing":
		pt.startedSent = true
	}
}

// seek flushes any elapsed playback time up to now and records newPosMs without
// inflating msPlayed by the seek distance.
func (pt *playbackTracker) seek(prevPosMs, newPosMs int64, now time.Time) int64 {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if pt.active {
		pt.flushLocked(now)
		if newPosMs >= 0 {
			pt.lastPosMs = newPosMs
		}
	}
	return max(0, prevPosMs)
}

func (pt *playbackTracker) matchesState(machineID, uri, stateID string) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if !pt.active || pt.uri != uri {
		return false
	}
	if pt.machineID != "" && machineID != "" && pt.machineID != machineID {
		return false
	}
	return pt.stateID == "" || stateID == "" || pt.stateID == stateID
}

func (pt *playbackTracker) clearPrevEndPos() {
	pt.mu.Lock()
	pt.prevEndPos = nil
	pt.mu.Unlock()
}

// finalize stops tracking the active track and produces any track_data_finalized
// state report (when Connect owns a state_ref) and TrackStreamVerification message.
func (pt *playbackTracker) finalize(finalPosMs int64, paused bool, nextStateID string, now time.Time) (*spotify.ConnectStateReport, *spotify.TrackStreamVerification) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	if !pt.active {
		return nil, nil
	}
	if !pt.playingSince.IsZero() {
		pt.flushLocked(now)
	}
	if finalPosMs < 0 {
		finalPosMs = pt.lastPosMs
	}
	endPos := max(0, finalPosMs)
	uri := pt.uri
	machineID := pt.machineID
	stateID := pt.stateID
	startPos := pt.startPosMs
	durMs := pt.durationMs
	bitrate := pt.bitrate
	format := pt.format
	msPlayed := pt.msPlayed
	played := pt.played

	pt.active = false
	pt.uri = ""
	pt.machineID = ""
	pt.stateID = ""
	pt.playingSince = time.Time{}
	pt.msPlayed = 0
	pt.played = false
	pt.startedSent = false
	pt.threshSent = false
	pt.prevEndPos = &endPos

	var report *spotify.ConnectStateReport
	if machineID != "" && stateID != "" && (played || msPlayed > 0) {
		prev := endPos
		report = &spotify.ConnectStateReport{
			StateRef: map[string]any{
				"state_machine_id": machineID,
				"state_id":         stateID,
				"paused":           paused,
			},
			Position:         endPos,
			Duration:         durMs,
			Paused:           paused,
			PreviousPosition: &prev,
			Bitrate:          bitrate,
			Format:           format,
			DebugSource:      "track_data_finalized",
			PlaybackStats: &spotify.ConnectPlaybackStats{
				MSTotalEst:         durMs,
				MSMetadataDuration: durMs,
				StartOffsetMS:      startPos,
				StartBitrate:       bitrate,
				AudioStartBitrate:  bitrate,
				KeySystem:          keySystemName(),
				LocalTimeMS:        now.UnixMilli(),
			},
		}
	}

	var tsv *spotify.TrackStreamVerification
	if strings.HasPrefix(uri, "spotify:track:") && msPlayed > 0 {
		pt.seqID++
		playbackID := stateID
		if playbackID == "" {
			playbackID = randomHexID(16)
		}
		tsv = &spotify.TrackStreamVerification{
			PlayTrack:       uri,
			PlaybackID:      playbackID,
			MSPlayed:        msPlayed,
			MSNominalPlayed: msPlayed,
			SessionID:       pt.sessionID,
			SequenceID:      pt.seqID,
			NextPlaybackID:  nextStateID,
			PlaybackService: "track-playback",
		}
	}
	return report, tsv
}

// enqueueFinalReport serializes track_data_finalized onto state.jobs so it
// always runs strictly before any subsequent play/Report(nil) command on the
// same Connect device, and logs failures silently without popping a user toast.
func (a *app) enqueueFinalReport(state *connectState, report *spotify.ConnectStateReport) {
	if state == nil || report == nil || state.client == nil || !state.receiver {
		return
	}
	rpt := *report
	select {
	case state.jobs <- func() error {
		if err := connectReport(state.client, rpt); err != nil {
			log.Printf("connect: track_data_finalized: %v", err)
		}
		return nil
	}:
	default:
	}
}

func (a *app) emitTrackStreamVerification(state *connectState, tsv *spotify.TrackStreamVerification, preset spotify.Identity) {
	if state == nil || state.tracker == nil || tsv == nil {
		return
	}
	msg := *tsv
	state.tracker.tsvWG.Add(1)
	go func() {
		defer state.tracker.tsvWG.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var err error
		if preset.Token != "" && !preset.Anonymous {
			err = sendTrackStreamVerificationWithID(ctx, msg, preset)
		} else {
			err = sendTrackStreamVerification(ctx, msg)
		}
		if err != nil {
			log.Printf("melody: tsv: %v", err)
		}
	}()
}

// finalizePlayback finalizes the currently tracked Spotify track (if any),
// queuing its track_data_finalized state report and sending TrackStreamVerification.
func (a *app) finalizePlayback(nextStateID string) {
	a.finalizePlaybackWith(nextStateID, true)
}

func (a *app) finalizePlaybackWith(nextStateID string, sendConnectFinal bool) {
	state := a.connect
	if state == nil || state.tracker == nil || a.pl.IsRemote() || !state.tracker.isActive() {
		return
	}
	posMs := int64(-1)
	if cur := a.pl.Current(); cur != nil && cur.URI() == state.tracker.currentURI() {
		posMs = a.pl.Position().Milliseconds()
		if durMs := a.pl.Duration().Milliseconds(); durMs > 0 {
			posMs = min(posMs, durMs)
		}
	}
	paused := !a.pl.Playing()
	preset := spotify.CachedIdentity()
	rpt, tsv := state.tracker.finalize(posMs, paused, nextStateID, connectNow())
	if sendConnectFinal && rpt != nil {
		a.enqueueFinalReport(state, rpt)
	}
	if tsv != nil {
		a.emitTrackStreamVerification(state, tsv, preset)
	}
}

// connectPlaybackChanged keeps the playback tracker and Connect play state in
// sync with asynchronous player transitions (ready, system media keys, radio/
// Smart Shuffle feed arrivals, or error skips).
func (a *app) connectPlaybackChanged() {
	state := a.connect
	if state == nil || state.tracker == nil || state.applying || a.pl.IsRemote() {
		return
	}
	cur := a.pl.Current()
	if cur == nil || cur.Local() {
		if state.tracker.isActive() {
			a.finalizePlayback("")
		}
		return
	}
	now := connectNow()
	posMs := a.pl.Position().Milliseconds()
	durMs := a.pl.Duration().Milliseconds()
	if durMs == 0 {
		durMs = cur.Length().Milliseconds()
	}
	bitrate, format, _ := a.pl.MediaInfo()
	playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
	trackedURI := state.tracker.currentURI()
	if trackedURI != "" && trackedURI != cur.URI() && !a.pl.Awaiting() {
		// An async transition (radio/Smart Shuffle feed, error skip, or
		// state.lost progression) moved to a new track outside playHere.
		a.finalizePlayback("")
		state.ref = nil
		state.playVersion.Add(1)
		state.tracker.start(cur.URI(), "", "", posMs, durMs, bitrate, format, playing, now)
		if !state.lost && (a.pl.Playing() || a.pl.Loading()) {
			state.pendingPlay = cur.URI()
			a.sendLocalConnectPlay()
		}
		return
	}
	if state.tracker.isActive() {
		if milestone := state.tracker.update(playing, posMs, durMs, bitrate, format, now); milestone != "" {
			if a.reportConnectWith(milestone, nil) {
				state.tracker.confirmMilestone(milestone)
				state.lastProgressAt = now
			}
		}
	}
}
