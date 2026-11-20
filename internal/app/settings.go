package app

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// settings are the listener's choices, kept in settings.json in the user
// data directory.
type settings struct {
	FusionPanel           bool              `json:"fusionPanel,omitempty"`
	FusionMode            string            `json:"fusionMode,omitempty"`
	FusionOffset          int64             `json:"fusionOffset,omitempty"`
	FusionPositions       map[string][2]int `json:"fusionPositions,omitempty"`
	FusionHideTranslation bool              `json:"fusionHideTranslation,omitempty"`
	FusionFloatingIsland  bool              `json:"fusionFloatingIsland,omitempty"`
	FusionLocked          bool              `json:"fusionLocked,omitempty"`
	FusionScale           int               `json:"fusionScale,omitempty"`
	FusionFont            string            `json:"fusionFont,omitempty"`
	FusionChineseFont     string            `json:"fusionChineseFont,omitempty"`
	FusionIslandWidth     int               `json:"fusionIslandWidth,omitempty"`
	FusionStatic          bool              `json:"fusionStatic,omitempty"`
	FusionNoAvoid         bool              `json:"fusionNoAvoid,omitempty"`
	LocalSources          []string          `json:"localSources,omitempty"`
	// Notch shows the quick player around the notch: the listener turns
	// it on.
	Notch bool `json:"notch"`
	// NotchActivity shows the cover and the music's levels beside the
	// notch while music plays, which hides the menu bar there.
	NotchActivity bool `json:"notchActivity"`
	// NotchVirtual keeps a notch of the app's own showing at the top of a
	// display without one; else it shows only while the pointer asks.
	NotchVirtual bool `json:"notchVirtual"`
	// NotchGreeted says the notch showed itself, once, as the first song
	// played (greet).
	NotchGreeted bool `json:"notchGreeted"`
	// Appearance is light, dark, or system to follow the system.
	Appearance string `json:"appearance"`
	// Glass enables iOS-style Liquid Glass surfaces across bars and floating controls.
	Glass bool `json:"glass"`
	// GlassStrength controls the intensity of Liquid Glass, from 0 (clear) to 1 (frosted).
	GlassStrength float64 `json:"glassStrength"`
	// MyPlaylistGrid shows the listener's own and saved playlists in the
	// sidebar as a two-column grid of covers rather than a list.
	MyPlaylistGrid bool `json:"myPlaylistGrid"`
	// Smoke makes the progress bars comets trailing smoke as music plays,
	// for a few percent of a core: the listener turns it on.
	Smoke bool `json:"smoke"`
	// LyricsStage shows lyrics on the stage, Folia's style, in Now
	// Playing, rather than scrolling.
	LyricsStage bool `json:"lyricsStage"`
	// Volume is where the volume was left, from 0 to 1.
	Volume float64 `json:"volume"`
	// ShuffleMode (0: off, 1: shuffle, 2: smart shuffle) and RepeatMode
	// (0: off, 1: repeat all, 2: repeat one) persist across restarts.
	ShuffleMode int `json:"shuffleMode,omitempty"`
	RepeatMode  int `json:"repeatMode,omitempty"`
	// Fog fills Now Playing's backdrop with fog of the cover's color,
	// rising and swelling with the bass: an experiment the listener turns
	// on.
	Fog bool `json:"fog"`
	// Flow fills Now Playing's backdrop with an Apple Music-style fluid
	// backdrop from the cover's blurred artwork and palette, drifting and
	// breathing with the music.
	Flow bool `json:"flow"`
	// MVFill has music videos fill Now Playing, rather than show where
	// the cover goes; and MVLyrics shows the lines sung over them then.
	MVFill   bool `json:"mvFill"`
	MVLyrics bool `json:"mvLyrics"`
	// MVLeads is how many milliseconds the listener has the music videos
	// of songs, by their IDs, run ahead of them: the lyrics follow.
	MVLeads map[string]int64 `json:"mvLeads,omitempty"`
	// MVQueue shows the queue beside music videos filling Now Playing.
	MVQueue bool `json:"mvQueue"`
	// QuitOnClose quits the app as its window closes, rather than hiding
	// it as music plays on.
	QuitOnClose bool `json:"quitOnClose"`

	// CacheMB is how many megabytes the parts of tracks kept on disk may
	// take; 0 keeps none.
	CacheMB int `json:"cacheMB"`

	// AutoUpdate looks for a new version on opening and twice a day.
	AutoUpdate bool `json:"autoUpdate"`

	// Proxy is the proxy the listener set, as spotify.ParseProxy reads
	// it; empty goes by the system's.
	Proxy string `json:"proxy,omitempty"`
	// Hotkeys are the global shortcuts the listener set, accelerators by
	// what they do (hotkeyActions).
	Hotkeys map[string]string `json:"hotkeys,omitempty"`

	path string // where they were loaded from, and are saved to
}

func defaultSettings() settings {
	return settings{
		NotchActivity: true,
		Appearance:    "system",
		Glass:         true,
		GlassStrength: 0.7,
		Volume:        0.8,
		CacheMB:       1024,
		MVLyrics:      true,
		MVQueue:       true,
		AutoUpdate:    true,
	}
}

func settingsPath() (string, error) {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// loadSettings reads the settings, or returns the defaults.
func loadSettings() settings {
	s := defaultSettings()
	if path, err := settingsPath(); err == nil {
		if b, err := os.ReadFile(path); err == nil {
			json.Unmarshal(b, &s)
		}
		s.path = path
	}
	s.GlassStrength = min(max(s.GlassStrength, 0), 1)
	return s
}

// save keeps the settings where they were loaded from; defaults that were
// never loaded, as in tests, are not saved.
func (s settings) save() error {
	path := s.path
	if path == "" {
		return nil
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

var setThemeSource = func(s mygo.ThemeSource) {
	mygo.Theme.SetSource(s)
}

// applyAppearance makes the windows light, dark, or follow the system.
func (s settings) applyAppearance() {
	switch s.Appearance {
	case "light":
		setThemeSource(mygo.ThemeLight)
	case "dark":
		setThemeSource(mygo.ThemeDark)
	default:
		setThemeSource(mygo.ThemeSystem)
	}
}
