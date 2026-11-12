package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/spotify"
)

func (a *app) localScene(dir string) {
	result := map[string]any{}
	defer func() {
		data, _ := json.MarshalIndent(result, "", "  ")
		os.WriteFile(filepath.Join(dir, "local-result.json"), data, 0o600)
		mygo.RunOnMain(mygo.App.Quit)
	}()
	mygo.RunOnMain(func() {
		a.settings.LocalSources = []string{os.Getenv("NM_LOCAL_MUSIC")}
		a.scanLocalMusic()
		a.route = route{page: pageLocal}
		a.pl.SetVolume(0)
		a.win.Show()
		a.redraw()
	})
	var ready bool
	for range 240 {
		mygo.RunOnMain(func() { ready = a.localLoaded && !a.localLoading })
		if ready {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	var tracks []spotify.Track
	mygo.RunOnMain(func() {
		tracks = append(tracks, a.localTracks...)
		result["tracks"], result["warnings"] = len(tracks), a.localWarnings
		playable := 0
		for _, track := range tracks {
			if track.Playable() {
				playable++
			}
		}
		result["playable"] = playable
	})
	if len(tracks) == 0 {
		result["error"] = "no local tracks imported"
		return
	}
	time.Sleep(time.Second)
	if image, err := a.win.CapturePage(); err == nil {
		os.WriteFile(filepath.Join(dir, "local-library.png"), image, 0o600)
	}
	var checks []map[string]any
	for _, extension := range []string{".mp3", ".m4a"} {
		var sample *spotify.Track
		for index := range tracks {
			if tracks[index].Playable() && filepath.Ext(tracks[index].LocalPath) == extension {
				sample = &tracks[index]
				break
			}
		}
		if sample == nil {
			continue
		}
		check := map[string]any{"format": extension, "title": sample.Name, "artist": sample.ArtistNames(), "tagDurationMS": sample.Duration, "cover": sample.Album.PicURL != ""}
		mygo.RunOnMain(func() { a.playQueue([]spotify.Track{*sample}, 0) })
		var position, length time.Duration
		for range 100 {
			time.Sleep(150 * time.Millisecond)
			mygo.RunOnMain(func() { position, length = a.pl.Position(), a.pl.Duration() })
			if position > 500*time.Millisecond {
				break
			}
		}
		check["started"], check["durationMS"] = position > 500*time.Millisecond, length.Milliseconds()
		mygo.RunOnMain(func() { a.pl.Seek(20 * time.Second) })
		time.Sleep(500 * time.Millisecond)
		mygo.RunOnMain(func() { position = a.pl.Position(); a.pl.TogglePlay() })
		check["seekMS"] = position.Milliseconds()
		time.Sleep(400 * time.Millisecond)
		mygo.RunOnMain(func() { position = a.pl.Position() })
		time.Sleep(400 * time.Millisecond)
		mygo.RunOnMain(func() {
			check["paused"] = !a.pl.Playing() && a.pl.Position()-position < 100*time.Millisecond
			a.pl.TogglePlay()
		})
		time.Sleep(800 * time.Millisecond)
		mygo.RunOnMain(func() {
			check["resumed"] = a.pl.Playing() && a.pl.Position() > position+300*time.Millisecond
			check["error"] = a.pl.Err()
			lines, _ := a.pl.Lyrics()
			check["lyricsLines"] = len(lines)
			a.redraw()
		})
		if image, err := a.win.CapturePage(); err == nil {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("playing%s.png", extension)), image, 0o600)
		}
		checks = append(checks, check)
	}
	result["playback"] = checks
	var queue []spotify.Track
	for _, track := range tracks {
		if track.Playable() {
			queue = append(queue, track)
			if len(queue) == 2 {
				break
			}
		}
	}
	if len(queue) == 2 {
		mygo.RunOnMain(func() { a.playQueue(queue, 0); a.pl.Next() })
		time.Sleep(800 * time.Millisecond)
		mygo.RunOnMain(func() {
			result["next"] = a.pl.Current() != nil && a.pl.Current().ID == queue[1].ID && a.pl.Position() > 0
			a.pl.Prev()
		})
		time.Sleep(800 * time.Millisecond)
		mygo.RunOnMain(func() {
			result["previous"] = a.pl.Current() != nil && a.pl.Current().ID == queue[0].ID && a.pl.Position() > 0
		})
	}
}
