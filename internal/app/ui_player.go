package app

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

// scrubState is a progress bar's own: the value dragged, while it is, and
// the levels of the music its smoke follows.
type scrubState struct {
	v float64
	// held is set while the pointer pressed on the bar is down, wherever
	// it has gone since: Pressed is false off the bar, which is thin.
	held   bool
	active bool           // dragged since the last frame, to seek once let go
	track  *spotify.Track // the track on which the drag began
	level  barState
}

func secs(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// scrubber shows how far the track is, and seeks where it is dragged or
// clicked: the bar thickens under the pointer, and its knob shows. With a
// smoke color, the bar is a comet while music plays: what has played is a
// streak brightening to the playhead, which glows and trails smoke that
// rises and fades, thicker as the music is louder.
func (a *app) scrubber(c *ui.Context, st *scrubState, track, fill ui.Color, height float32, smoke ui.Color, surface ui.Material) ui.Element {
	pl := a.pl
	dur := pl.Duration().Seconds()
	if !st.active && !st.held {
		st.v = pl.Position().Seconds()
	}
	s := ui.SliderBase(c, &st.v, 0, max(dur, 1)).Height(height).FocusRing(false).Label("播放进度").Cursor(ui.CursorPointer)
	// The press and its release come to the bar wherever the pointer is,
	// and go on to the slider, which follows the pointer.
	s.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Button != 0 {
			return false
		}
		switch ev.Kind {
		case ui.InputPointerDown:
			st.held, st.track = true, pl.Current()
		case ui.InputPointerUp:
			st.held = false
			if ev.Canceled {
				st.active, st.track = false, nil
			}
		}
		return false
	})
	pressed := st.held
	switch {
	case pressed:
		st.active = true
	case st.active:
		// Let go: there, but not right at the end, which would end the
		// song as it begins to play. A new track has its own playhead.
		st.active = false
		if st.track != nil && st.track == pl.Current() {
			pl.Seek(secs(min(st.v, max(0, dur-1))))
		}
		st.track = nil
	case s.Changed():
		pl.Seek(secs(min(st.v, max(0, dur-1))))
	}
	scrubbing := pressed && st.track == pl.Current()
	hot := s.Hovered() || scrubbing
	thick := float32(4)
	if hot {
		thick = 6
	}
	thick = s.AnimateTo("thick", thick, ui.Snappy)
	knob := float32(0)
	if hot {
		knob = 1
	}
	knob = s.AnimateTo("knob", knob, ui.Snappy)
	// The smoke settles away as the music pauses, and while dragging.
	smoking := float32(0)
	if smoke.A > 0 && a.settings.Smoke && pl.Playing() && !pl.Loading() && !scrubbing {
		smoking = 1
	}
	smoking = s.AnimateTo("smoke", smoking, ui.Animation{Duration: 600 * time.Millisecond})
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		at := st.v
		if !scrubbing {
			at = pl.Position().Seconds()
		}
		frac := float32(0)
		if dur > 0 {
			frac = float32(min(1, max(0, at/dur)))
		}
		y := r.Y + (r.H-thick)/2
		head, cy := r.X+r.W*frac, r.Y+r.H/2
		rail := ui.Rect{X: r.X, Y: y, W: r.W, H: thick}
		radius := thick / 2
		if surface == nil {
			p.Fill(rail, track, radius)
		} else {
			surface.PaintMaterial(p, rail, [4]float32{radius, radius, radius, radius})
			p.Stroke(rail, white.Alpha(.20), radius, 1)
		}
		played := ui.Rect{X: r.X, Y: y, W: max(thick, head-r.X), H: thick}
		switch {
		case smoking > 0.01:
			// A streak of light, brightest at the head.
			p.FillGradient(played, ui.LinearGradient{Angle: 90, Oklab: true,
				From: fill.Mix(smoke, smoking).Alpha(0.3), To: fill.Mix(smoke, 0.8*smoking)}, thick/2)
			lv, live := pl.Levels()
			st.level.step(p.Now(), lv, live)
			bass := st.level.group(0, 6)
			drawSmoke(p, head, cy, p.Now(), bass, smoke, smoking)
			// The glow of the head, swelling with the bass, and its core.
			glow := 9 + 8*bass
			p.Shadow(ui.Rect{X: head, Y: cy}, 0, 0, 0, glow, 2.5+2.5*bass, smoke.Alpha(0.95*smoking))
			core := thick * (1 + 0.25*bass)
			p.Fill(ui.Rect{X: head - core/2, Y: cy - core/2, W: core, H: core}, white.Mix(smoke, 0.15).Alpha(smoking), core/2)
		case surface != nil:
			p.FillGradient(played, ui.LinearGradient{Angle: 180, From: white.Alpha(.88), To: fill.Alpha(.66)}, radius)
		default:
			p.Fill(played, fill, radius)
		}
		if knob > 0.01 {
			k := 12 * knob
			p.Shadow(ui.Rect{X: head - k/2, Y: r.Y + (r.H-k)/2, W: k, H: k}, k/2, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.3))
			p.Fill(ui.Rect{X: head - k/2, Y: r.Y + (r.H-k)/2, W: k, H: k}, fill, k/2)
		}
		switch {
		case smoking > 0.01 && (pl.Playing() || smoking < 1):
			// The smoke drifts slowly, under a point a frame at 30 frames
			// a second, which show it smooth for a quarter of what a 120 Hz
			// display would draw.
			p.After(time.Second / 30)
		case pl.Playing() && !scrubbing && dur > 0:
			// Again when the fill has moved a pixel of the screen, which
			// takes a good part of a second, rather than every frame.
			perPixel := dur / float64(r.W*p.Scale())
			p.After(max(16*time.Millisecond, secs(perPixel)))
		}
	})
	return s
}

// Smoke from the playhead: wisps shed one after another, each drifting
// back along the bar and rising, growing and fading over its life. Where a
// wisp is follows from the time alone, so nothing is kept between frames.
const (
	smokeLife  = 1.4 // seconds
	smokeWisps = 36  // alive at once, at most
)

// drawSmoke draws the smoke of a playhead at (x, y) at now, thicker as
// loud (0 to 1) is higher, at opacity shown.
func drawSmoke(p *ui.Painter, x, y float32, now time.Time, loud float32, color ui.Color, shown float32) {
	t := float64(now.UnixMilli()) / 1000
	interval := smokeLife / smokeWisps
	first := math.Floor(t / interval)
	for j := range smokeWisps {
		k := first - float64(j)
		age := float32(t - k*interval)
		if age < 0 || age > smokeLife {
			continue
		}
		// A wisp of its own: how fast it drifts and rises, how it curls,
		// how large it grows. Quiet music sheds fewer of them.
		r := func(o float64) float32 { return lineRandom(k, o) }
		if r(5) > 0.6+0.4*loud {
			continue
		}
		f := age / smokeLife
		wx := x - age*(12+22*r(1))
		wy := y - age*(12+18*r(2)) + float32(math.Sin(float64(age)*(2.5+2*float64(r(3)))+float64(r(4))*2*math.Pi))*6*f
		size := 3 + f*(12+10*r(4))
		alpha := 0.5 * float32(math.Pow(float64(1-f), 1.4)) * shown * (0.75 + 0.5*loud)
		p.Shadow(ui.Rect{X: wx, Y: wy}, 0, 0, 0, size, size*0.45, color.Alpha(alpha))
	}
}

// tickTimes redraws the view as the next second of the track begins, for
// the times shown.
func (a *app) tickTimes(c *ui.Context) {
	if a.pl.Playing() && a.pl.Ready() {
		pos := a.pl.Position()
		c.After(time.Second - pos%time.Second + 10*time.Millisecond)
	}
}

// volumeButton is the speaker, which opens the volume in a popover above
// it, as an AppKit popover: a native slider standing up, the volume in
// percent over it. The wheel over the speaker turns it up and down
// without opening it.
func (a *app) volumeButton(c *ui.Context, color ui.Color) {
	b := iconButton(c.Key("volume-button"), volumeIcon(a.volume), 16, color, "音量")
	if b.Clicked() {
		a.volumeOpen = !a.volumeOpen
	}
	b.HandleInput(func(ev ui.InputEvent) bool { return a.scrollVolume(ev) })
	pop := popoverColors(a.pal.dark)
	ui.PopoverBase(c.Key("volume-popover"), b, &a.volumeOpen, func(panel ui.Element) {
		const arrow = 7
		panel.AttachTo(b, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, arrow+3, 0).Width(52).Padding(11, 0, 12, 0).
			AlignItems(ui.Center).Gap(8).Label("音量")
		a.styleGlassPanel(panel, 16, pop.fill, pop.edge, 24, 8, ui.RGBA(0, 0, 0, 0.2))
		if !a.glassEnabled() {
			panel.Draw(func(p *ui.Painter, r ui.Rect) {
				// The arrow pointing at the speaker, its sides edged as the
				// panel is, over the panel's border.
				cx, foot := r.X+r.W/2, r.Y+r.H
				tip := (&ui.Path{}).MoveTo(cx-arrow-1, foot-0.75).LineTo(cx-1.2, foot+arrow-1.2).
					QuadTo(cx, foot+arrow, cx+1.2, foot+arrow-1.2).LineTo(cx+arrow+1, foot-0.75).Close()
				p.FillPath(tip, pop.fill)
				edge := (&ui.Path{}).MoveTo(cx-arrow-1, foot).LineTo(cx-1.2, foot+arrow-1.2).
					QuadTo(cx, foot+arrow, cx+1.2, foot+arrow-1.2).LineTo(cx+arrow+1, foot)
				p.StrokePath(edge, 0.5, pop.edge)
			})
		}
		ui.Textf(c, "%d%%", int(a.volume*100+0.5)).FontSize(11).FontWeight(500).FontFeatures("tnum").TextColor(a.pal.muted)
		a.nativeVolumeSlider(c, pop)
	})
}

// popover holds the colors of a popover as AppKit draws them.
type popover struct {
	fill, edge, track, knob ui.Color
}

func popoverColors(dark bool) popover {
	if dark {
		return popover{fill: ui.Hex("#2b2b2e"), edge: ui.RGBA(255, 255, 255, 0.14), track: ui.RGBA(255, 255, 255, 0.16), knob: ui.Hex("#ececec")}
	}
	return popover{fill: ui.Hex("#f6f6f6"), edge: ui.RGBA(0, 0, 0, 0.12), track: ui.RGBA(0, 0, 0, 0.12), knob: ui.Hex("#ffffff")}
}

// nativeVolumeSlider is a vertical slider as AppKit draws one: a thin
// track, filled with the accent from the foot to a round knob.
func (a *app) nativeVolumeSlider(c *ui.Context, pop popover) {
	const knob, track = 20, 4
	s := ui.SliderBase(c.Key("volume"), &a.volume, 0, 1).Vertical().Size(knob+8, 124).PaddingY(knob / 2).
		Step(0.01).AutoFocus().FocusRing(false).Label("音量")
	if s.Changed() {
		a.pl.SetVolume(a.volume)
	}
	s.HandleInput(func(ev ui.InputEvent) bool { return a.scrollVolume(ev) })
	pressed := s.Pressed()
	v := float32(a.volume)
	accent := a.pal.accent
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		top, foot := r.Y+knob/2, r.Y+r.H-knob/2
		x := r.X + (r.W-track)/2
		y := foot - (foot-top)*v
		p.Fill(ui.Rect{X: x, Y: top - track/2, W: track, H: foot - top + track}, pop.track, track/2)
		p.Fill(ui.Rect{X: x, Y: y, W: track, H: foot - y + track/2}, accent, track/2)
		k := ui.Rect{X: r.X + (r.W-knob)/2, Y: y - knob/2, W: knob, H: knob}
		p.Shadow(k, knob/2, 0, 0.5, 1.5, 0, ui.RGBA(0, 0, 0, 0.3))
		p.Shadow(k, knob/2, 0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.12))
		fill := pop.knob
		if pressed {
			fill = fill.Mix(ui.RGB(0, 0, 0), 0.06)
		}
		p.Fill(k, fill, knob/2)
		p.Stroke(k, ui.RGBA(0, 0, 0, 0.08), knob/2, 0.5)
	})
}

// scrollVolume turns the volume up as the wheel or the fingers scroll up.
func (a *app) scrollVolume(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputScroll {
		return false
	}
	a.volume = min(1, max(0, a.volume-float64(ev.DY)/400))
	a.pl.SetVolume(a.volume)
	return true
}

// volumeIcon is the speaker for volume v: silent, or with one or two waves.
func volumeIcon(v float64) *ui.SVG {
	switch {
	case v == 0:
		return iconMute
	case v < 0.4:
		return iconVolumeLow
	default:
		return iconVolume
	}
}

// muteButton is the speaker, which mutes and unmutes.
func (a *app) muteButton(c *ui.Context, color ui.Color) {
	icon := iconVolume
	if a.volume == 0 {
		icon = iconMute
	}
	if iconButton(c.Key("mute"), icon, 16, color, "静音").Clicked() {
		if a.volume > 0 {
			a.mutedFrom, a.volume = a.volume, 0
		} else {
			a.volume = max(a.mutedFrom, 0.5)
		}
		a.pl.SetVolume(a.volume)
	}
}

// volumeSlider is a slim slider with the speaker before it, which mutes.
func (a *app) volumeSlider(c *ui.Context, color, track ui.Color, width float32) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		a.muteButton(c, color)
		s := ui.SliderBase(c.Key("volume"), &a.volume, 0, 1).Width(width).Height(16).FocusRing(false).Label("音量")
		if s.Changed() {
			a.pl.SetVolume(a.volume)
		}
		hot := s.Hovered() || s.Pressed()
		thick := float32(4)
		if hot {
			thick = 5
		}
		thick = s.AnimateTo("thick", thick, ui.Snappy)
		v := float32(a.volume)
		s.Draw(func(p *ui.Painter, r ui.Rect) {
			y := r.Y + (r.H-thick)/2
			p.Fill(ui.Rect{X: r.X, Y: y, W: r.W, H: thick}, track, thick/2)
			p.Fill(ui.Rect{X: r.X, Y: y, W: max(thick, r.W*v), H: thick}, color, thick/2)
		})
	})
}

func (a *app) applySavedPlaybackModes() {
	if a.pl == nil {
		return
	}
	if a.pl.SmartFeed == nil {
		a.pl.SmartFeed = a.smartShuffleFeed()
	}
	if a.settings.ShuffleMode >= int(player.ShuffleOff) && a.settings.ShuffleMode <= int(player.ShuffleSmart) {
		a.pl.SetShuffleMode(player.ShuffleMode(a.settings.ShuffleMode))
	}
	if a.settings.RepeatMode >= int(player.RepeatOff) && a.settings.RepeatMode <= int(player.RepeatOne) {
		a.pl.SetRepeatMode(player.RepeatMode(a.settings.RepeatMode))
	}
}

func (a *app) setShuffleMode(mode player.ShuffleMode) {
	a.pl.SetShuffleMode(mode)
	a.settings.ShuffleMode = int(a.pl.ShuffleMode())
	a.settings.PlaybackModesSaved = true
	a.settings.save()
	a.keepSession()
}

func (a *app) enableShuffle() {
	if !a.pl.Shuffle() {
		a.setShuffleMode(player.ShuffleOn)
	}
}

func (a *app) cycleShuffle() {
	if a.pl.SmartFeed == nil {
		a.pl.SmartFeed = a.smartShuffleFeed()
	}
	mode := a.pl.CycleShuffle()
	a.settings.ShuffleMode = int(mode)
	a.settings.PlaybackModesSaved = true
	a.settings.save()
	a.keepSession()
}

func (a *app) cycleRepeat() {
	a.pl.CycleRepeat()
	a.settings.RepeatMode = int(a.pl.Repeat())
	a.settings.PlaybackModesSaved = true
	a.settings.save()
	a.keepSession()
}

// transport is the row of controls: shuffle, previous, play, next, repeat.
func (a *app) transport(c *ui.Context, color, dim, on ui.Color, size float32) {
	pl := a.pl
	ui.Row(c).Gap(size * 0.5).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		shuffle, sicon, stip := dim, iconShuffle, "随机播放"
		switch pl.ShuffleMode() {
		case player.ShuffleOn:
			shuffle, stip = on, "随机播放（再点开启智能随机推荐）"
		case player.ShuffleSmart:
			shuffle, sicon, stip = on, iconSmartShuffle, "智能随机播放（已开启相似新歌推荐）"
		}
		if iconButton(c.Key("shuffle"), sicon, size*0.62, shuffle, "随机播放").Tooltip(stip).Clicked() {
			a.cycleShuffle()
		}
		if iconButton(c.Key("prev"), iconPrev, size*0.85, color, "上一首").Clicked() {
			pl.Prev()
		}
		if playButton(c.Key("play"), pl.Playing(), size*1.25, color).Clicked() {
			pl.TogglePlay()
		}
		if iconButton(c.Key("next"), iconNext, size*0.85, color, "下一首").Clicked() {
			pl.Next()
		}
		repeat, ricon := dim, iconRepeat
		switch pl.Repeat() {
		case player.RepeatAll:
			repeat = on
		case player.RepeatOne:
			repeat, ricon = on, iconRepeat1
		}
		if iconButton(c.Key("repeat"), ricon, size*0.62, repeat, "循环").Clicked() {
			a.cycleRepeat()
		}
	})
}

// playerBar runs along the foot of the content: what plays, its controls
// and its progress, and the volume.
func (a *app) playerBar(c *ui.Context) {
	p := a.pal
	pl := a.pl
	tr := pl.Current()
	a.tickTimes(c)
	const controls = 4*32 + 3*4
	var bar ui.Element
	if a.glassEnabled() {
		material := a.liquidGlass(false, ui.Color{})
		// A light neutral tint leaves the refracted backdrop visible. The
		// material's tone range carries contrast over bright album artwork.
		material.Tint = p.bg.Alpha(0.28)
		bar = ui.Row(c).Absolute().Left(11).Right(10).Bottom(6).Height(playerBarHeight - 12).PaddingX(10).Gap(20).AlignItems(ui.Center).
			Radius(24).
			Material(material)
		if !p.dark {
			bar.Shadow(0, 3, 14, -3, ui.RGBA(0, 0, 0, 0.08))
		}
	} else {
		edge := ui.Box(c.Key("bottom-edge")).Absolute().Left(0).Right(0).Bottom(0).Height(playerBarHeight).
			Material(glass.ScrollEdge{Hard: true, Bottom: true, Background: p.bg})
		edge.Opacity(edgeShown(edge, a.underBottom))
		bar = ui.Row(c).Absolute().Left(0).Right(0).Bottom(0).Height(playerBarHeight).PaddingX(20).Gap(20).AlignItems(ui.Center)
	}
	bar.Children(func() {
		// What plays.
		ui.Row(c).Grow(1).Basis(0).MinWidth(150).Children(func() {
			if tr == nil {
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(48, 48).Radius(8).Background(p.hover).Center().Children(func() {
						ui.Icon(c, iconNote).Size(18, 18).TextColor(p.faint)
					})
					ui.Text(c, "未在播放").FontSize(13).TextColor(p.muted)
				})
				return
			}
			info := ui.ButtonBase(c.Key("info")).Gap(12).AlignItems(ui.Center).FocusRing(false).Shrink(1).MinWidth(0).Label("打开正在播放")
			hovered := info.Hovered()
			var coverBox ui.Element
			info.Children(func() {
				coverBox = ui.Box(c.Key("bar-cover")).Size(48, 48).Shrink(0)
				coverBox.Children(func() {
					a.cover(c, tr.Album.PicURL, 120, 48, 8).Shadow(0, 2, 8, 0, p.cardShade)
					if hovered {
						ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(8).Center().
							Background(ui.RGBA(0, 0, 0, 0.35)).TextColor(ui.RGB(255, 255, 255)).Children(func() {
							ui.Icon(c, iconDown).Size(18, 18).Rotate(180)
						})
					}
				})
				ui.Column(c).Shrink(1).MinWidth(0).Gap(3).Children(func() {
					ui.Text(c, tr.Name).FontSize(13.5).FontWeight(600).SingleLine()
					switch {
					case pl.Loading():
						ui.Text(c, "正在缓冲…").FontSize(12).TextColor(p.muted).SingleLine()
					case pl.Err() != "":
						ui.Text(c, pl.Err()).FontSize(12).TextColor(p.accent).SingleLine()
					default:
						a.artistLinks(c.Key("by"), tr.Artists, 12, p.muted, p.text)
					}
				})
			})
			if info.Clicked() {
				a.nowPlaying, a.npPane = true, a.lyricsPaneChosen()
			}
			a.barCoverHoverCard(c, coverBox, tr)
			a.likeButton(c.Key("like"), tr, 16, p.muted).Margin(0, 0, 0, 4).Shrink(0)
		})

		// Controls over the progress.
		ui.Column(c).Width(440).Shrink(1).MinWidth(240).Gap(0).AlignItems(ui.Stretch).Children(func() {
			a.transport(c, p.text, p.muted, p.accent, 18)
			ui.Row(c).Gap(10).AlignItems(ui.Center).Margin(-4, 0, 0, 0).Children(func() {
				ui.Text(c, clock(pl.Position())).FontSize(11).FontFeatures("tnum").TextColor(p.muted).Width(34).TextAlign(ui.End)
				a.scrubber(c.Key("bar-scrub"), &a.barScrub, p.press, p.text.Alpha(0.75), 14, p.accent, nil).Grow(1)
				ui.Text(c, clock(pl.Duration())).FontSize(11).FontFeatures("tnum").TextColor(p.muted).Width(34)
			})
		})

		// Queue, devices, volume and floating lyrics.
		ui.Row(c).Grow(1).Basis(0).MinWidth(controls).Justify(ui.End).AlignItems(ui.Center).Gap(4).Children(func() {
			if tr != nil && pl.HasVideo() {
				a.barMVButton(c)
			}
			if iconButton(c.Key("queue"), iconQueue, 16, p.muted, "播放队列").Clicked() && tr != nil {
				a.nowPlaying, a.npPane = true, paneQueue
			}
			a.connectButton(c)
			a.volumeButton(c, p.text.Alpha(0.75))
			a.fusionButton(c)
		})
	})
}

// trackCreditsFetch loads a track's credits; tests replace it.
var trackCreditsFetch = spotify.TrackCredits

func (a *app) ensureBarCredits(tr *spotify.Track) {
	if tr == nil || tr.ID == "" || tr.Local() || tr.Cloud() || tr.Episode {
		return
	}
	if a.barCredits == nil {
		a.barCredits = map[string][]spotify.Credit{}
		a.barCreditsLoading = map[string]bool{}
	}
	if _, ok := a.barCredits[tr.ID]; ok || a.barCreditsLoading[tr.ID] {
		return
	}
	// Reuse already-loaded TrackPage credits if present.
	if pg := a.tracks[tr.ID]; pg != nil && len(pg.Credits) > 0 {
		a.barCredits[tr.ID] = pg.Credits
		return
	}
	id := tr.ID
	a.barCreditsLoading[id] = true
	go func() {
		credits, err := trackCreditsFetch(id)
		a.post(func() {
			delete(a.barCreditsLoading, id)
			if err == nil {
				a.barCredits[id] = credits
			} else {
				a.barCredits[id] = nil
			}
		})
	}()
}

type creditRow struct {
	role  string
	names string
}

var creditRoleMap = map[string]string{
	"main artist":        "演唱",
	"主要艺人":               "演唱",
	"featured artist":    "特邀",
	"composer":           "作曲",
	"作曲者":                "作曲",
	"lyricist":           "作词",
	"作词者":                "作词",
	"writer":             "词曲",
	"songwriter":         "词曲",
	"arranger":           "编曲",
	"编曲者":                "编曲",
	"producer":           "制作人",
	"executive producer": "监制",
	"mixing engineer":    "混音",
	"mastering engineer": "母带",
	"recording engineer": "录音",
	"vocals":             "人声",
}

func formatCreditRole(role string) string {
	r := strings.TrimSpace(role)
	if mapped, ok := creditRoleMap[strings.ToLower(r)]; ok {
		return mapped
	}
	return r
}

func groupCredits(credits []spotify.Credit) []creditRow {
	type bucket struct {
		role  string
		names []string
		seen  map[string]bool
	}
	var order []string
	byRole := map[string]*bucket{}
	for _, cr := range credits {
		name := strings.TrimSpace(cr.Name)
		if name == "" {
			continue
		}
		for _, rawRole := range cr.Roles {
			role := formatCreditRole(rawRole)
			if role == "" {
				continue
			}
			b, ok := byRole[role]
			if !ok {
				b = &bucket{role: role, seen: map[string]bool{}}
				byRole[role] = b
				order = append(order, role)
			}
			if !b.seen[name] {
				b.seen[name] = true
				b.names = append(b.names, name)
			}
		}
	}
	// Put songwriting & production roles first in a natural Chinese music order.
	priority := map[string]int{
		"作词": 1, "作曲": 2, "词曲": 3, "编曲": 4, "制作人": 5, "监制": 6, "演唱": 7,
	}
	var rows []creditRow
	added := map[string]bool{}
	for _, pRole := range []string{"作词", "作曲", "词曲", "编曲", "制作人", "监制", "演唱"} {
		if b, ok := byRole[pRole]; ok {
			rows = append(rows, creditRow{role: b.role, names: strings.Join(b.names, "、")})
			added[pRole] = true
		}
	}
	for _, role := range order {
		if added[role] || priority[role] > 0 {
			continue
		}
		b := byRole[role]
		rows = append(rows, creditRow{role: b.role, names: strings.Join(b.names, "、")})
		if len(rows) >= 6 {
			break
		}
	}
	return rows
}

// lyricsCreditRows extracts "作词：xxx" / "作曲：xxx" etc. from the opening lines
// of the current lyrics as a fallback for local/cloud tracks or songs without
// Spotify credit metadata.
func (a *app) lyricsCreditRows() []creditRow {
	lines, _ := a.pl.Lyrics()
	if len(lines) == 0 {
		return nil
	}
	limit := min(len(lines), 10)
	prefixes := []struct {
		keys  []string
		label string
	}{
		{[]string{"作词", "词"}, "作词"},
		{[]string{"作曲", "曲"}, "作曲"},
		{[]string{"词曲"}, "词曲"},
		{[]string{"编曲"}, "编曲"},
		{[]string{"制作人", "制作"}, "制作人"},
	}
	var rows []creditRow
	seen := map[string]bool{}
	for _, ln := range lines[:limit] {
		text := strings.TrimSpace(ln.Text)
		if text == "" {
			continue
		}
		idx := strings.IndexAny(text, ":：")
		if idx <= 0 || idx > 12 {
			continue
		}
		key := strings.TrimSpace(text[:idx])
		val := strings.TrimSpace(text[idx+len(":"):])
		if strings.HasPrefix(text[idx:], "：") {
			val = strings.TrimSpace(text[idx+len("："):])
		}
		if val == "" {
			continue
		}
		for _, p := range prefixes {
			matched := false
			for _, k := range p.keys {
				if key == k {
					matched = true
					break
				}
			}
			if matched && !seen[p.label] {
				seen[p.label] = true
				rows = append(rows, creditRow{role: p.label, names: val})
				break
			}
		}
	}
	return rows
}

// barCoverHoverCard floats a compact credits & track info card above the
// player bar's album artwork while the pointer hovers over it.
func (a *app) barCoverHoverCard(c *ui.Context, coverBox ui.Element, tr *spotify.Track) {
	if tr == nil {
		return
	}
	hovered := coverBox.Hovered() && !a.nowPlaying
	if hovered {
		a.ensureBarCredits(tr)
	}
	target := float32(0)
	if hovered {
		target = 1
	}
	show := coverBox.AnimateTo("info-card", target, ui.Snappy)
	if show <= 0.01 {
		return
	}
	p := a.pal
	pop := popoverColors(p.dark)
	rows := groupCredits(a.barCredits[tr.ID])
	if len(rows) == 0 {
		rows = a.lyricsCreditRows()
	}
	loading := a.barCreditsLoading[tr.ID]
	ui.Overlay(c, func() {
		card := ui.Column(c.Key("bar-cover-info")).
			AttachTo(coverBox, ui.AnchorTopLeft, ui.AnchorBottomLeft).
			Margin(0, 0, 10, 0).
			Padding(11, 14, 12, 14).
			Gap(7).
			Width(292).
			Opacity(show).
			PassThrough()
		a.styleGlassPanel(card, 14, pop.fill, pop.edge, 22, 6, ui.RGBA(0, 0, 0, 0.18))
		card.Children(func() {
			ui.Text(c, "制作人员").FontSize(11.5).FontWeight(600).TextColor(p.accent)
			ui.Box(c).Height(0.5).Background(pop.edge)
			if len(rows) > 0 {
				for i, row := range rows {
					ui.Row(c.Key(i)).Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, row.role).FontSize(12).TextColor(p.faint).Width(60).Shrink(0).SingleLine()
						ui.Text(c, row.names).FontSize(12).FontWeight(500).TextColor(p.text).Grow(1).Shrink(1).MinWidth(0)
					})
				}
			} else if loading {
				ui.Text(c, "正在获取制作人员…").FontSize(12).TextColor(p.muted)
			} else {
				if artists := tr.ArtistNames(); artists != "" {
					ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, "演唱").FontSize(12).TextColor(p.faint).Width(60).Shrink(0).SingleLine()
						ui.Text(c, artists).FontSize(12).FontWeight(500).TextColor(p.text).Grow(1).Shrink(1).MinWidth(0)
					})
				}
				if tr.Album.Name != "" {
					ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
						ui.Text(c, "专辑").FontSize(12).TextColor(p.faint).Width(60).Shrink(0).SingleLine()
						ui.Text(c, tr.Album.Name).FontSize(12).TextColor(p.muted).Grow(1).Shrink(1).MinWidth(0)
					})
				}
				ui.Text(c, "暂无更多词曲信息").FontSize(11.5).TextColor(p.faint)
			}
			if tr.Album.Name != "" && len(rows) > 0 {
				ui.Box(c).Height(0.5).Margin(2, 0, 0, 0).Background(pop.edge)
				ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
					label := "专辑"
					if tr.Episode {
						label = "节目"
					}
					ui.Text(c, label).FontSize(11.5).TextColor(p.faint).Width(60).Shrink(0).SingleLine()
					ui.Text(c, tr.Album.Name).FontSize(11.5).TextColor(p.muted).Grow(1).Shrink(1).MinWidth(0).SingleLine()
				})
			}
		})
	})
}
