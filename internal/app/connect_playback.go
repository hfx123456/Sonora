package app

import (
	"errors"
	"slices"
	"sync"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

// latestConnectJob replaces work that has not started yet. One request may
// be in flight and one waiting; rapid input never builds a history to replay.
type latestConnectJob struct {
	mu       sync.Mutex
	next     func() error
	queued   bool
	priority bool
}

func queueLatestConnect(job *latestConnectJob, enqueue func(func() error) bool, next func() error) bool {
	return queueLatestConnectPriority(job, enqueue, true, next)
}

func queueLatestConnectPriority(job *latestConnectJob, enqueue func(func() error) bool, priority bool, next func() error) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	// Never let a routine periodic progress ping overwrite an unstarted
	// high-priority lifecycle report (before_track_load, milestone, seek, pause/resume).
	if job.queued && job.priority && !priority {
		return false
	}
	job.next = next
	job.priority = priority
	if job.queued {
		return true
	}
	job.queued = true
	if enqueue(func() error {
		job.mu.Lock()
		next := job.next
		job.next, job.queued, job.priority = nil, false, false
		job.mu.Unlock()
		return next()
	}) {
		return true
	}
	job.next, job.queued, job.priority = nil, false, false
	return false
}

// localConnectControl acts on this computer before syncing with Spotify.
// Remote devices still use commands: their audio is not in this player.
func (a *app) localConnectControl(action string, value any) bool {
	state := a.connect
	if a.pl.IsRemote() || state.cluster.Active != "" && state.cluster.Active != state.client.ID {
		return false
	}
	switch action {
	case "toggle":
		if a.pl.Current() == nil {
			return false
		}
		a.here(a.pl.TogglePlay)
		if state.tracker != nil && !state.local {
			cur := a.pl.Current()
			posMs, durMs := a.pl.Position().Milliseconds(), a.pl.Duration().Milliseconds()
			if durMs == 0 && cur != nil {
				durMs = cur.Length().Milliseconds()
			}
			bitrate, format, _ := a.pl.MediaInfo()
			playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
			if !state.tracker.isActive() && cur != nil && !cur.Local() {
				state.tracker.start(cur.URI(), "", "", posMs, durMs, bitrate, format, playing, connectNow())
			} else {
				state.tracker.update(playing, posMs, durMs, bitrate, format, connectNow())
			}
		}
		if state.ref == nil && state.pendingPlay == "" {
			state.playVersion.Add(1)
			state.pendingPlay = a.pl.Current().URI()
			a.sendLocalConnectPlay()
		} else {
			src := "resume"
			if !a.pl.Playing() {
				src = "pause"
			}
			a.reportConnectWith(src, nil)
		}
		return true
	case "play":
		request := value.(player.QueueRequest)
		if len(request.Tracks) == 0 {
			return false
		}
		state.contextURI = request.ContextURI
		a.playHere(func() { a.pl.PlayQueueContext(request.Tracks, request.Start, request.ContextURI) }, false)
	case "jump":
		a.playHere(func() { a.pl.Jump(value.(int)) }, !state.localQueue)
	case "skip_next":
		a.playHere(a.pl.Next, !state.localQueue)
	case "skip_prev":
		a.playHere(a.pl.Prev, !state.localQueue)
	case "skip_prev_track":
		a.playHere(a.pl.PrevTrack, !state.localQueue)
	default:
		return false
	}
	return true
}

func (a *app) playHere(play func(), allowSkipUID bool) {
	state := a.connect
	a.finalizePlayback("")
	// Reports of the previous song must not be queued during the change.
	state.ref = nil
	state.pendingPlay = ""
	if !allowSkipUID {
		state.localQueue = true
	}
	state.playVersion.Add(1)
	a.here(play)
	if a.pl.Awaiting() {
		// Radio or Smart Shuffle hit end-of-queue and is waiting for its async
		// feed; do not re-register the old finished track as pendingPlay.
		return
	}
	if track := a.pl.Current(); track != nil && !track.Local() {
		if a.pl.Playing() || a.pl.Loading() {
			state.pendingPlay = track.URI()
			a.sendLocalConnectPlay()
		}
		if state.tracker != nil {
			bitrate, format, _ := a.pl.MediaInfo()
			playing := a.pl.Playing() && !a.pl.Loading() && a.pl.Ready()
			state.tracker.start(track.URI(), "", "", a.pl.Position().Milliseconds(), track.Length().Milliseconds(), bitrate, format, playing, connectNow())
		}
	}
}

// sendLocalConnectPlay registers the queue already playing here. A late
// replace_state adopts its server identity without restarting the audio.
func (a *app) sendLocalConnectPlay() {
	state := a.connect
	if !state.observer || !state.receiver || state.pendingPlay == "" {
		return
	}
	version := state.playVersion.Load()
	if state.playSent == version {
		return
	}
	order, at := a.pl.PlayOrder()
	if at < 0 || at >= len(order) {
		return
	}
	request := player.QueueRequest{Tracks: slices.Clone(a.pl.Queue()), Start: order[at], ContextURI: state.contextURI}
	body := connectPlayBody(request)
	body["options"].(map[string]any)["seek_to"] = a.pl.Position().Milliseconds()
	body["options"].(map[string]any)["initially_paused"] = !a.pl.Playing()
	body["options"].(map[string]any)["player_options_override"] = map[string]bool{
		"shuffling_context": a.pl.Shuffle(),
		"repeating_context": a.pl.Repeat() == player.RepeatAll,
		"repeating_track":   a.pl.Repeat() == player.RepeatOne,
	}
	uid, uri := a.pl.Current().UID, a.pl.Current().URI()
	// Playlist item UIDs are not Spotify Connect queue UIDs; only use
	// skip_next when advancing inside a Connect-managed queue.
	if state.localQueue {
		uid = ""
	}
	if queueLatestConnect(&state.playJob, a.connectJob, func() error {
		if version != state.playVersion.Load() {
			return nil
		}
		var err error
		if uid != "" {
			// A selection in Spotify's queue keeps its place in that queue.
			err = connectCommand(state.client, state.client.ID, "skip_next", map[string]any{"track": map[string]string{"uri": uri, "uid": uid}})
		}
		if (uid == "" || err != nil) && version == state.playVersion.Load() {
			err = connectCommand(state.client, state.client.ID, "play", body)
		}
		if version != state.playVersion.Load() {
			return nil
		}
		if err != nil {
			var ce *spotify.ConnectError
			terminal := errors.As(err, &ce) && ce.Status >= 400 && ce.Status < 500 && ce.Status != 429
			a.post(func() {
				if a.connect == state && version == state.playVersion.Load() {
					if terminal {
						state.pendingPlay = ""
					} else {
						state.playSent = 0
					}
				}
			})
		}
		return err
	}) {
		state.playSent = version
	}
}
