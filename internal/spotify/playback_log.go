package spotify

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// melodyBatchURL is Spotify's Melody batch ingestion endpoint; tests swap it.
var melodyBatchURL = spclientURL + "/melody/v1/msg/batch"

// TrackStreamVerification matches the Spotify Web Player
// track_stream_verification telemetry message sent through Melody.
type TrackStreamVerification struct {
	PlayTrack       string `json:"play_track"`
	PlaybackID      string `json:"playback_id"`
	MSPlayed        int64  `json:"ms_played"`
	MSNominalPlayed int64  `json:"ms_nominal_played"`
	SessionID       string `json:"session_id"`
	SequenceID      int64  `json:"sequence_id"`
	NextPlaybackID  string `json:"next_playback_id,omitempty"`
	PlaybackService string `json:"playback_service"`
}

// SendTrackStreamVerification reports a finalized playback segment to
// Spotify's Melody endpoint for signed-in listeners.
func SendTrackStreamVerification(ctx context.Context, ev TrackStreamVerification) error {
	return SendTrackStreamVerificationWithIdentity(ctx, ev, Identity{})
}

// SendTrackStreamVerificationWithIdentity sends a TrackStreamVerification using
// preset on the first attempt when available (so sign-out cleanup still carries
// the departing listener's token before SignOut clears it).
func SendTrackStreamVerificationWithIdentity(ctx context.Context, ev TrackStreamVerification, preset Identity) error {
	if ev.PlayTrack == "" || ev.MSPlayed <= 0 {
		return nil
	}
	ev.PlaybackService = cmp.Or(ev.PlaybackService, "track-playback")
	payload, err := json.Marshal(struct {
		SDKID         string `json:"sdk_id"`
		Platform      string `json:"platform"`
		ClientVersion string `json:"client_version"`
		Messages      []struct {
			Type    string                  `json:"type"`
			Message TrackStreamVerification `json:"message"`
		} `json:"messages"`
	}{
		SDKID:         ConnectClientVersion,
		Platform:      ConnectPlatformIdentifier(),
		ClientVersion: ConnectClientVersion,
		Messages: []struct {
			Type    string                  `json:"type"`
			Message TrackStreamVerification `json:"message"`
		}{{Type: "track_stream_verification", Message: ev}},
	})
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		var id Identity
		if attempt == 0 && preset.Token != "" && !preset.Anonymous {
			id = preset
		} else if attempt == 0 {
			id, err = CurrentIdentity()
		} else {
			id, err = RenewedIdentity()
		}
		if err != nil {
			if err == ErrSignedOut {
				return nil
			}
			return fmt.Errorf("melody: identity: %w", err)
		}
		if id.Anonymous || id.Token == "" {
			return nil
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, melodyBatchURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+id.Token)
		if id.ClientToken != "" {
			req.Header.Set("client-token", id.ClientToken)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("app-platform", "WebPlayer")
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Origin", "https://open.spotify.com")
		req.Header.Set("Referer", "https://open.spotify.com/")

		res, err := Client.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 4<<10))
		res.Body.Close()
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusAccepted || res.StatusCode == http.StatusNoContent {
			return readErr
		}
		return fmt.Errorf("melody: HTTP %d: %s", res.StatusCode, snippet(body))
	}
}
