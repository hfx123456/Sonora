package app

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/audio"
	"github.com/egoist/mygo/ui"
	"vinyl/internal/spotify"
)

// Native clicks and Space must pause and resume without any Connect request completing.
func TestConnectLocalPauseAndSpace(t *testing.T) {
	a := testConnectApp(t)
	a.settings = defaultSettings()
	out := audio.NewPlayer(&silence{})
	out.Play()
	a.pl.Attach(out)
	defer out.Close()
	if err := json.Unmarshal([]byte(`{"state_machine_id":"m","states":[{"state_id":"s","track":0}],"tracks":[{"metadata":{"uri":"spotify:track:one"}}]}`), &a.connect.machine); err != nil {
		t.Fatal(err)
	}
	a.connect.ref = &spotify.ConnectRef{}
	a.connect.cluster.Active = a.connect.client.ID
	tt := ui.NewTester(a.view, 1200, 800)
	if err := tt.Click("播放/暂停"); err != nil {
		t.Fatal(err)
	}
	if a.pl.Playing() {
		t.Fatal("click waited for Spotify to pause")
	}
	tt.Key(0, ui.KeySpace)
	if !a.pl.Playing() {
		t.Fatal("Space waited for Spotify to resume")
	}
	tt.Key(0, ui.KeySpace)
	if a.pl.Playing() {
		t.Fatal("second Space did not immediately pause")
	}
	if len(a.connect.jobs) != 0 || len(a.connect.reports) != 1 {
		t.Fatal("local controls sent commands or piled up old reports")
	}
	old := connectReport
	defer func() { connectReport = old }()
	called := false
	connectReport = func(_ *spotify.Connect, report spotify.ConnectStateReport) error {
		called = true
		if !report.Paused {
			t.Error("reported an older play intent instead of the latest pause")
		}
		return nil
	}
	if err := (<-a.connect.reports)(); err != nil || !called {
		t.Fatalf("latest report: called %v, err %v", called, err)
	}
	// Losing the observer connection must not turn the local pause button off.
	a.connect.observer = false
	tt.Key(0, ui.KeySpace)
	if !a.pl.Playing() {
		t.Fatal("local resume was blocked by Connect reconnecting")
	}
}

// Rapid selections start locally and register only the final queue; a failed sync cannot undo it.
func TestConnectLocalSelectionsKeepLatest(t *testing.T) {
	a := testConnectApp(t)
	old := connectCommand
	defer func() { connectCommand = old }()
	var sent []string
	connectCommand = func(_ *spotify.Connect, target, endpoint string, body map[string]any) error {
		if target != "local" || endpoint != "play" {
			t.Errorf("sent %s to %s", endpoint, target)
		}
		tracks := body["context"].(map[string]any)["pages"].([]any)[0].(map[string]any)["tracks"].([]map[string]string)
		start := body["options"].(map[string]any)["skip_to"].(map[string]any)["track_index"].(int)
		sent = append(sent, tracks[start]["uri"])
		return errors.New("offline")
	}
	tracks := []spotify.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	a.pl.PlayQueue(tracks, 0)
	a.pl.PlayQueue(tracks, 1)
	a.pl.PlayQueue(tracks, 2)
	if a.pl.Current().ID != "c" || !a.pl.Playing() || len(a.connect.jobs) != 1 {
		t.Fatal("latest selection waited, or obsolete requests piled up")
	}
	if err := (<-a.connect.jobs)(); err == nil {
		t.Fatal("expected simulated sync failure")
	}
	if len(sent) != 1 || sent[0] != "spotify:track:c" || a.pl.Current().ID != "c" || !a.pl.Playing() {
		t.Fatalf("sync replayed %v or changed local playback", sent)
	}
	// A second batch must still be scheduled after the first was taken.
	a.pl.PlayQueue(tracks, 0)
	if len(a.connect.jobs) != 1 || a.pl.Current().ID != "a" {
		t.Fatal("later selection could not be scheduled")
	}
}

// Late selection replies cannot replace a newer song or rewind/resume one paused while it loaded.
func TestConnectLocalSelectionAdoptsWithoutRestart(t *testing.T) {
	a := testConnectApp(t)
	tracks := []spotify.Track{{ID: "a"}, {ID: "b"}}
	a.pl.PlayQueue(tracks, 0)
	a.pl.PlayQueue(tracks, 1)
	a.pl.Seek(42 * time.Second)
	a.pl.TogglePlay()
	reply := func(id string) {
		t.Helper()
		data := `{"type":"replace_state","seek_to":0,"state_ref":{"state_index":0,"paused":false},"state_machine":{"state_machine_id":"m-` + id + `","states":[{"state_id":"s-` + id + `","track":0}],"tracks":[{"metadata":{"uri":"spotify:track:` + id + `"}}]}}`
		a.connectEvent(spotify.ConnectEvent{Kind: "command", Data: json.RawMessage(data)})
	}
	reply("a")
	a.connectEvent(spotify.ConnectEvent{Kind: "command", Data: json.RawMessage(`{"type":"replace_state","state_ref":null}`)})
	if a.pl.Current().ID != "b" || a.pl.Playing() || a.pl.Position() != 42*time.Second {
		t.Fatal("obsolete reply changed the newer local selection")
	}
	reply("b")
	if a.connect.ref == nil || !a.connect.ref.Paused || a.connect.pendingPlay != "" {
		t.Fatal("matching reply did not adopt the local paused state")
	}
	if a.pl.Current().ID != "b" || a.pl.Playing() || a.pl.Position() != 42*time.Second {
		t.Fatal("matching reply restarted, rewound or resumed the audio")
	}
	if len(a.pl.Queue()) != len(tracks) {
		t.Fatal("the server's short state machine truncated the local queue")
	}
}

// A request to another device still controls that device instead of starting local audio.
func TestConnectNewQueueOnRemoteStaysRemote(t *testing.T) {
	a := testConnectApp(t)
	a.connect.cluster.Active = "phone"
	a.pl.MirrorConnect(spotify.Track{ID: "one"}, time.Second, true)
	a.pl.PlayQueue([]spotify.Track{{ID: "two"}}, 0)
	if !a.pl.IsRemote() || a.pl.Current().ID != "one" || len(a.connect.jobs) != 1 {
		t.Fatal("remote queue selection started local audio")
	}
}

// A blocked status report must not block a new play command or local selection.
func TestConnectSlowReportDoesNotBlockPlay(t *testing.T) {
	a := testConnectApp(t)
	oldReport, oldCommand := connectReport, connectCommand
	defer func() { connectReport, connectCommand = oldReport, oldCommand }()
	started, release, sent := make(chan struct{}), make(chan struct{}), make(chan struct{})
	connectReport = func(*spotify.Connect, spotify.ConnectStateReport) error {
		close(started)
		<-release
		return nil
	}
	connectCommand = func(*spotify.Connect, string, string, map[string]any) error {
		close(sent)
		return nil
	}
	a.pl.Hold(time.Second)
	json.Unmarshal([]byte(`{"state_machine_id":"m","states":[{"state_id":"s","track":0}],"tracks":[{"metadata":{"uri":"spotify:track:one"}}]}`), &a.connect.machine)
	a.connect.ref = &spotify.ConnectRef{}
	a.reportConnect()
	stopped := make(chan struct{}, 2)
	for _, queue := range []chan func() error{a.connect.jobs, a.connect.reports} {
		go func() {
			a.runConnectJobs(a.connect, queue)
			stopped <- struct{}{}
		}()
	}
	defer func() {
		close(release)
		close(a.connect.done)
		<-stopped
		<-stopped
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("report worker did not start")
	}
	a.pl.PlayQueue([]spotify.Track{{ID: "two"}}, 0)
	if a.pl.Current().ID != "two" || !a.pl.Playing() {
		t.Fatal("new selection waited for the blocked report")
	}
	select {
	case <-sent:
	case <-time.After(3 * time.Second):
		t.Fatal("play command waited behind a blocked report")
	}
}

// An older request already in flight cannot prevent the latest selection from being sent next.
func TestConnectLocalSelectionDuringRequest(t *testing.T) {
	a := testConnectApp(t)
	old := connectCommand
	defer func() { connectCommand = old }()
	started, release := make(chan struct{}), make(chan struct{})
	var sent []string
	connectCommand = func(_ *spotify.Connect, _, _ string, body map[string]any) error {
		tracks := body["context"].(map[string]any)["pages"].([]any)[0].(map[string]any)["tracks"].([]map[string]string)
		start := body["options"].(map[string]any)["skip_to"].(map[string]any)["track_index"].(int)
		sent = append(sent, tracks[start]["uri"])
		if len(sent) == 1 {
			close(started)
			<-release
			return errors.New("older request failed")
		}
		return nil
	}
	tracks := []spotify.Track{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	a.pl.PlayQueue(tracks, 0)
	first := <-a.connect.jobs
	finished := make(chan error, 1)
	go func() { finished <- first() }()
	<-started
	a.pl.PlayQueue(tracks, 1)
	a.pl.PlayQueue(tracks, 2)
	close(release)
	if err := <-finished; err != nil {
		t.Fatal("obsolete failure was still reported:", err)
	}
	if len(a.connect.jobs) != 1 || a.pl.Current().ID != "c" {
		t.Fatal("an in-flight request blocked or queued obsolete selections")
	}
	if err := (<-a.connect.jobs)(); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 2 || sent[0] != "spotify:track:a" || sent[1] != "spotify:track:c" {
		t.Fatalf("sent %v", sent)
	}
}

// Repeated readiness events cannot register the same selection again and rewind it on a duplicate reply.
func TestConnectLocalSelectionRegistersOnce(t *testing.T) {
	a := testConnectApp(t)
	old := connectCommand
	defer func() { connectCommand = old }()
	connectCommand = func(*spotify.Connect, string, string, map[string]any) error { return nil }
	a.pl.PlayQueue([]spotify.Track{{ID: "a"}}, 0)
	if err := (<-a.connect.jobs)(); err != nil {
		t.Fatal(err)
	}
	a.connectEvent(spotify.ConnectEvent{Kind: "observer_ready"})
	a.connectEvent(spotify.ConnectEvent{Kind: "receiver_ready"})
	if len(a.connect.jobs) != 0 {
		t.Fatal("readiness replayed a selection already sent")
	}
}

// Transferring to a real remote device invalidates a local registration still waiting to be sent.
func TestConnectPendingLocalSelectionYieldsToRemote(t *testing.T) {
	a := testConnectApp(t)
	old := connectCommand
	defer func() { connectCommand = old }()
	connectCommand = func(*spotify.Connect, string, string, map[string]any) error {
		t.Error("obsolete local registration stole playback back")
		return nil
	}
	a.pl.PlayQueue([]spotify.Track{{ID: "a"}}, 0)
	a.connectEvent(spotify.ConnectEvent{Kind: "cluster", Data: json.RawMessage(`{"active_device_id":"phone","player_state":{"is_playing":true,"track":{"uri":"spotify:track:remote"}}}`)})
	if err := (<-a.connect.jobs)(); err != nil {
		t.Fatal(err)
	}
	if !a.pl.IsRemote() || a.pl.Current().ID != "remote" {
		t.Fatal("a pending local selection prevented the transfer")
	}
}
