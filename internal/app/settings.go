package app

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"vinyl/internal/localmusic"
)

// cloudSourceSetting persists a cloud music source (WebDAV, OneDrive, or
// Google Drive) in settings.json. Passwords and OAuth tokens are sealed with
// the OS keystore (Windows DPAPI / macOS Keychain).
type cloudSourceSetting struct {
	ID            string `json:"id"`
	Type          string `json:"type,omitempty"` // "", "webdav", "onedrive", "googledrive"
	Name          string `json:"name"`
	URL           string `json:"url,omitempty"`
	Folder        string `json:"folder,omitempty"`
	FolderPending bool   `json:"folderPending,omitempty"`
	Username      string `json:"username,omitempty"`
	ClientID      string `json:"clientId,omitempty"`
	SealedPass    string `json:"sealedPass,omitempty"`
	PlainPass     string `json:"plainPass,omitempty"` // fallback only on OSes without seal()
	SealedSecret  string `json:"sealedSecret,omitempty"`
	PlainSecret   string `json:"plainSecret,omitempty"`
	SealedRefresh string `json:"sealedRefresh,omitempty"`
	PlainRefresh  string `json:"plainRefresh,omitempty"`
}

func sealCloudPassword(pass string) (sealed, plain string) {
	if pass == "" {
		return "", ""
	}
	if b, err := seal([]byte(pass)); err == nil {
		return base64.StdEncoding.EncodeToString(b), ""
	}
	return "", pass
}

func unsealCloudSecret(sealed, plain string) string {
	if sealed != "" {
		if raw, err := base64.StdEncoding.DecodeString(sealed); err == nil {
			if opened, err := unseal(raw); err == nil {
				return string(opened)
			}
		}
	}
	return plain
}

func (c cloudSourceSetting) effectiveType() string {
	switch c.Type {
	case localmusic.CloudTypeOneDrive, localmusic.CloudTypeGoogleDrive, localmusic.CloudTypeBaidu, localmusic.CloudTypeQuark, localmusic.CloudType115:
		return c.Type
	default:
		return localmusic.CloudTypeWebDAV
	}
}

func (c cloudSourceSetting) toCloudSource() localmusic.CloudSource {
	return localmusic.CloudSource{
		ID:           c.ID,
		Type:         c.effectiveType(),
		Name:         c.Name,
		URL:          c.URL,
		Folder:       c.Folder,
		Username:     c.Username,
		Password:     unsealCloudSecret(c.SealedPass, c.PlainPass),
		ClientID:     c.ClientID,
		ClientSecret: unsealCloudSecret(c.SealedSecret, c.PlainSecret),
		RefreshToken: unsealCloudSecret(c.SealedRefresh, c.PlainRefresh),
	}
}

func (s *settings) cloudSources() []localmusic.CloudSource {
	out := make([]localmusic.CloudSource, 0, len(s.CloudSources))
	for _, c := range s.CloudSources {
		if c.FolderPending {
			continue
		}
		out = append(out, c.toCloudSource())
	}
	return out
}

func (s *settings) allCloudSources() []localmusic.CloudSource {
	out := make([]localmusic.CloudSource, 0, len(s.CloudSources))
	for _, c := range s.CloudSources {
		out = append(out, c.toCloudSource())
	}
	return out
}

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
	LocalSources          []string             `json:"localSources,omitempty"`
	CloudSources          []cloudSourceSetting `json:"cloudSources,omitempty"`
	HiddenLocalIDs        []string             `json:"hiddenLocalIds,omitempty"`
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
	// StartupAnimation plays the cold-start floating music notes animation on launch.
	StartupAnimation bool `json:"startupAnimation"`
	// MyPlaylistGrid shows the listener's own and saved playlists in the
	// sidebar as a two-column grid of covers rather than a list.
	MyPlaylistGrid bool `json:"myPlaylistGrid"`
	// PlaylistTrackCovers shows per-row album thumbnails in playlists, Liked
	// Songs, and Local/Cloud music lists.
	PlaylistTrackCovers bool `json:"playlistTrackCovers"`
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
	ShuffleMode        int  `json:"shuffleMode,omitempty"`
	RepeatMode         int  `json:"repeatMode,omitempty"`
	PlaybackModesSaved bool `json:"playbackModesSaved,omitempty"`
	// Fog fills Now Playing's backdrop with fog of the cover's color,
	// rising and swelling with the bass: an experiment the listener turns
	// on.
	Fog bool `json:"fog"`
	// Flow fills Now Playing's backdrop with an Apple Music-style fluid
	// mesh gradient of the cover's palette, drifting and breathing with
	// the music.
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

	// MiniPlayer shows a compact floating desktop playback controller.
	MiniPlayer bool `json:"miniPlayer,omitempty"`
	// MiniStyle picks the mini player layout: "bar" (Apple Music compact bar) or "card" (split cover card).
	MiniStyle string `json:"miniStyle,omitempty"`
	// MiniAlwaysOnTop keeps the mini player floating above other windows.
	MiniAlwaysOnTop bool `json:"miniAlwaysOnTop"`
	// MiniPosition remembers the last dragged screen position of the mini player.
	MiniPosition    [2]int `json:"miniPosition,omitempty"`
	MiniPositionSet bool   `json:"miniPositionSet,omitempty"`

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
		FusionNoAvoid:       true,
		NotchActivity:       true,
		Appearance:          "system",
		Glass:               true,
		GlassStrength:       0.7,
		StartupAnimation:    true,
		PlaylistTrackCovers: true,
		Volume:              0.8,
		CacheMB:             1024,
		MVLyrics:            true,
		MVQueue:             true,
		MiniStyle:           "bar",
		MiniAlwaysOnTop:     true,
		AutoUpdate:          true,
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
	if s.migratePlainCloudSecrets() {
		_ = s.save()
	}
	return s
}

// migratePlainCloudSecrets re-seals any legacy plaintext cloud credentials
// once OS keystore encryption is available.
func (s *settings) migratePlainCloudSecrets() bool {
	migrated := false
	for i := range s.CloudSources {
		cs := &s.CloudSources[i]
		if cs.PlainPass != "" {
			if sealed, plain := sealCloudPassword(cs.PlainPass); sealed != "" && plain == "" {
				cs.SealedPass, cs.PlainPass = sealed, ""
				migrated = true
			}
		}
		if cs.PlainSecret != "" {
			if sealed, plain := sealCloudPassword(cs.PlainSecret); sealed != "" && plain == "" {
				cs.SealedSecret, cs.PlainSecret = sealed, ""
				migrated = true
			}
		}
		if cs.PlainRefresh != "" {
			if sealed, plain := sealCloudPassword(cs.PlainRefresh); sealed != "" && plain == "" {
				cs.SealedRefresh, cs.PlainRefresh = sealed, ""
				migrated = true
			}
		}
	}
	return migrated
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
