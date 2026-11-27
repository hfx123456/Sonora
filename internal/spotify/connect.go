package spotify

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const ConnectClientVersion = "harmony:4.62.0"

func ConnectPlatformIdentifier() string {
	switch runtime.GOOS {
	case "windows":
		return "web_player windows 10;chrome 141.0.0.0;desktop"
	case "darwin":
		return "web_player osx 14.0;chrome 141.0.0.0;desktop"
	default:
		return "web_player linux;chrome 141.0.0.0;desktop"
	}
}

type ConnectPlaybackStats struct {
	MSTotalEst         int64  `json:"ms_total_est"`
	MSMetadataDuration int64  `json:"ms_metadata_duration"`
	StartOffsetMS      int64  `json:"start_offset_ms"`
	StartBitrate       int    `json:"start_bitrate,omitempty"`
	AudioStartBitrate  int    `json:"audio_start_bitrate,omitempty"`
	KeySystem          string `json:"key_system,omitempty"`
	LocalTimeMS        int64  `json:"local_time_ms"`
}

type ConnectStateReport struct {
	StateRef         any
	Position         int64
	Duration         int64
	Paused           bool
	PreviousPosition *int64
	Bitrate          int
	Format           int
	DebugSource      string
	PlaybackStats    *ConnectPlaybackStats
}

type ConnectEvent struct {
	Kind string
	Data json.RawMessage
	Err  error
}

type Connect struct {
	ID        string
	Name      string
	ctx       context.Context
	cancel    context.CancelFunc
	emit      func(ConnectEvent)
	mu        sync.Mutex
	wg        sync.WaitGroup
	seq       int64
	lastState *ConnectStateReport
	lastID    Identity
	host      string
}

func NewConnect(id, name string, emit func(ConnectEvent)) *Connect {
	ctx, cancel := context.WithCancel(context.Background())
	return &Connect{ID: id, Name: name, ctx: ctx, cancel: cancel, emit: emit, host: "spclient.wg.spotify.com"}
}

func (c *Connect) Start() {
	c.wg.Add(2)
	go func() { defer c.wg.Done(); c.loop(false) }()
	go func() { defer c.wg.Done(); c.loop(true) }()
}

func (c *Connect) Close() { c.cancel(); c.wg.Wait() }

func (c *Connect) notify(kind string, data json.RawMessage, err error) {
	if c.ctx.Err() == nil && c.emit != nil {
		c.emit(ConnectEvent{kind, data, err})
	}
}

func (c *Connect) request(ctx context.Context, method, path, connection string, body any) (json.RawMessage, error) {
	return c.requestOnce(ctx, method, path, connection, body, true)
}

func (c *Connect) requestOnce(ctx context.Context, method, path, connection string, body any, renew bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	id, err := CurrentIdentity()
	if err != nil {
		return nil, errors.New("Connect: 请重新登录")
	}
	return c.requestWithIdentity(ctx, id, method, path, connection, body, renew)
}

func (c *Connect) requestWithIdentity(ctx context.Context, id Identity, method, path, connection string, body any, renew bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var reqBody io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(raw)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, "https://"+c.host+path, reqBody)
	if err != nil {
		return nil, errors.New("Connect: 请求地址无效")
	}
	req.Header.Set("Authorization", "Bearer "+id.Token)
	req.Header.Set("client-token", id.ClientToken)
	if id.Token != "" && !id.Anonymous {
		c.mu.Lock()
		c.lastID = id
		c.mu.Unlock()
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Origin", "https://open.spotify.com")
	req.Header.Set("Referer", "https://open.spotify.com/")
	req.Header.Set("app-platform", "WebPlayer")
	if connection != "" {
		req.Header.Set("X-Spotify-Connection-Id", connection)
	}
	res, err := Client.Do(req)
	if err != nil {
		return nil, errors.New("Connect: 网络请求失败")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode == http.StatusUnauthorized && renew {
		next, err := RenewedIdentity()
		if err != nil {
			return nil, errors.New("Connect: 身份刷新失败，请重新登录")
		}
		return c.requestWithIdentity(ctx, next, method, path, connection, body, false)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		turned := &ConnectError{Path: path, Status: res.StatusCode}
		var why struct {
			Type    string `json:"error_type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(data, &why) == nil {
			turned.Type, turned.Message = why.Type, why.Message
		}
		return nil, turned
	}
	return data, nil
}

func (c *Connect) loop(receiver bool) {
	delay := time.Second
	for c.ctx.Err() == nil {
		started := time.Now()
		err := c.session(receiver)
		if time.Since(started) > 30*time.Second {
			delay = time.Second
		}
		kind := "observer_offline"
		if receiver {
			kind = "receiver_offline"
		}
		c.notify(kind, nil, err)
		timer := time.NewTimer(delay)
		select {
		case <-c.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 30*time.Second)
	}
}

func (c *Connect) session(receiver bool) error {
	id, err := CurrentIdentity()
	if err != nil {
		return errors.New("Connect: 登录身份不可用")
	}
	ctx, cancel := context.WithCancel(c.ctx)
	defer cancel()
	address := "wss://dealer.spotify.com/?access_token=" + url.QueryEscape(id.Token)
	conn, res, err := websocket.Dial(ctx, address, &websocket.DialOptions{HTTPClient: Client, HTTPHeader: http.Header{"User-Agent": {UserAgent}}})
	if err != nil {
		if res != nil && res.StatusCode == http.StatusUnauthorized {
			_, _ = RenewedIdentity()
		}
		return errors.New("Connect: Dealer 连接失败")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(4 << 20)
	go func() {
		timer := time.NewTicker(25 * time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, []byte(`{"type":"ping"}`))
				stop()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	registered := false
	connection := ""
	defer func() {
		if !registered {
			return
		}
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		// Use the latest in-memory identity without triggering an interactive
		// renewal (which can deadlock against the UI thread on quit or race with SignOut).
		cleanupID := CachedIdentity()
		if cleanupID.Token == "" {
			c.mu.Lock()
			cleanupID = c.lastID
			c.mu.Unlock()
		}
		if cleanupID.Token == "" {
			cleanupID = id
		}
		if receiver {
			deregBody := c.deregisterBody()
			_, _ = c.requestWithIdentity(cleanup, cleanupID, http.MethodDelete, "/track-playback/v1/devices/"+c.ID, "", deregBody, false)
		} else {
			_, _ = c.requestWithIdentity(cleanup, cleanupID, http.MethodDelete, "/connect-state/v1/devices/"+c.ID+"-observer", connection, nil, false)
		}
	}()
	for {
		readCtx, stop := context.WithTimeout(ctx, 60*time.Second)
		_, raw, err := conn.Read(readCtx)
		stop()
		if err != nil {
			return errors.New("Connect: Dealer 已断开")
		}
		var msg struct {
			Type     string            `json:"type"`
			URI      string            `json:"uri"`
			Headers  map[string]string `json:"headers"`
			Payloads []json.RawMessage `json:"payloads"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if msg.Type != "message" {
			continue
		}
		nextConnection := ""
		for key, value := range msg.Headers {
			if strings.EqualFold(key, "Spotify-Connection-Id") {
				nextConnection = value
			}
		}
		if strings.HasPrefix(msg.URI, "hm://pusher/v1/connections/") && nextConnection != "" {
			connection = nextConnection
			if receiver {
				err = c.registerReceiver(ctx, connection)
			} else {
				body := map[string]any{"member_type": "CONNECT_STATE", "device": map[string]any{"device_info": map[string]any{"capabilities": map[string]any{"can_be_player": false, "hidden": true, "needs_full_player_state": true}}}}
				var data json.RawMessage
				data, err = c.request(ctx, http.MethodPut, "/connect-state/v1/devices/"+c.ID+"-observer", connection, body)
				if err == nil {
					c.notify("cluster", data, nil)
				}
			}
			if err != nil {
				return err
			}
			registered = true
			kind := "observer_ready"
			if receiver {
				kind = "receiver_ready"
			}
			c.notify(kind, nil, nil)
			continue
		}
		for _, payload := range msg.Payloads {
			data, err := decodeConnectPayload(payload)
			if err != nil {
				continue
			}
			switch msg.URI {
			case "hm://connect-state/v1/cluster":
				if !receiver {
					c.notify("cluster", data, nil)
				}
			case "hm://track-playback/v1/command":
				if receiver {
					c.notify("command", data, nil)
				}
			}
		}
	}
}

func decodeConnectPayload(raw json.RawMessage) (json.RawMessage, error) {
	var encoded string
	if json.Unmarshal(raw, &encoded) != nil {
		return raw, nil
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) > 2 && data[0] == 0x1f && data[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		data, err = io.ReadAll(io.LimitReader(reader, (4<<20)+1))
		if err != nil {
			return nil, err
		}
	}
	if len(data) > 4<<20 || !json.Valid(data) {
		return nil, errors.New("invalid Connect payload")
	}
	return data, nil
}

func (c *Connect) registerReceiver(ctx context.Context, connection string) error {
	body := map[string]any{
		"device": map[string]any{"brand": "spotify", "device_id": c.ID, "device_type": "computer", "model": "web_player", "name": c.Name, "metadata": map[string]any{}, "is_group": false, "platform_identifier": ConnectPlatformIdentifier(),
			"capabilities": map[string]any{"change_volume": true, "enable_play_token": true, "supports_file_media_type": true, "play_token_lost_behavior": "pause", "disable_connect": false, "audio_podcasts": true, "video_playback": false, "manifest_formats": []string{"file_ids_mp4", "file_ids_mp4_dual"}}},
		"outro_endcontent_snooping": false, "connection_id": connection, "client_version": ConnectClientVersion, "volume": 52428,
	}
	raw, err := c.request(ctx, http.MethodPost, "/track-playback/v1/devices", "", body)
	if err != nil {
		return err
	}
	var result struct {
		Seq int64 `json:"initial_seq_num"`
	}
	if err = json.Unmarshal(raw, &result); err != nil {
		return err
	}
	c.mu.Lock()
	c.seq = result.Seq
	c.lastState = nil
	c.mu.Unlock()
	return nil
}

func (c *Connect) Transfer(target string) error {
	_, err := c.request(c.ctx, http.MethodPost, "/connect-state/v1/connect/transfer/from/"+c.ID+"-observer/to/"+url.PathEscape(target), "", map[string]any{"transfer_options": map[string]string{"restore_paused": "restore"}})
	return err
}

func (c *Connect) Command(target, endpoint string, extra map[string]any) error {
	command := map[string]any{"endpoint": endpoint}
	for key, value := range extra {
		command[key] = value
	}
	_, err := c.request(c.ctx, http.MethodPost, "/connect-state/v1/player/command/from/"+c.ID+"-observer/to/"+url.PathEscape(target), "", map[string]any{"command": command})
	var turned *ConnectError
	if errors.As(err, &turned) {
		turned.Endpoint = endpoint
	}
	return err
}

// ConnectError is Spotify turning a Connect request down: the command, if it
// was one, and what Spotify said of why.
type ConnectError struct {
	Endpoint, Path string
	Status         int
	Type, Message  string // the error_type and message of its answer
}

func (e *ConnectError) Error() string {
	s := fmt.Sprintf("Connect: %s HTTP %d", e.Path, e.Status)
	if e.Endpoint != "" {
		s = fmt.Sprintf("Connect: %s %s HTTP %d", e.Endpoint, e.Path, e.Status)
	}
	if why := strings.TrimSpace(e.Type + " " + e.Message); why != "" {
		s += ": " + why
	}
	return s
}

func (c *Connect) deregisterBody() map[string]any {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	last := c.lastState
	c.lastState = nil
	c.mu.Unlock()
	body := map[string]any{
		"seq_num":      seq,
		"debug_source": "deregister",
	}
	if last != nil {
		body["state_ref"] = last.StateRef
		body["sub_state"] = connectSubState(*last)
		if last.PreviousPosition != nil {
			body["previous_position"] = *last.PreviousPosition
		}
	}
	return body
}

func connectSubState(report ConnectStateReport) map[string]any {
	speed := 1
	if report.Paused {
		speed = 0
	}
	sub := map[string]any{
		"position":       report.Position,
		"duration":       report.Duration,
		"playback_speed": speed,
		"media_type":     "AUDIO",
	}
	if report.Bitrate > 0 {
		sub["bitrate"] = report.Bitrate
	}
	if report.Format > 0 {
		sub["format"] = report.Format
	}
	return sub
}

func (c *Connect) UpdateLastPosition(posMs, durMs int64, paused bool) {
	c.mu.Lock()
	if c.lastState != nil {
		c.lastState.Position = posMs
		if durMs > 0 {
			c.lastState.Duration = durMs
		}
		c.lastState.Paused = paused
		c.lastState.PreviousPosition = nil
		if m, ok := c.lastState.StateRef.(map[string]any); ok {
			refCopy := make(map[string]any, len(m))
			for k, v := range m {
				refCopy[k] = v
			}
			refCopy["paused"] = paused
			c.lastState.StateRef = refCopy
		}
	}
	c.mu.Unlock()
}

func (c *Connect) ReportState(report ConnectStateReport) error {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	if report.StateRef != nil && report.DebugSource != "track_data_finalized" {
		copied := report
		if m, ok := report.StateRef.(map[string]any); ok {
			refCopy := make(map[string]any, len(m))
			for k, v := range m {
				refCopy[k] = v
			}
			copied.StateRef = refCopy
		}
		copied.PreviousPosition = nil
		copied.PlaybackStats = nil
		c.lastState = &copied
	} else if report.StateRef == nil {
		c.lastState = nil
	}
	c.mu.Unlock()

	body := map[string]any{
		"seq_num":      seq,
		"state_ref":    report.StateRef,
		"sub_state":    connectSubState(report),
		"debug_source": cmp.Or(report.DebugSource, "progress"),
	}
	if report.PreviousPosition != nil {
		body["previous_position"] = *report.PreviousPosition
	}
	if report.PlaybackStats != nil {
		body["playback_stats"] = report.PlaybackStats
	}
	raw, err := c.request(c.ctx, http.MethodPut, "/track-playback/v1/devices/"+c.ID+"/state", "", body)
	if err == nil && len(raw) > 0 {
		var meta struct {
			EndSong  json.RawMessage   `json:"endsong"`
			EndSongs []json.RawMessage `json:"endsongs"`
		}
		if json.Unmarshal(raw, &meta) == nil {
			n := len(meta.EndSongs)
			if n == 0 && len(meta.EndSong) > 0 && string(meta.EndSong) != "null" {
				n = 1
			}
			if n > 0 {
				log.Printf("connect: spotify emitted %d endsong(s)", n)
			}
		}
		c.notify("report", raw, nil)
	}
	return err
}

func (c *Connect) Report(ref any, position, duration int64, paused bool) error {
	return c.ReportState(ConnectStateReport{
		StateRef:    ref,
		Position:    position,
		Duration:    duration,
		Paused:      paused,
		DebugSource: "progress",
	})
}

func (c *Connect) Reject(machine ConnectMachine, ref *ConnectRef, current any, position, duration int64, paused bool) error {
	if ref == nil || ref.Index < 0 || ref.Index >= len(machine.States) {
		return errors.New("Connect: 无效冲突状态")
	}
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	speed := 1
	if paused {
		speed = 0
	}
	body := map[string]any{"seq_nums": []int64{seq}, "state_ref": current, "sub_state": map[string]any{"position": position, "duration": duration, "playback_speed": speed, "media_type": "AUDIO"}, "rejected_state_refs": []any{map[string]any{"state_machine_id": machine.ID, "state_id": machine.States[ref.Index].ID, "paused": ref.Paused}}, "debug_source": "state_conflict"}
	raw, err := c.request(c.ctx, http.MethodPost, "/track-playback/v1/devices/"+c.ID+"/state_conflict", "", body)
	if err != nil {
		return err
	}
	var response struct {
		Commands []json.RawMessage `json:"commands"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &response) == nil {
		for _, command := range response.Commands {
			c.notify("command", command, nil)
		}
	}
	return nil
}

func (c *Connect) Volume(target string, volume float64) error {
	_, err := c.request(c.ctx, http.MethodPut, "/connect-state/v1/connect/volume/from/"+c.ID+"-observer/to/"+url.PathEscape(target), "", map[string]any{"volume": int(min(max(volume, 0), 1) * 65535)})
	return err
}
