package app

import (
	"cmp"
	"math"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

const (
	miniStyleBar  = "bar"
	miniStyleCard = "card"

	// Generous transparent margin so rounded elevation shadows decay cleanly
	// to zero well inside the frameless window (preventing rectangular clipping).
	miniShadowPad = 28
	miniBarW      = 408
	miniBarH      = 80
	miniCardW     = 220
	miniCardH     = 220
)

type miniState struct {
	win       *mygo.Window
	style     string
	placing   bool
	saveTimer *time.Timer
	scrub     scrubState
}

// miniPlayerButton renders the quick-toggle Mini Player button in the main
// window's bottom player bar so the user can open/close it in one click.
func (a *app) miniPlayerButton(c *ui.Context) {
	col := a.pal.muted
	tip := "打开迷你播放器"
	if a.settings.MiniPlayer {
		col = a.pal.accent
		tip = "关闭迷你播放器"
	}
	if iconButton(c.Key("mini-player-toggle"), iconMiniPlayer, 16, col, "迷你播放器").Tooltip(tip).Clicked() {
		a.setMiniPlayerEnabled(!a.settings.MiniPlayer)
	}
}

// miniSmokeColor ensures the comet progress bar & smoke stay luminous and
// readable even when the album cover is nearly black or desaturated.
func miniSmokeColor(tint, fallback ui.Color, dark bool) ui.Color {
	if tint == (ui.Color{}) {
		return fallback
	}
	lum := 0.299*float32(tint.R) + 0.587*float32(tint.G) + 0.114*float32(tint.B)
	if lum < 75 {
		return tint.Mix(fallback, 0.72)
	}
	if !dark && lum > 220 {
		return tint.Mix(fallback, 0.55)
	}
	return tint
}

// miniProgressBar draws an interactive, seekable progress bar with Sonora's
// luminous comet head & rising bass-reactive smoke trail.
func (a *app) miniProgressBar(c *ui.Context, trackCol, fillCol, smokeCol ui.Color, height float32) ui.Element {
	pl := a.pl
	st := &a.mini.scrub
	dur := pl.Duration().Seconds()
	if !st.active {
		st.v = pl.Position().Seconds()
	}
	if dur <= 0 {
		st.v = 0
	}
	maxV := max(dur, 1)
	s := ui.SliderBase(c, &st.v, 0, maxV).Height(height).Disabled(dur <= 0).
		Step(1).FocusRing(false).Label("迷你播放进度")
	pressed := s.Pressed()
	switch {
	case pressed && !st.active:
		st.active, st.track = true, pl.Current()
	case !pressed && st.active:
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
	thick := float32(3.5)
	if hot {
		thick = 5.5
	}
	thick = s.AnimateTo("mini-thick", thick, ui.Snappy)
	knob := float32(0)
	if hot {
		knob = 1
	}
	knob = s.AnimateTo("mini-knob", knob, ui.Snappy)

	smoking := float32(0)
	if !c.Preferences().ReduceMotion && pl.Playing() && !pl.Loading() && !scrubbing && dur > 0 {
		smoking = 1
	}
	smoking = s.AnimateTo("mini-smoke", smoking, ui.Animation{Duration: 550 * time.Millisecond})
	white := ui.RGB(255, 255, 255)

	s.Draw(func(p *ui.Painter, r ui.Rect) {
		at := st.v
		if !scrubbing {
			at = pl.SongPosition().Seconds()
		}
		frac := float32(0)
		if dur > 0 {
			frac = float32(min(1, max(0, at/dur)))
		}
		y := r.Y + (r.H-thick)/2
		head, cy := r.X+r.W*frac, r.Y+r.H/2
		rail := ui.Rect{X: r.X, Y: y, W: r.W, H: thick}
		radius := thick / 2
		p.Fill(rail, trackCol, radius)
		if frac > 0 {
			played := ui.Rect{X: r.X, Y: y, W: max(thick, head-r.X), H: thick}
			if smoking > 0.01 {
				p.FillGradient(played, ui.LinearGradient{
					Angle: 90,
					Oklab: true,
					From:  fillCol.Mix(smokeCol, smoking).Alpha(0.35),
					To:    fillCol.Mix(smokeCol, 0.85*smoking),
				}, radius)
				lv, live := pl.Levels()
				st.level.step(p.Now(), lv, live)
				bass := st.level.group(0, 6)
				drawSmoke(p, head, cy, p.Now(), bass, smokeCol, smoking*0.85)
				glow := 8 + 7*bass
				p.Shadow(ui.Rect{X: head, Y: cy}, 0, 0, 0, glow, 2+2*bass, smokeCol.Alpha(0.92*smoking))
				core := thick * (1.1 + 0.25*bass)
				p.Fill(ui.Rect{X: head - core/2, Y: cy - core/2, W: core, H: core}, white.Mix(smokeCol, 0.18).Alpha(smoking), core/2)
			} else {
				p.FillGradient(played, ui.LinearGradient{
					Angle: 90,
					Oklab: true,
					From:  fillCol.Alpha(0.65),
					To:    smokeCol,
				}, radius)
			}
		}
		if knob > 0.01 {
			k := 10 * knob
			p.Shadow(ui.Rect{X: head - k/2, Y: cy - k/2, W: k, H: k}, k/2, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.28))
			p.Fill(ui.Rect{X: head - k/2, Y: cy - k/2, W: k, H: k}, white.Mix(smokeCol, 0.25), k/2)
		}
		switch {
		case smoking > 0.01 && (pl.Playing() || smoking < 1):
			p.After(time.Second / 30)
		case pl.Playing() && !scrubbing && dur > 0:
			perPixel := dur / float64(max(1, r.W*p.Scale()))
			p.After(max(16*time.Millisecond, secs(perPixel)))
		}
	})
	return s
}

func (s settings) effectiveMiniStyle() string {
	if s.MiniStyle == miniStyleCard {
		return miniStyleCard
	}
	return miniStyleBar
}

func miniWindowSize(style string) (w, h int) {
	if style == miniStyleCard {
		return miniCardW + miniShadowPad*2, miniCardH + miniShadowPad*2
	}
	return miniBarW + miniShadowPad*2, miniBarH + miniShadowPad*2
}

// miniBounds computes the target window rectangle on screen, honoring a saved
// position (clamped to the nearest visible display) or defaulting to the
// bottom-right corner of the primary display's work area.
func miniBounds(style string, pos [2]int, posSet bool, displays []mygo.Display, primary mygo.Display) mygo.Rectangle {
	w, h := miniWindowSize(style)
	if !posSet {
		area := primary.WorkArea
		if area.Width <= 0 || area.Height <= 0 {
			area = mygo.Rectangle{X: 0, Y: 0, Width: 1440, Height: 900}
		}
		x := area.X + area.Width - w - 10
		y := area.Y + area.Height - h - 10
		return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
	}

	target := mygo.Rectangle{X: pos[0], Y: pos[1], Width: w, Height: h}
	disp := miniNearestDisplay(target, displays, primary)
	area := disp.WorkArea
	if area.Width <= 0 || area.Height <= 0 {
		return target
	}
	x := min(max(target.X, area.X-miniShadowPad), area.X+area.Width-w+miniShadowPad)
	y := min(max(target.Y, area.Y-miniShadowPad), area.Y+area.Height-h+miniShadowPad)
	return mygo.Rectangle{X: x, Y: y, Width: w, Height: h}
}

func miniNearestDisplay(bounds mygo.Rectangle, displays []mygo.Display, primary mygo.Display) mygo.Display {
	if len(displays) == 0 {
		return primary
	}
	cx, cy := bounds.X+bounds.Width/2, bounds.Y+bounds.Height/2
	best := displays[0]
	bestDist := int64(math.MaxInt64)
	for _, d := range displays {
		area := d.WorkArea
		if area.Width <= 0 || area.Height <= 0 {
			area = d.Bounds
		}
		dx := int64(0)
		if cx < area.X {
			dx = int64(area.X - cx)
		} else if cx > area.X+area.Width {
			dx = int64(cx - (area.X + area.Width))
		}
		dy := int64(0)
		if cy < area.Y {
			dy = int64(area.Y - cy)
		} else if cy > area.Y+area.Height {
			dy = int64(cy - (area.Y + area.Height))
		}
		dist := dx*dx + dy*dy
		if dist < bestDist {
			best, bestDist = d, dist
		}
	}
	return best
}

// syncMiniPlayer creates, updates, repositions, or hides the floating mini
// player window according to the user's current settings.
func (a *app) syncMiniPlayer() {
	if a.win == nil || a.quitting || shotHook != nil {
		return
	}
	if !a.settings.MiniPlayer {
		if a.mini.win != nil {
			a.keepMiniPosition()
			a.mini.win.Hide()
		}
		return
	}

	style := a.settings.effectiveMiniStyle()
	// When switching style while open, anchor around the bottom-right corner
	// of the current window so bar <-> card transitions stay put naturally.
	if a.mini.win != nil && a.mini.style != "" && a.mini.style != style {
		cur := a.mini.win.Bounds()
		newW, newH := miniWindowSize(style)
		a.settings.MiniPosition = [2]int{
			cur.X + cur.Width - newW,
			cur.Y + cur.Height - newH,
		}
		a.settings.MiniPositionSet = true
	}

	bounds := miniBounds(style, a.settings.MiniPosition, a.settings.MiniPositionSet, mygo.Screen.Displays(), mygo.Screen.PrimaryDisplay())
	a.mini.style = style

	if a.mini.win == nil {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:         appName + " 迷你播放器",
			Hidden:        true,
			Frameless:     true,
			Transparent:   true,
			DisableShadow: true,
			DisableResize: true,
			AlwaysOnTop:   a.settings.MiniAlwaysOnTop,
			SkipTaskbar:   true,
			X:             bounds.X,
			Y:             bounds.Y,
			Width:         bounds.Width,
			Height:        bounds.Height,
			Content:       ui.View(a.miniView),
		})
		w.OnClose(func(e *mygo.CloseEvent) {
			if !a.quitting {
				e.PreventDefault()
				a.setMiniPlayerEnabled(false)
			}
		})
		w.OnMove(a.onMiniMove)
		mygo.Screen.OnDisplaysChanged(func() {
			if a.settings.MiniPlayer {
				a.syncMiniPlayer()
			}
		})
		a.mini.win = w
	}

	a.mini.placing = true
	a.mini.win.SetBounds(bounds)
	a.mini.placing = false
	a.mini.win.SetAlwaysOnTop(a.settings.MiniAlwaysOnTop)
	a.mini.win.ShowInactive()
	a.mini.win.Invalidate()
}

func (a *app) setMiniPlayerEnabled(on bool) {
	if a.settings.MiniPlayer == on {
		return
	}
	a.settings.MiniPlayer = on
	a.settings.save()
	a.syncMiniPlayer()
	a.redraw()
}

func (a *app) setMiniStyle(style string) {
	if style != miniStyleCard {
		style = miniStyleBar
	}
	if a.settings.effectiveMiniStyle() == style {
		return
	}
	a.settings.MiniStyle = style
	a.settings.save()
	a.syncMiniPlayer()
	a.redraw()
}

func (a *app) toggleMiniAlwaysOnTop() {
	a.settings.MiniAlwaysOnTop = !a.settings.MiniAlwaysOnTop
	a.settings.save()
	a.syncMiniPlayer()
	a.redraw()
}

func (a *app) onMiniMove() {
	if a.mini.placing || a.mini.win == nil || !a.settings.MiniPlayer {
		return
	}
	b := a.mini.win.Bounds()
	a.settings.MiniPosition = [2]int{b.X, b.Y}
	a.settings.MiniPositionSet = true
	if a.mini.saveTimer != nil {
		a.mini.saveTimer.Stop()
	}
	a.mini.saveTimer = time.AfterFunc(400*time.Millisecond, func() {
		a.post(func() {
			a.mini.saveTimer = nil
			a.settings.save()
		})
	})
}

func (a *app) keepMiniPosition() {
	if a.mini.saveTimer != nil {
		a.mini.saveTimer.Stop()
		a.mini.saveTimer = nil
	}
	if a.mini.win != nil && a.settings.MiniPlayer {
		b := a.mini.win.Bounds()
		a.settings.MiniPosition = [2]int{b.X, b.Y}
		a.settings.MiniPositionSet = true
	}
}

func (a *app) closeMiniPlayer() {
	a.keepMiniPosition()
	if w := a.mini.win; w != nil {
		a.mini.win = nil
		w.Close()
	}
}

func (a *app) miniMenu(menu *ui.Menu) {
	style := a.settings.effectiveMiniStyle()
	if menu.Item("经典横条").Checked(style == miniStyleBar).Chosen() {
		a.setMiniStyle(miniStyleBar)
	}
	if menu.Item("封面方卡").Checked(style == miniStyleCard).Chosen() {
		a.setMiniStyle(miniStyleCard)
	}
	menu.Separator()
	if menu.Item("始终置顶").Checked(a.settings.MiniAlwaysOnTop).Chosen() {
		a.toggleMiniAlwaysOnTop()
	}
	menu.Separator()
	if menu.Item("打开 " + appName).Chosen() {
		a.showMain()
	}
	if menu.Item("关闭迷你播放器").Chosen() {
		a.setMiniPlayerEnabled(false)
	}
}

func (a *app) miniView(c *ui.Context) {
	t := *c.Theme()
	pal := paletteFor(&t)
	t.Accent, t.AccentText = pal.accent, pal.onAccent
	t.Font = appFont
	c.SetTheme(&t)
	c.Root().Background(ui.Transparent)

	root := ui.Box(c.Key("mini-root")).Fill().Padding(miniShadowPad)
	root.Children(func() {
		if a.settings.effectiveMiniStyle() == miniStyleCard {
			a.miniCardView(c, pal)
		} else {
			a.miniBarView(c, pal)
		}
	})
}

// miniBarView renders the compact Liquid Glass horizontal floating bar:
// seamless optical glass lens over blurred album art + borderless controls +
// interactive comet/smoke progress bar.
func (a *app) miniBarView(c *ui.Context, pal palette) {
	cur := a.pl.Current()

	var art *artwork
	var tint ui.Color
	if cur != nil && cur.Album.PicURL != "" {
		art = a.art.get(spotify.Sized(cur.Album.PicURL, 240), true)
		if art != nil && !art.failed {
			tint = art.tint
		}
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}
	smokeCol := miniSmokeColor(tint, pal.accent, pal.dark)
	bd := backdrop(art)

	// Concentric outer corner radius: cover radius 12 + horizontal padding 12 = 24.
	const rad float32 = 24
	shadowCol := ui.RGBA(0, 0, 0, 0.16)
	baseFill := ui.RGBA(248, 250, 252, 0.72)
	trackBG := ui.RGBA(0, 0, 0, 0.09)
	fallbackBorder := ui.RGBA(0, 0, 0, 0.08)
	if pal.dark {
		shadowCol = ui.RGBA(0, 0, 0, 0.38)
		baseFill = ui.RGBA(26, 27, 32, 0.72)
		trackBG = ui.RGBA(255, 255, 255, 0.14)
		fallbackBorder = ui.RGBA(255, 255, 255, 0.10)
	}

	// Outer shell carries only the soft elevation shadow (well within miniShadowPad).
	shell := ui.Box(c.Key("mini-bar-shell")).Fill().Radius(rad).
		Shadow(0, 6, 16, -4, shadowCol)

	hovered := shell.Hovered()

	shell.Children(func() {
		// Clipped glass body: everything inside stays strictly within the rounded corners.
		body := ui.Box(c.Key("mini-bar")).Fill().Radius(rad).Clip().
			DragWindow().ContextMenu(a.miniMenu)

		body.Children(func() {
			// Layer 1 (flow, painted first): blurred album cover + atmospheric color wash.
			bgBox := ui.Box(c.Key("mini-bar-bg")).Fill().Radius(rad).Clip().PassThrough()
			bgBox.Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(r, baseFill, rad)
				glowAlpha := float32(0.18)
				if pal.dark {
					glowAlpha = 0.28
				}
				pt.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.16, Y: 0.45},
					Radius: r.W * 0.56,
					Stops: []ui.GradientStop{
						{Color: tint.Alpha(glowAlpha), At: 0},
						{Color: tint.Alpha(0), At: 1},
					},
				}, rad)
			})
			bgBox.Children(func() {
				if bd != nil {
					imgOpacity := float32(0.32)
					if pal.dark {
						imgOpacity = 0.46
					}
					ui.Image(c, bd).Fill().Fit(ui.Cover).Radius(rad).Opacity(imgOpacity).PassThrough()
				}
			})

			// Layer 2 (Liquid Glass lens container): let Liquid Glass draw its own
			// seamless optical edge — no double manual wireframe border!
			glassCard := ui.Box(c.Key("mini-bar-glass")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Radius(rad).Clip()
			if a.glassEnabled() {
				mat := a.liquidGlass(false, ui.Color{})
				mat.Tint = pal.bg.Alpha(0.24)
				glassCard.Material(mat)
			} else {
				glassCard.Border(0.5, fallbackBorder)
			}
			glassCard.Children(func() {
				// Foreground cover, title/artist, and compact playback controls — razor sharp!
				ui.Row(c).Fill().AlignItems(ui.Center).Padding(0, 14, 10, 12).Gap(10).Children(func() {
					artBtn := ui.ButtonBase(c.Key("mini-cover")).Size(48, 48).Shrink(0).Radius(12).
						Label("打开主窗口").Tooltip("点击打开 Sonora 主窗口")
					if artBtn.Clicked() {
						a.showMain()
					}
					artBtn.Children(func() {
						if cur != nil && cur.Album.PicURL != "" {
							a.cover(c, cur.Album.PicURL, 160, 48, 12).Shadow(0, 3, 10, -2, ui.RGBA(0, 0, 0, 0.22))
						} else {
							ui.Box(c).Size(48, 48).Radius(12).Center().Background(pal.hover).Children(func() {
								ui.Icon(c, iconNote).Size(20, 20).TextColor(pal.muted)
							})
						}
					})

					ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Justify(ui.Center).Children(func() {
						title := "此刻静悄悄的"
						artist := "选一首歌，让旋律流淌起来"
						if cur != nil {
							title = cmp.Or(cur.Name, "未知歌曲")
							artist = cmp.Or(cur.ArtistNames(), "未知歌手")
						}
						ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.text).SingleLine()
						ui.Text(c, artist).FontSize(11.5).TextColor(pal.muted).SingleLine()
					})

					a.miniTransport(c, pal.text, pal.muted, pal.accent, false)
				})

				// Interactive comet + smoke progress bar inset cleanly along the bottom.
				a.miniProgressBar(c.Key("mini-bar-scrub"), trackBG, tint, smokeCol, 14).
					Absolute().Left(20).Right(20).Bottom(2)

				// Topmost z-order inside glassCard: single clean close button on hover.
				if hovered {
					if miniCornerCloseBtn(c.Key("mini-close"), pal.muted).
						Absolute().Top(6).Right(8).Clicked() {
						a.setMiniPlayerEnabled(false)
					}
				}
			})
		})
	})
}

// miniCardView renders the Apple-Music-style square artwork mini player:
// crisp album cover + comet/smoke bottom progress bar at rest, Liquid Glass
// floating playback capsule + top-right close button on hover.
func (a *app) miniCardView(c *ui.Context, pal palette) {
	cur := a.pl.Current()

	var bmp *ui.Bitmap
	var tint ui.Color
	if cur != nil && cur.Album.PicURL != "" {
		bmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 420), false))
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}
	smokeCol := miniSmokeColor(tint, pal.accent, true)

	const rad float32 = 24
	white := ui.RGB(255, 255, 255)
	fallbackBG := ui.Hex("#1e2026")

	// Outer shell carries only the refined drop shadow (fits comfortably inside miniShadowPad).
	shell := ui.Box(c.Key("mini-card-shell")).Fill().Radius(rad).
		Shadow(0, 8, 18, -4, ui.RGBA(0, 0, 0, 0.30))

	hovered := shell.Hovered()
	hoverAlpha := shell.AnimateTo("mini-card-hover", boolFloat(hovered), ui.Animation{Duration: 150 * time.Millisecond})

	shell.Children(func() {
		card := ui.Box(c.Key("mini-card")).Fill().Radius(rad).Clip().
			DragWindow().ContextMenu(a.miniMenu).
			Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Fill(r, fallbackBG, rad)
				if bmp == nil {
					pt.FillRadialGradient(r, ui.RadialGradient{
						Center: ui.Point{X: 0.35, Y: 0.30},
						Radius: r.W * 0.75,
						Stops: []ui.GradientStop{
							{Color: ui.Hex("#18C964").Alpha(0.28), At: 0},
							{Color: ui.Hex("#0EA5E9").Alpha(0.14), At: 0.6},
							{Color: ui.Transparent, At: 1},
						},
					}, rad)
				}
			})

		card.Children(func() {
			// Layer 1: Full-bleed album cover.
			if bmp != nil {
				ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover).Radius(rad).PassThrough()
			} else {
				ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().PassThrough().Children(func() {
					ui.Icon(c, iconNote).Size(46, 46).TextColor(white.Alpha(0.32))
				})
			}

			// Layer 2: Soft readability scrim (no harsh white wireframe edge).
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.Clip(r, rad, func() {
					if hoverAlpha > 0.01 {
						pt.Fill(r, ui.RGBA(10, 12, 16, 0.28*hoverAlpha), rad)
					}
					pt.FillGradient(r, ui.LinearGradient{
						Angle: 180,
						Stops: []ui.GradientStop{
							{Color: ui.RGBA(0, 0, 0, 0.14*hoverAlpha), At: 0},
							{Color: ui.RGBA(0, 0, 0, 0.0), At: 0.42},
							{Color: ui.RGBA(0, 0, 0, 0.76), At: 1},
						},
					}, rad)
				})
			})

			// Layer 3: Bottom song info (click to open Sonora main window).
			infoBtn := ui.ButtonBase(c.Key("card-open-main")).Absolute().Left(10).Right(10).Bottom(18).
				Padding(4, 6).Radius(8).Label("打开主窗口").Tooltip("点击打开 Sonora 主窗口")
			if infoBtn.Clicked() {
				a.showMain()
			}
			infoBtn.Children(func() {
				ui.Column(c).Gap(1).Children(func() {
					title := "此刻静悄悄的"
					artist := "选一首歌，让旋律流淌起来"
					if cur != nil {
						title = cmp.Or(cur.Name, "未知歌曲")
						artist = cmp.Or(cur.ArtistNames(), "未知歌手")
					}
					ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(white).SingleLine()
					ui.Text(c, artist).FontSize(11).TextColor(white.Alpha(0.78)).SingleLine()
				})
			})

			// Layer 4: Interactive comet + smoke progress bar cleanly inset along bottom.
			a.miniProgressBar(c.Key("mini-card-scrub"), white.Alpha(0.24), white.Alpha(0.92), smokeCol.Mix(white, 0.30), 14).
				Absolute().Left(16).Right(16).Bottom(4)

			// Layer 5: Liquid Glass floating control dock in the center band ONLY.
			if hoverAlpha > 0.05 {
				ui.Box(c.Key("mini-card-controls")).Absolute().Top(52).Left(8).Right(8).Bottom(56).Center().Opacity(hoverAlpha).Children(func() {
					dock := ui.Box(c.Key("mini-card-dock")).Radius(999).Padding(5, 10)
					if a.glassEnabled() {
						dock.Material(a.liquidGlass(false, ui.RGBA(12, 14, 18, 0.34)))
					} else {
						dock.Background(ui.RGBA(16, 18, 24, 0.72))
					}
					dock.Children(func() {
						a.miniTransport(c, white, white.Alpha(0.74), tint.Mix(white, 0.40), true)
					})
				})
			}

			// Layer 6 (topmost z-order): single unmistakable Liquid Glass close button.
			if hoverAlpha > 0.05 {
				closeBtn := ui.ButtonBase(c.Key("card-close")).Absolute().Top(10).Right(10).
					Size(24, 24).Center().Radius(999).Opacity(hoverAlpha).
					TextColor(white).Label("关闭迷你播放器").Tooltip("关闭迷你播放器")
				if a.glassEnabled() {
					closeBtn.Material(a.liquidGlass(true, ui.RGBA(0, 0, 0, 0.28)))
				} else {
					bg := ui.RGBA(0, 0, 0, 0.48)
					if closeBtn.Hovered() {
						bg = ui.RGBA(0, 0, 0, 0.72)
					}
					closeBtn.Background(bg)
				}
				closeBtn.Children(func() { ui.Icon(c, iconX).Size(11, 11) })
				if closeBtn.Clicked() {
					a.setMiniPlayerEnabled(false)
				}
			}
		})
	})
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func miniCornerCloseBtn(c *ui.Context, col ui.Color) ui.Element {
	b := ui.ButtonBase(c).Size(20, 20).Center().Radius(999).TextColor(col).Label("关闭迷你播放器").Tooltip("关闭迷你播放器")
	if b.Hovered() {
		b.Background(ui.RGBA(128, 128, 128, 0.20))
	}
	b.Children(func() { ui.Icon(c, iconX).Size(10, 10) })
	return b
}

// miniTransport renders compact shuffle, prev, play/pause, next, and repeat
// controls tailored for both the horizontal bar and square card mini players.
func (a *app) miniTransport(c *ui.Context, color, dim, on ui.Color, cardMode bool) {
	pl := a.pl
	shuffleCol, sicon, stip := dim, iconShuffle, "顺序播放（点击切换随机）"
	switch pl.ShuffleMode() {
	case player.ShuffleOn:
		shuffleCol, stip = on, "随机播放（点击切换智能随机）"
	case player.ShuffleSmart:
		shuffleCol, sicon, stip = on, iconSmartShuffle, "智能随机播放（点击切回顺序）"
	}

	repeatCol, ricon, rtip := dim, iconRepeat, "不循环（点击开启列表循环）"
	switch pl.Repeat() {
	case player.RepeatAll:
		repeatCol, rtip = on, "列表循环（点击切换单曲循环）"
	case player.RepeatOne:
		repeatCol, ricon, rtip = on, iconRepeat1, "单曲循环（点击关闭循环）"
	}

	btn := func(key string, icon *ui.SVG, iconSize, boxSize float32, col ui.Color, tip string) ui.Element {
		b := ui.ButtonBase(c.Key(key)).Size(boxSize, boxSize).Center().Radius(999).TextColor(col).Label(tip).Tooltip(tip)
		bg := ui.Transparent
		if b.Hovered() {
			if cardMode {
				bg = ui.RGBA(255, 255, 255, 0.22)
			} else {
				bg = ui.RGBA(128, 128, 128, 0.16)
			}
		}
		b.Background(bg)
		pose := ui.Pose{}
		if b.Pressed() {
			pose.Scale = 0.88
		}
		b.AnimatePose(pose, ui.Snappy)
		b.Children(func() { ui.Icon(c, icon).Size(iconSize, iconSize) })
		return b
	}

	gap := float32(2)
	if cardMode {
		gap = 2
	}
	ui.Row(c).AlignItems(ui.Center).Gap(gap).Shrink(0).Children(func() {
		btnSize := float32(28)
		navSize := float32(30)
		if cardMode {
			btnSize = 26
			navSize = 28
		}
		if btn("mini-shuffle", sicon, 13, btnSize, shuffleCol, stip).Clicked() {
			a.cycleShuffle()
		}
		if btn("mini-prev", iconPrev, 14, navSize, color, "上一首").Clicked() {
			pl.Prev()
		}
		playBox := float32(34)
		playIconSize := float32(20)
		if cardMode {
			playBox = 36
			playIconSize = 21
		}
		pb := ui.ButtonBase(c.Key("mini-play")).Size(playBox, playBox).Center().Radius(999).Label("播放/暂停").TextColor(color)
		playBG := ui.Transparent
		if pb.Hovered() {
			if cardMode {
				playBG = ui.RGBA(255, 255, 255, 0.22)
			} else {
				playBG = ui.RGBA(128, 128, 128, 0.16)
			}
		}
		pb.Background(playBG)
		pose := ui.Pose{}
		if pb.Pressed() {
			pose.Scale = 0.88
		}
		pb.AnimatePose(pose, ui.Snappy)
		pb.Children(func() { playPause(c, pb, pl.Playing(), playIconSize) })
		if pb.Clicked() {
			pl.TogglePlay()
		}
		if btn("mini-next", iconNext, 14, navSize, color, "下一首").Clicked() {
			pl.Next()
		}
		if btn("mini-repeat", ricon, 13, btnSize, repeatCol, rtip).Clicked() {
			a.cycleRepeat()
		}
	})
}
