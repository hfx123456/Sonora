package app

import (
	"slices"
	"sync"

	"vinyl/internal/player"
)

// latestConnectJob replaces work that has not started yet. One request may
// be in flight and one waiting; rapid input never builds a history to replay.
type latestConnectJob struct {
	mu     sync.Mutex
	next   func() error
	queued bool
}

func queueLatestConnect(job *latestConnectJob, enqueue func(func() error) bool, next func() error) bool {
	job.mu.Lock()
	defer job.mu.Unlock()
	job.next = next
	if job.queued {
		return true
	}
	job.queued = true
	if enqueue(func() error {
		job.mu.Lock()
		next := job.next
		job.next, job.queued = nil, false
		job.mu.Unlock()
		return next()
	}) {
		return true
	}
	job.next, job.queued = nil, false
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
		if state.ref == nil && state.pendingPlay == "" {
			state.playVersion.Add(1)
			state.pendingPlay = a.pl.Current().URI()
			a.sendLocalConnectPlay()
		} else {
			a.reportConnect()
		}
		return true
	case "play":
		request := value.(player.QueueRequest)
		if len(request.Tracks) == 0 {
			return false
		}
		a.playHere(func() { a.pl.PlayQueue(request.Tracks, request.Start) })
	case "jump":
		a.playHere(func() { a.pl.Jump(value.(int)) })
	case "skip_next":
		a.playHere(a.pl.Next)
	case "skip_prev":
		a.playHere(a.pl.Prev)
	default:
		return false
	}
	return true
}

func (a *app) playHere(play func()) {
	state := a.connect
	// Reports of the previous song must not be queued during the change.
	state.ref = nil
	state.pendingPlay = ""
	state.localQueue = true
	state.playVersion.Add(1)
	a.here(play)
	if track := a.pl.Current(); track != nil {
		state.pendingPlay = track.URI()
		a.sendLocalConnectPlay()
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
	request := player.QueueRequest{Tracks: slices.Clone(a.pl.Queue()), Start: order[at]}
	body := connectPlayBody(request)
	body["options"].(map[string]any)["seek_to"] = a.pl.Position().Milliseconds()
	body["options"].(map[string]any)["initially_paused"] = !a.pl.Playing()
	body["options"].(map[string]any)["player_options_override"] = map[string]bool{
		"shuffling_context": a.pl.Shuffle(),
		"repeating_context": a.pl.Repeat() == player.RepeatAll,
		"repeating_track":   a.pl.Repeat() == player.RepeatOne,
	}
	uid, uri := a.pl.Current().UID, a.pl.Current().URI()
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
			a.post(func() {
				if a.connect == state && version == state.playVersion.Load() {
					state.playSent = 0
				}
			})
		}
		return err
	}) {
		state.playSent = version
	}
}
