package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo/ui"
	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

type connectState struct {
	local                        bool
	user                         string
	client                       *spotify.Connect
	cluster                      spotify.ConnectCluster
	machine                      spotify.ConnectMachine
	ref                          *spotify.ConnectRef
	observer, receiver, applying bool
	// lost tells that Spotify turned down the state this device reported and
	// gave no other: it plays on here, on its own, until Spotify gives one.
	lost        bool
	err         string
	open        bool
	known       map[string]spotify.Track // what Decorate named, by URI
	decorating  bool
	timer       *time.Timer
	jobs        chan func() error
	reports     chan func() error
	done        chan struct{}
	playJob        latestConnectJob
	reportJob      latestConnectJob
	playVersion    atomic.Uint64
	contextURI     string
	tracker        *playbackTracker
	lastProgressAt time.Time
	// pendingPlay is the locally selected URI awaiting a server state.
	// Earlier selections must not restart or replace the current audio.
	pendingPlay string
	playSent    uint64 // the selection already registered or awaiting its reply
	localQueue  bool   // keep the native queue until a matching cluster arrives
	// The volume asked of a device, sent once the request before it is
	// done: a drag turns it a percent at a time, which one by one would
	// walk the device there long after the drag.
	volMu     sync.Mutex
	volTarget string
	volume    float64
	volQueued bool
	// volLocal is when the listener last turned the volume here: Spotify
	// sends what it was told back, a step behind.
	volLocal time.Time
	// options are shuffle and repeat as Spotify last said; shuffle and
	// repeat what the listener set since, which it may not say yet.
	options spotify.ConnectOptions
	shuffle held[bool]
	repeat  held[player.RepeatMode]
}

// held is a setting changed here, shown as changed while what Spotify says
// is from before it took the change: until, having taken it, it says the
// same, or a while after it took it, or it would not take it. Pressed
// twice, a button can ask for what Spotify said before: that is not
// Spotify having taken it, nor is its answer to the first press, which
// comes after. Nor, as it may take the presses out of order, is its saying
// the same before it took the latest, if it said otherwise since.
type held[T comparable] struct {
	value T
	on    bool
	ask   int       // counts the changes
	until time.Time // zero while Spotify is asked
	said  bool      // the latest Spotify said, since the change, is value
}

// heldFor is how long after Spotify took a change it is held at most.
const heldFor = 10 * time.Second

// set holds v, which Spotify is asked for, and returns which change it is.
func (h *held[T]) set(v T) int {
	h.ask++
	h.value, h.on, h.until, h.said = v, true, time.Time{}, false
	return h.ask
}

// answered takes Spotify's answer to change ask, if it is the latest: it
// took it, or would not, which lets go of it.
func (h *held[T]) answered(ask int, took bool) {
	switch {
	case ask != h.ask:
	case took:
		h.until = time.Now().Add(heldFor)
	default:
		h.on = false
	}
}

// show returns what to show of the setting, got as Spotify said it last,
// if known: fresh if Spotify says it now. The value held shows until
// Spotify, having taken it, last said the same, or the hold is over.
func (h *held[T]) show(got T, known, fresh bool) (T, bool) {
	if h.on && fresh {
		h.said = got == h.value
	}
	if h.on && !h.until.IsZero() && (h.said || known && time.Now().After(h.until)) {
		h.on = false
	}
	if h.on {
		return h.value, true
	}
	return got, known
}

// connectOptions shows shuffle and repeat as Spotify says, o: but those the
// listener just set as they set them. What Spotify said meanwhile, from
// before, undid the button pressed, as much as a few seconds. Empty, it
// shows again what Spotify said last.
func (a *app) connectOptions(o spotify.ConnectOptions) {
	state := a.connect
	shuffleSaid := o.Shuffle != nil
	if shuffleSaid {
		state.options.Shuffle = o.Shuffle
	}
	_, repeatSaid := player.RepeatOf(o)
	if repeatSaid {
		state.options.RepeatContext, state.options.RepeatTrack = o.RepeatContext, o.RepeatTrack
	}
	o = state.options
	if v, ok := state.shuffle.show(o.Shuffle != nil && *o.Shuffle, o.Shuffle != nil, shuffleSaid); ok {
		o.Shuffle = &v
	}
	mode, known := player.RepeatOf(o)
	if mode, ok := state.repeat.show(mode, known, repeatSaid); ok {
		all, one := mode == player.RepeatAll, mode == player.RepeatOne
		o.RepeatContext, o.RepeatTrack = &all, &one
	}
	a.pl.ConnectOptions(o)
}

// connectCommand is (*spotify.Connect).Command; tests swap it.
var connectCommand = (*spotify.Connect).Command

// connectReport is (*spotify.Connect).ReportState; tests swap it.
var connectReport = (*spotify.Connect).ReportState

func (a *app) stopConnect() {
	a.stopConnectWait(0)
}

func (a *app) stopConnectWait(wait time.Duration) {
	state := a.connect
	a.connect = nil
	if state == nil {
		return
	}
	if state.timer != nil {
		state.timer.Stop()
	}
	var finalTSV *spotify.TrackStreamVerification
	presetID := spotify.CachedIdentity()
	if state.tracker != nil && state.tracker.isActive() && !a.pl.IsRemote() {
		posMs := int64(-1)
		durMs := int64(0)
		if cur := a.pl.Current(); cur != nil && cur.URI() == state.tracker.currentURI() {
			posMs = a.pl.Position().Milliseconds()
			durMs = a.pl.Duration().Milliseconds()
			if durMs > 0 {
				posMs = min(posMs, durMs)
			}
		}
		paused := !a.pl.Playing()
		if state.client != nil && posMs >= 0 {
			state.client.UpdateLastPosition(posMs, durMs, paused)
		}
		_, finalTSV = state.tracker.finalize(posMs, paused, "", connectNow())
	}
	close(state.done)
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		var wg sync.WaitGroup
		if state.client != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				state.client.Close()
			}()
		}
		if finalTSV != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				var err error
				if presetID.Token != "" && !presetID.Anonymous {
					err = sendTrackStreamVerificationWithID(ctx, *finalTSV, presetID)
				} else {
					err = sendTrackStreamVerification(ctx, *finalTSV)
				}
				if err != nil {
					log.Printf("melody: final tsv: %v", err)
				}
			}()
		}
		if state.tracker != nil {
			state.tracker.tsvWG.Wait()
		}
		wg.Wait()
	}()
	a.pl.Control = nil
	if a.pl.IsRemote() {
		a.pl.StopConnect()
	}
	if wait > 0 {
		select {
		case <-closed:
		case <-time.After(wait):
		}
	}
}

func (a *app) startConnect() {
	if a.connect != nil && a.profile != nil && a.connect.user == a.profile.UserID {
		return
	}
	a.stopConnect()
	if a.profile == nil || shotHook != nil || a.quitting {
		return
	}
	hostname, _ := os.Hostname()
	sum := sha256.Sum256([]byte("Sonora/" + a.profile.UserID + "/" + hostname))
	state := &connectState{
		user:    a.profile.UserID,
		jobs:    make(chan func() error, 32),
		reports: make(chan func() error, 16),
		done:    make(chan struct{}),
		tracker: newPlaybackTracker(connectNow()),
	}
	state.client = spotify.NewConnect(hex.EncodeToString(sum[:20]), appName+" · "+hostname, func(event spotify.ConnectEvent) {
		a.post(func() {
			if a.connect == state {
				a.connectEvent(event)
			}
		})
	})
	a.connect = state
	state.contextURI = a.pl.ContextURI()
	if cur := a.pl.Current(); cur != nil && cur.Local() {
		state.local = true
	}
	a.pl.Control = a.connectControl
	go a.runConnectJobs(state, state.jobs)
	// A slow periodic report cannot hold user commands behind its HTTP request.
	go a.runConnectJobs(state, state.reports)
	state.client.Start()
	a.connectTick(state)
}

func (a *app) runConnectJobs(state *connectState, jobs <-chan func() error) {
	for {
		select {
		case <-state.done:
			return
		case job := <-jobs:
			select {
			case <-state.done:
				return
			default:
			}
			if err := job(); err != nil {
				log.Printf("connect: %v", err)
				a.post(func() {
					if a.connect == state {
						state.err = connectMessage(err)
						a.toast = state.err
					}
				})
			}
		}
	}
}

func (a *app) connectJob(job func() error) bool {
	if a.connect == nil {
		return false
	}
	select {
	case a.connect.jobs <- job:
		return true
	default:
		a.toast = "Connect 操作正在排队，请稍后再试"
		return false
	}
}

// connectVolume is (*spotify.Connect).Volume; tests swap it.
var connectVolume = (*spotify.Connect).Volume

// sendVolume asks target for volume v, or has the request already waiting
// ask for it instead.
func (a *app) sendVolume(state *connectState, target string, v float64) {
	state.volMu.Lock()
	queued := state.volQueued
	state.volTarget, state.volume, state.volQueued = target, v, true
	state.volMu.Unlock()
	if queued {
		return
	}
	sent := a.connectJob(func() error {
		state.volMu.Lock()
		target, v := state.volTarget, state.volume
		state.volQueued = false
		state.volMu.Unlock()
		return connectVolume(state.client, target, v)
	})
	if !sent {
		state.volMu.Lock()
		state.volQueued = false
		state.volMu.Unlock()
	}
}

// here does to the player what fn does, as playing here, not through
// Connect.
func (a *app) here(fn func()) {
	control := a.pl.Control
	a.pl.Control = nil
	defer func() { a.pl.Control = control }()
	fn()
}

func (a *app) connectTick(state *connectState) {
	state.timer = time.AfterFunc(1*time.Second, func() {
		a.post(func() {
			if a.connect != state || a.quitting {
				return
			}
			now := connectNow()
			var milestone string
			if state.tracker != nil && !state.local && !a.pl.IsRemote() {
				posMs := a.pl.Position().Milliseconds()
				durMs := a.pl.Duration().Milliseconds()
				bitrate, format, _ := a.pl.MediaInfo()
				playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
				milestone = state.tracker.update(playing, posMs, durMs, bitrate, format, now)
			}
			a.sendLocalConnectPlay()
			if milestone != "" {
				if a.reportConnectWith(milestone, nil) {
					state.tracker.confirmMilestone(milestone)
					state.lastProgressAt = now
				}
			} else if now.Sub(state.lastProgressAt) >= progressReportInterval {
				if a.reportConnectWith("progress", nil) {
					state.lastProgressAt = now
				}
			}
			a.connectTick(state)
		})
	})
}

func (a *app) reportConnect() {
	a.reportConnectWith("progress", nil)
}

func (a *app) reportConnectWith(debugSource string, previousPos *int64) bool {
	state := a.connect
	if state != nil && !state.local {
		if cur := a.pl.Current(); cur != nil && cur.Local() {
			state.local, state.ref = true, nil
		}
	}
	allowLoading := debugSource == "before_track_load"
	if state == nil || state.local || state.applying || !state.receiver || state.ref == nil || a.pl.IsRemote() || (a.pl.Loading() && !allowLoading) {
		return false
	}
	index := state.ref.Index
	if index < 0 || index >= len(state.machine.States) {
		return false
	}
	paused := !a.pl.Playing()
	state.ref.Paused = paused
	sent := state.machine.States[index].ID
	ref := map[string]any{"state_machine_id": state.machine.ID, "state_id": sent, "paused": paused}
	pos, duration := a.pl.Position().Milliseconds(), a.pl.Duration().Milliseconds()
	if duration == 0 {
		if cur := a.pl.Current(); cur != nil {
			duration = cur.Length().Milliseconds()
		}
	}
	if duration > 0 {
		// The page's clock runs a little past the end of the song.
		pos = min(pos, duration)
	}
	bitrate, format, _ := a.pl.MediaInfo()
	var prevCopy *int64
	if previousPos != nil {
		v := *previousPos
		prevCopy = &v
	}
	report := spotify.ConnectStateReport{
		StateRef:         ref,
		Position:         pos,
		Duration:         duration,
		Paused:           paused,
		PreviousPosition: prevCopy,
		Bitrate:          bitrate,
		Format:           format,
		DebugSource:      debugSource,
	}
	version := state.playVersion.Load()
	priority := debugSource != "" && debugSource != "progress"
	return queueLatestConnectPriority(&state.reportJob, func(job func() error) bool {
		select {
		case state.reports <- job:
			return true
		default:
			return false
		}
	}, priority, func() error {
		if version != state.playVersion.Load() {
			return nil
		}
		// Nothing the listener did: what goes wrong goes to the log, not to a
		// toast that would come back every five seconds.
		err := connectReport(state.client, report)
		if err != nil {
			log.Printf("connect: %v (state %s, %d of %d ms)", err, sent, pos, duration)
		}
		var turned *spotify.ConnectError
		if errors.As(err, &turned) && turned.Status == http.StatusBadRequest {
			a.post(func() {
				if version == state.playVersion.Load() {
					a.connectTurnedDown(state, sent)
				}
			})
		}
		return nil
	})
}

// connectTurnedDown takes Spotify's refusal of the state sent, which it would
// refuse each time after: unless a newer state has come since, this device
// stops reporting, and its controls act here, as Spotify turns those down too.
func (a *app) connectTurnedDown(state *connectState, sent string) {
	if a.connect != state || state.ref == nil || state.machine.States[state.ref.Index].ID != sent {
		return
	}
	log.Printf("connect: state %s refused, playing on here", sent)
	state.ref, state.lost = nil, true
}

func (a *app) connectEvent(event spotify.ConnectEvent) {
	state := a.connect
	if state.local && (event.Kind == "cluster" || event.Kind == "command" || event.Kind == "report") {
		return
	}
	if event.Err != nil {
		state.err = event.Err.Error()
		log.Printf("connect: %s: %v", event.Kind, event.Err)
	}
	switch event.Kind {
	// Ready is logged too: without it, a log with no Connect in it could
	// not tell one that never started from one that never failed.
	case "observer_ready":
		log.Printf("connect: observer ready")
		state.observer = true
		state.err = ""
		a.sendLocalConnectPlay()
	case "receiver_ready":
		log.Printf("connect: ready, as %q", state.client.Name)
		state.receiver = true
		state.lost = false
		state.err = ""
		a.sendLocalConnectPlay()
	case "observer_offline":
		state.observer = false
		state.playVersion.Add(1)
		state.playSent = 0
	case "receiver_offline":
		state.receiver = false
		state.playVersion.Add(1)
		state.playSent = 0
		if state.ref != nil {
			state.ref = nil
			// Losing the Connect WebSocket (e.g. proxy node switch) must not
			// pause or reset local playback; re-register once back online.
			if !a.pl.IsRemote() {
				if track := a.pl.Current(); track != nil {
					state.pendingPlay = track.URI()
				}
			}
		}
	case "cluster":
		cluster, err := spotify.ParseConnectCluster(event.Data)
		if err != nil {
			return
		}
		if cluster.Timestamp > 0 && cluster.Timestamp < state.cluster.Timestamp {
			return
		}
		if cluster.Timestamp == 0 && state.cluster.Timestamp > 0 {
			if cluster.Active != "" && cluster.Active != state.client.ID && state.ref != nil {
				return
			}
			cluster.Timestamp = state.cluster.Timestamp
		}
		if cluster.Active != "" && cluster.Active != state.client.ID && state.ref != nil && cluster.Timestamp > 0 && cluster.Timestamp == state.cluster.Timestamp {
			return
		}
		state.cluster = cluster
		// Playing here, shuffle and repeat are those of the state Spotify
		// gave this device: the cluster's are what it reported, a step
		// behind, and set the buttons back after the state had moved on.
		if cluster.Active != "" && (cluster.Active != state.client.ID || (state.ref == nil && a.settings.ShuffleMode == 0 && a.settings.RepeatMode == 0)) {
			a.connectOptions(cluster.Player.Options)
		}
		if cluster.Active != "" && cluster.Active != state.client.ID {
			if !a.pl.IsRemote() && state.tracker != nil {
				// Another device already took playback ownership; skip sending
				// a doomed track_data_finalized PUT on our revoked state_ref
				// while still emitting TrackStreamVerification.
				a.finalizePlaybackWith("", false)
				state.tracker.clearPrevEndPos()
			}
			state.contextURI = ""
			state.pendingPlay = ""
			state.localQueue = false
			state.playVersion.Add(1)
			state.ref, state.lost = nil, false
			track := cluster.Track()
			if track.ID != "" {
				position := int64(cluster.Player.Position)
				if cluster.Player.Playing && !cluster.Player.Paused && cluster.Timestamp > cluster.Player.Timestamp && cluster.Player.Timestamp > 0 {
					position += int64(cluster.Timestamp - cluster.Player.Timestamp)
				}
				state.applying = true
				a.pl.MirrorConnect(track, time.Duration(max(0, position))*time.Millisecond, cluster.Player.Playing && !cluster.Player.Paused)
				state.applying = false
				a.showConnectQueue(cluster.Queue())
			} else {
				a.pl.StopConnect()
			}
		} else if cluster.Active == state.client.ID && state.ref != nil {
			// Keep the native local queue (history + Smart Shuffle) intact when
			// playback was started locally instead of truncating it to Connect's
			// [current, next...] window.
			a.connectQueue(state.ref.Index)
		}
	case "report":
		if state.ref == nil {
			return
		}
		var response struct {
			Machine spotify.ConnectMachine `json:"state_machine"`
			Updated *spotify.ConnectRef    `json:"updated_state_ref"`
		}
		if json.Unmarshal(event.Data, &response) != nil || response.Machine.ID == "" {
			return
		}
		currentID := state.machine.States[state.ref.Index].ID
		for index, next := range response.Machine.States {
			if response.Updated != nil && response.Updated.Index == index || response.Updated == nil && next.ID == currentID {
				track, err := response.Machine.Track(index)
				if next := next.Transitions.Advance; err == nil && next != nil && a.pl.Current() != nil && track.URI() != a.pl.Current().URI() {
					// The answer to a report from before the song ended: the
					// song now is the one after. Dropped, this device went on
					// reporting states Spotify had replaced, each turned down.
					index = next.Index
					track, err = response.Machine.Track(index)
				}
				if err == nil && a.pl.Current() != nil && (track.URI() == a.pl.Current().URI() || (track.LinkedFrom != "" && "spotify:track:"+track.LinkedFrom == a.pl.Current().URI())) {
					state.err = ""
					state.machine = response.Machine
					state.ref.Index = index
					if state.tracker != nil && index >= 0 && index < len(state.machine.States) {
						posMs, durMs := a.pl.Position().Milliseconds(), a.pl.Duration().Milliseconds()
						bitrate, format, _ := a.pl.MediaInfo()
						playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
						state.tracker.bind(track.URI(), track.LinkedFrom, state.machine.ID, state.machine.States[index].ID, posMs, durMs, bitrate, format, playing, connectNow(), false)
					}
					a.connectQueue(index)
				}
				break
			}
		}
	case "command":
		var command spotify.ConnectCommand
		if json.Unmarshal(event.Data, &command) != nil {
			return
		}
		switch command.Type {
		case "replace_state":
			if state.pendingPlay != "" {
				if command.Ref == nil {
					// Revoking an earlier context may arrive during a new play.
					// A cluster transfer to another device still takes ownership.
					return
				}
				track, err := command.Machine.Track(command.Ref.Index)
				matched := err == nil && (track.URI() == state.pendingPlay || track.LinkedFrom != "" && "spotify:track:"+track.LinkedFrom == state.pendingPlay)
				if !matched {
					return
				}
				// The audio has already started (and may since have paused or
				// sought). Adopt the identity, keeping that newer local state.
				ref := *command.Ref
				ref.Paused = !a.pl.Playing()
				state.err = ""
				state.pendingPlay = ""
				state.machine = command.Machine
				state.ref, state.lost = &ref, false
				state.cluster.Active = state.client.ID
				localShuffle := a.pl.ShuffleMode()
				localRepeat := a.pl.Repeat()
				opts := state.machine.Attributes.Options
				if localShuffle != player.ShuffleOff && !state.shuffle.on {
					on := true
					opts.Shuffle = &on
				}
				if localRepeat != player.RepeatOff && !state.repeat.on {
					all, one := localRepeat == player.RepeatAll, localRepeat == player.RepeatOne
					opts.RepeatContext, opts.RepeatTrack = &all, &one
				}
				a.connectOptions(opts)
				if state.tracker != nil {
					stateID := ""
					if ref.Index >= 0 && ref.Index < len(state.machine.States) {
						stateID = state.machine.States[ref.Index].ID
					}
					posMs, durMs := a.pl.Position().Milliseconds(), a.pl.Duration().Milliseconds()
					if durMs == 0 {
						durMs = track.Length().Milliseconds()
					}
					bitrate, format, _ := a.pl.MediaInfo()
					playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
					prevPos := state.tracker.bind(track.URI(), track.LinkedFrom, state.machine.ID, stateID, posMs, durMs, bitrate, format, playing, connectNow(), true)
					a.reportConnectWith("before_track_load", prevPos)
				} else {
					a.reportConnect()
				}
				return
			}
			if command.Ref == nil {
				a.finalizePlaybackWith("", false)
				if state.tracker != nil {
					state.tracker.clearPrevEndPos()
				}
				state.contextURI = ""
				state.playVersion.Add(1)
				state.localQueue = false
				state.ref = nil
				if !a.pl.IsRemote() {
					a.pl.StopConnect()
				}
				a.connectJob(func() error { return state.client.Report(nil, 0, 0, true) })
				return
			}
			if len(command.Previous) > 0 && string(command.Previous) != "null" && state.ref != nil {
				var previous struct {
					Machine string `json:"state_machine_id"`
					State   string `json:"state_id"`
					Paused  bool   `json:"paused"`
				}
				if json.Unmarshal(command.Previous, &previous) == nil && (previous.Machine != state.machine.ID || previous.State != state.machine.States[state.ref.Index].ID || previous.Paused != state.ref.Paused) {
					state.err = "Connect 状态冲突，正在重新同步"
					current := map[string]any{"state_machine_id": state.machine.ID, "state_id": state.machine.States[state.ref.Index].ID, "paused": state.ref.Paused}
					position, duration, paused := a.pl.Position().Milliseconds(), a.pl.Duration().Milliseconds(), !a.pl.Playing()
					a.connectJob(func() error {
						return state.client.Reject(command.Machine, command.Ref, current, position, duration, paused)
					})
					return
				}
			}
			state.machine = command.Machine
			a.applyConnect(command.Ref, command.Seek)
		case "set_volume":
			if command.Volume != nil && time.Since(state.volLocal) > 2*time.Second {
				state.applying = true
				a.volume = float64(min(max(*command.Volume, 0), 65535)) / 65535
				a.pl.SetVolume(a.volume)
				state.applying = false
			}
		case "log_out":
			a.finalizePlaybackWith("", false)
			if state.tracker != nil {
				state.tracker.clearPrevEndPos()
			}
			state.contextURI = ""
			state.pendingPlay = ""
			state.localQueue = false
			state.playVersion.Add(1)
			state.ref = nil
			a.pl.StopConnect()
		case "ping":
			a.reportConnect()
		}
	}
}

func (a *app) applyConnect(ref *spotify.ConnectRef, seek *int64) {
	a.applyConnectWith(ref, seek, false)
}

func (a *app) applyConnectWith(ref *spotify.ConnectRef, seek *int64, forceRestart bool) {
	state := a.connect
	track, err := state.machine.Track(ref.Index)
	if err != nil {
		a.finalizePlaybackWith("", false)
		if state.tracker != nil {
			state.tracker.clearPrevEndPos()
		}
		state.err = err.Error()
		a.toast = state.err
		state.ref = nil
		a.pl.StopConnect()
		return
	}
	nextStateID := ""
	if ref.Index >= 0 && ref.Index < len(state.machine.States) {
		nextStateID = state.machine.States[ref.Index].ID
	}
	sameState := !forceRestart && state.tracker != nil && state.tracker.matchesState(state.machine.ID, track.URI(), nextStateID) && state.ref != nil && !a.pl.IsRemote()
	prevPosMs := a.pl.Position().Milliseconds()
	if !sameState {
		a.finalizePlayback(nextStateID)
	}
	copyRef := *ref
	state.err = ""
	state.localQueue = false
	state.ref, state.lost = &copyRef, false
	state.cluster.Active = state.client.ID
	position := time.Duration(-1)
	if seek != nil {
		position = time.Duration(max(0, *seek)) * time.Millisecond
	}
	state.applying = true
	a.connectOptions(state.machine.Attributes.Options)
	a.pl.ApplyConnect(track, position, ref.Paused)
	a.connectQueue(ref.Index)
	state.applying = false
	if state.tracker != nil && !sameState {
		startPos := a.pl.Position().Milliseconds()
		if position >= 0 {
			startPos = position.Milliseconds()
		}
		bitrate, format, _ := a.pl.MediaInfo()
		playing := !ref.Paused && !a.pl.Loading() && a.pl.Ready()
		prevPos := state.tracker.start(track.URI(), state.machine.ID, nextStateID, startPos, track.Length().Milliseconds(), bitrate, format, playing, connectNow())
		a.reportConnectWith("before_track_load", prevPos)
	} else {
		if state.tracker != nil && sameState {
			now := connectNow()
			if position >= 0 {
				state.tracker.seek(prevPosMs, position.Milliseconds(), now)
			}
			bitrate, format, _ := a.pl.MediaInfo()
			playing := !ref.Paused && !a.pl.Loading() && a.pl.Ready()
			state.tracker.update(playing, a.pl.Position().Milliseconds(), track.Length().Milliseconds(), bitrate, format, now)
		}
		a.reportConnect()
	}
}

// connectQueue shows what follows the machine's state index as Spotify's
// apps do, from the cluster: the machine holds only the track and the one
// after. Until the cluster has come to the track, the machine's two.
func (a *app) connectQueue(index int) {
	state := a.connect
	if state.localQueue {
		return
	}
	track, err := state.machine.Track(index)
	if err != nil {
		return
	}
	queue := state.cluster.Queue()
	for i := range queue {
		if queue[i].URI() == track.URI() {
			track.UID = queue[i].UID
			// The machine names some tracks' artists only by URI; the
			// cluster may name them.
			if track.Unnamed() && !queue[i].Unnamed() {
				track.Artists = queue[i].Artists
			}
			queue[i] = track
			a.showConnectQueue(queue[i:])
			return
		}
	}
	a.pl.ConnectQueue(state.machine.Queue(index))
}

// showConnectQueue shows queue, naming the tracks the cluster gives only
// the URIs of: from what Spotify said before, or once it says.
func (a *app) showConnectQueue(queue []spotify.Track) {
	state := a.connect
	missing := false
	for i, t := range queue {
		if !t.Unnamed() {
			continue
		}
		if known, ok := state.known[t.URI()]; ok {
			known.UID = t.UID
			queue[i] = known
		} else {
			missing = true
		}
	}
	a.pl.ConnectQueue(queue)
	if !missing || state.decorating {
		return
	}
	state.decorating = true
	go func() {
		named, err := spotify.Decorate(queue)
		a.post(func() {
			if a.connect != state {
				return
			}
			state.decorating = false
			if err != nil {
				log.Printf("connect: queue: %v", err)
				return
			}
			if state.known == nil || len(state.known) > 2000 {
				state.known = map[string]spotify.Track{}
			}
			// Those Spotify does not name either are kept as they are, not
			// asked of again.
			for i, t := range queue {
				if t.Unnamed() {
					state.known[t.URI()] = named[i]
				}
			}
			if state.ref != nil || a.pl.IsRemote() {
				a.showConnectQueue(append([]spotify.Track(nil), a.pl.Queue()...))
			}
		})
	}()
}

func (a *app) connectControl(action string, value any) bool {
	state := a.connect
	if state != nil && action == "smart_shuffle" {
		if !a.pl.IsRemote() {
			state.localQueue = true
		}
		return false
	}
	if state != nil && action != "play" {
		if cur := a.pl.Current(); cur != nil && cur.Local() {
			state.local, state.ref = true, nil
		}
	}
	if state != nil && action == "play" {
		nextLocal := false
		request := value.(player.QueueRequest)
		for _, track := range request.Tracks {
			nextLocal = nextLocal || track.Local()
		}
		if nextLocal {
			// Finalize any running Spotify track BEFORE setting state.local = true.
			a.finalizePlayback("")
			if state.tracker != nil {
				state.tracker.clearPrevEndPos()
			}
			state.local = true
			state.contextURI = ""
			state.ref = nil
			state.pendingPlay = ""
			state.localQueue = false
			state.playVersion.Add(1)
			if state.cluster.Active == state.client.ID {
				a.connectJob(func() error { return state.client.Command(state.client.ID, "pause", nil) })
			}
		} else {
			state.local = false
		}
	}
	if state != nil && state.local {
		return false
	}
	if state == nil || state.applying || state.lost {
		return false
	}
	if a.localConnectControl(action, value) {
		return true
	}
	localDevice := !a.pl.IsRemote() && (state.cluster.Active == "" || state.cluster.Active == state.client.ID)
	if localDevice {
		switch action {
		case "seek":
			if state.ref != nil && state.ref.Index >= 0 && state.ref.Index < len(state.machine.States) && state.machine.States[state.ref.Index].NoSeek {
				a.toast = "当前内容不支持拖动进度"
				return true
			}
			prevPos := a.pl.Position().Milliseconds()
			targetPos := max(0, value.(time.Duration))
			a.here(func() { a.pl.Seek(targetPos) })
			newPos := a.pl.Position().Milliseconds()
			if state.tracker != nil {
				prevPos = state.tracker.seek(prevPos, newPos, connectNow())
			}
			a.reportConnectWith("seek", &prevPos)
			return true
		case "volume":
			volume := value.(float64)
			a.here(func() { a.pl.SetVolume(volume) })
			state.volLocal = time.Now()
			if state.observer && state.receiver {
				target := state.cluster.Active
				if target == "" {
					target = state.client.ID
				}
				a.sendVolume(state, target, volume)
			}
			return true
		case "advance":
			if state.localQueue || state.ref == nil || !state.observer || !state.receiver {
				a.playHere(a.pl.AdvanceLocal, !state.localQueue && state.ref != nil)
				return true
			}
		}
	}
	if !state.observer || !state.receiver {
		if a.pl.IsRemote() || state.ref != nil {
			a.toast = "Connect 正在重连，请稍后"
			return true
		}
		return false
	}
	target := state.cluster.Active
	if target == "" {
		target = state.client.ID
	}
	extra := map[string]any{}
	switch action {
	case "play":
		extra = connectPlayBody(value.(player.QueueRequest))
	case "toggle":
		if a.pl.Playing() {
			action = "pause"
		} else {
			action = "resume"
		}
	case "seek":
		if state.ref != nil && state.machine.States[state.ref.Index].NoSeek {
			a.toast = "当前内容不支持拖动进度"
			return true
		}
		action = "seek_to"
		extra["value"] = max(0, value.(time.Duration).Milliseconds())
	case "advance":
		if state.ref == nil {
			return false
		}
		if state.localQueue {
			a.playHere(a.pl.AdvanceLocal, false)
			return true
		}
		next := state.machine.States[state.ref.Index].Transitions.Advance
		if next == nil {
			a.finalizePlayback("")
			state.ref = nil
			a.pl.StopConnect()
			return true
		}
		zero := int64(0)
		a.applyConnectWith(next, &zero, true)
		return true
	case "shuffle", "repeat":
		if !a.pl.IsRemote() && state.ref == nil && state.cluster.Active == "" {
			return false
		}
		// Shown at once, as Spotify is asked.
		answered := func(took bool) {}
		if action == "shuffle" {
			v := value.(bool)
			ask := state.shuffle.set(v)
			answered = func(took bool) { state.shuffle.answered(ask, took) }
			action, extra["value"] = "set_shuffling_context", v
		} else {
			mode := value.(player.RepeatMode)
			ask := state.repeat.set(mode)
			answered = func(took bool) { state.repeat.answered(ask, took) }
			action = "set_options"
			extra["repeating_context"] = mode == player.RepeatAll
			extra["repeating_track"] = mode == player.RepeatOne
		}
		a.connectOptions(spotify.ConnectOptions{})
		sent := a.connectJob(func() error {
			err := connectCommand(state.client, target, action, extra)
			a.post(func() {
				if a.connect == state {
					answered(err == nil)
					a.connectOptions(spotify.ConnectOptions{})
				}
			})
			return err
		})
		if !sent {
			answered(false)
			a.connectOptions(spotify.ConnectOptions{})
		}
		return true
	case "volume":
		volume := value.(float64)
		a.sendVolume(state, target, volume)
		return true
	case "jump":
		// A track of the queue Spotify gave is skipped to where it is, as
		// Spotify's apps do, rather than played as a queue of its own.
		queue, i := a.pl.Queue(), value.(int)
		if state.ref == nil && !a.pl.IsRemote() || queue[i].UID == "" {
			return false
		}
		track := map[string]string{"uri": queue[i].URI(), "uid": queue[i].UID}
		request := player.QueueRequest{Tracks: append([]spotify.Track(nil), queue...), Start: i, ContextURI: state.contextURI}
		a.connectJob(func() error {
			err := state.client.Command(target, "skip_next", map[string]any{"track": track})
			if err != nil {
				log.Printf("connect: jump: %v; playing the queue from there instead", err)
				err = state.client.Command(target, "play", connectPlayBody(request))
			}
			return err
		})
		return true
	case "queue":
		// Played through Connect, Spotify holds the queue and says what
		// plays next: a song only put in Vinyl's was dropped at its next
		// word. It is put in Spotify's, as phones queue one.
		if (state.ref == nil || state.localQueue) && !a.pl.IsRemote() {
			return false
		}
		action = "add_to_queue"
		track := value.(spotify.Track)
		extra["track"] = map[string]any{"uri": track.URI(), "metadata": map[string]string{"is_queued": "true"}, "provider": "queue"}
	case "skip_next", "skip_prev":
	case "skip_prev_track":
		action = "skip_prev"
	default:
		return false
	}
	a.connectJob(func() error { return state.client.Command(target, action, extra) })
	return true
}

// connectMessage is err as the listener reads it: a request Spotify turned
// down says only so, what Spotify said of why going to the log.
func connectMessage(err error) string {
	var turned *spotify.ConnectError
	if errors.As(err, &turned) {
		return fmt.Sprintf("Spotify 没有接受这个操作（%d），原因已写进日志", turned.Status)
	}
	return err.Error()
}

func connectPlayBody(request player.QueueRequest) map[string]any {
	const maxPlayTracks = 200
	lo, hi := 0, len(request.Tracks)
	start := min(max(request.Start, 0), max(0, hi-1))
	if hi > maxPlayTracks {
		lo = max(0, start-50)
		hi = min(len(request.Tracks), lo+maxPlayTracks)
		lo = max(0, hi-maxPlayTracks)
	}
	window := request.Tracks[lo:hi]
	startIdx := max(0, start-lo)
	tracks := make([]map[string]string, 0, len(window))
	for _, track := range window {
		tracks = append(tracks, map[string]string{"uri": track.URI()})
	}
	skipTo := map[string]any{"track_index": startIdx}
	if startIdx < len(tracks) && tracks[startIdx]["uri"] != "" {
		skipTo["track_uri"] = tracks[startIdx]["uri"]
	}
	return map[string]any{
		"context": map[string]any{
			"uri":      normalizeConnectContextURI(request.ContextURI),
			"url":      "",
			"metadata": map[string]any{},
			"pages":    []any{map[string]any{"tracks": tracks}},
		},
		"play_origin": map[string]any{
			"feature_identifier": "harmony",
			"feature_version":    spotify.ConnectClientVersion,
		},
		"options": map[string]any{
			"license": "on-demand",
			"skip_to": skipTo,
		},
	}
}

// connectButton is the monitor speaker standing for the device the music
// plays on, which opens the picker of the devices in a panel below it; the
// popover, not the bar, is where the device playing names itself. The
// speaker wears the accent while the music plays somewhere other than here.
func (a *app) connectButton(c *ui.Context) {
	state := a.connect
	if state == nil {
		return
	}
	color := a.pal.muted
	if state.cluster.Active != "" && state.cluster.Active != state.client.ID {
		color = a.pal.accent
	}
	button := iconButton(c.Key("connect"), iconDevice, 16, color, "播放设备")
	if button.Clicked() {
		state.open = !state.open
	}
	ui.PopoverBase(c.Key("connect-menu"), button, &state.open, func(panel ui.Element) {
		pop := popoverColors(a.pal.dark)
		panel.AttachTo(button, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 8, 0).Width(270).Padding(12)
		a.styleGlassPanel(panel, 16, pop.fill, pop.edge, 24, 8, ui.RGBA(0, 0, 0, 0.2))
		panel.Children(func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "选择播放设备").FontSize(15).FontWeight(600)
				if state.err != "" {
					ui.Text(c, state.err).FontSize(12).TextColor(a.pal.muted)
				}
				if !state.receiver || !state.observer {
					ui.Text(c, "正在连接 Spotify…").FontSize(12)
					return
				}
				ids := []string{state.client.ID}
				for id, device := range state.cluster.Devices {
					if id != state.client.ID && !device.Capabilities.Hidden && (device.Capabilities.CanPlay == nil || *device.Capabilities.CanPlay) {
						ids = append(ids, id)
					}
				}
				sort.Strings(ids[1:])
				for _, id := range ids {
					name := state.cluster.Devices[id].Name
					if id == state.client.ID {
						name = "此电脑 · " + appName
					}
					if name == "" {
						name = "Spotify 设备"
					}
					if id == state.cluster.Active {
						name = "✓ " + name
					}
					row := ui.ButtonBase(c.Key(id)).Label(name).Padding(8, 6).Radius(6)
					if row.Hovered() {
						row.Background(a.pal.hover)
					}
					row.Children(func() { ui.Text(c, name).FontSize(13).SingleLine() })
					if row.Clicked() {
						target := id
						if state.local {
							state.local = false
							state.ref = nil
							if a.pl.Playing() {
								a.here(a.pl.TogglePlay)
							}
						}
						a.connectJob(func() error { return state.client.Transfer(target) })
						state.open = false
					}
				}
				if len(ids) == 1 {
					ui.Text(c, "在同账号的手机或网页打开 Spotify").FontSize(12).TextColor(a.pal.muted)
				}
			})
		})
	})
}
