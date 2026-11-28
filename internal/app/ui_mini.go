package app

import (
	"cmp"
	"math"
	"runtime"
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
	miniShadowPad = 24
	miniBarW      = 360
	miniBarH      = 56
	miniCardW     = 232
	miniCardArtH  = 212
	miniCardFootH = 112
	miniCardH     = miniCardArtH + miniCardFootH
)

type miniState struct {
	win       *mygo.Window
	style     string
	placing   bool
	saveTimer *time.Timer
	scrub     scrubState
}

// titlebarMiniButton renders the top-right window-mode button beside the native
// minimize/maximize/close controls to switch directly from the main window into
// the floating mini player. On Windows it matches the exact 46×H square caption
// tile geometry, hover/press backplate, and crisp thin glyph weight of MyGoCaption.
func (a *app) titlebarMiniButton(c *ui.Context, darkOnOverlay bool) {
	if runtime.GOOS == "darwin" {
		col := a.pal.muted
		if darkOnOverlay {
			col = ui.RGBA(255, 255, 255, 0.72)
		}
		ui.Row(c).Absolute().Top(0).Right(14).Height(topBar(c)).AlignItems(ui.Center).Children(func() {
			btn := iconButton(c.Key("mini-mode-switch"), iconMiniPlayer, 15, col, "切换迷你模式").Tooltip("切换迷你模式")
			if btn.Clicked() {
				a.enterMiniMode()
			}
		})
		return
	}

	bar := c.TitleBar()
	h := bar.Height
	if h <= 0 {
		h = topBar(c)
	}
	dark := a.pal.dark || darkOnOverlay
	fore := a.pal.muted
	hoverFore := a.pal.text
	hoverBG := a.pal.hover
	pressBG := a.pal.press
	if dark {
		fore = ui.RGBA(255, 255, 255, 0.72)
		hoverFore = ui.RGB(255, 255, 255)
		hoverBG = ui.RGBA(255, 255, 255, 0.12)
		pressBG = ui.RGBA(255, 255, 255, 0.20)
	}
	ui.Row(c).Absolute().Top(0).Right(bar.Right+6).Height(h).AlignItems(ui.Center).Children(func() {
		b := ui.ButtonBase(c.Key("mini-mode-switch")).
			Size(28, 28).Center().Radius(8).
			FocusRing(false).Label("切换迷你模式").Tooltip("切换迷你模式")
		bg := ui.Transparent
		col := fore
		switch {
		case b.Pressed():
			bg = pressBG
			col = hoverFore
		case b.Hovered():
			bg = hoverBG
			col = hoverFore
		}
		b.Background(b.AnimateColor("mini-btn-bg", bg, ui.Animation{Duration: 110 * time.Millisecond}))
		b.Children(func() {
			ui.Icon(c.Key("mini-btn-icon"), iconMiniCaption).Size(13.5, 13.5).TextColor(col)
		})
		if b.Clicked() {
			a.enterMiniMode()
		}
	})
}

// enterMiniMode switches directly from the full main window to the floating
// mini player: opens the mini window immediately and hides the main window.
func (a *app) enterMiniMode() {
	if a.splash.win != nil && !a.splash.done {
		a.finishSplash()
	}
	was := a.settings.MiniPlayer
	a.setMiniPlayerEnabled(true)
	if was {
		a.syncMiniPlayer()
	}
	if a.win != nil && a.settings.MiniPlayer {
		a.win.Hide()
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

// miniProgressBar draws an interactive, seekable progress bar synchronized with
// the user's chosen ProgressStyle (aurora, waveform, pulse, stardust, smoke, or classic).
func (a *app) miniProgressBar(c *ui.Context, trackCol, fillCol, tintCol, accentCol ui.Color, dark bool, height, restThick, hotThick float32) ui.Element {
	pl := a.pl
	st := &a.mini.scrub
	dur := pl.Duration().Seconds()
	if !st.active && !st.held {
		st.v = pl.Position().Seconds()
	}
	if dur <= 0 {
		st.v = 0
	}
	maxV := max(dur, 1)
	s := ui.SliderBase(c, &st.v, 0, maxV).Height(height).Disabled(dur <= 0).
		Step(1).FocusRing(false).Label("迷你播放进度").Cursor(ui.CursorPointer)
	s.HandleInput(func(ev ui.InputEvent) bool {
		if ev.Button != 0 || dur <= 0 {
			return false
		}
		switch ev.Kind {
		case ui.InputPointerDown:
			st.held, st.trackURI = true, trackURIOf(pl.Current())
		case ui.InputPointerUp:
			st.held = false
			if ev.Canceled {
				st.active, st.trackURI = false, ""
			}
		}
		return false
	})
	pressed := st.held
	curURI := trackURIOf(pl.Current())
	switch {
	case pressed:
		st.active = true
	case st.active:
		st.active = false
		if st.trackURI != "" && st.trackURI == curURI {
			pl.Seek(secs(min(st.v, max(0, dur-1))))
		}
		st.trackURI = ""
	case s.Changed():
		pl.Seek(secs(min(st.v, max(0, dur-1))))
	}
	scrubbing := pressed && st.trackURI != "" && st.trackURI == curURI
	hot := s.Hovered() || scrubbing
	thick := restThick
	if hot {
		thick = hotThick
	}
	thick = s.AnimateTo("mini-thick", thick, ui.Snappy)
	knob := float32(0)
	if hot {
		knob = 1
	}
	knob = s.AnimateTo("mini-knob", knob, ui.Snappy)

	style := a.settings.effectiveProgressStyle()
	animating := float32(0)
	if progressStyleAnimates(style) && !c.Preferences().ReduceMotion && pl.Playing() && !pl.Loading() && !scrubbing && dur > 0 {
		animating = 1
	}
	animating = s.AnimateTo("mini-smoke", animating, ui.Animation{Duration: 500 * time.Millisecond})
	white := ui.RGB(255, 255, 255)
	_, c2 := vibrantProgressColors(tintCol, accentCol, style, dark)

	s.Draw(func(p *ui.Painter, r ui.Rect) {
		at := st.v
		if !scrubbing {
			at = pl.SongPosition().Seconds()
		}
		frac := float32(0)
		if dur > 0 {
			frac = float32(min(1, max(0, at/dur)))
		}
		head, cy := r.X+r.W*frac, r.Y+r.H/2
		bass := float32(0)
		if animating > 0.01 {
			lv, live := pl.Levels()
			st.level.step(p.Now(), lv, live)
			bass = st.level.group(0, 6)
		}
		drawProgressEffect(p, r, frac, thick, animating, bass, p.Now(), style, trackCol, fillCol, tintCol, accentCol, nil, dark)
		if knob > 0.01 {
			k := 9 * knob
			p.Shadow(ui.Rect{X: head - k/2, Y: cy - k/2, W: k, H: k}, k/2, 0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.28))
			p.Fill(ui.Rect{X: head - k/2, Y: cy - k/2, W: k, H: k}, white.Mix(c2, 0.20), k/2)
		}
		switch {
		case animating > 0.01 && (pl.Playing() || animating < 1):
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

func (a *app) miniSplashActive() bool {
	return (a.splash.win != nil || !a.splash.startedAt.IsZero()) && !a.splash.done
}

// syncMiniPlayer creates, updates, repositions, or hides the floating mini
// player window according to the user's current settings.
func (a *app) syncMiniPlayer() {
	if a.win == nil || a.quitting || shotHook != nil {
		return
	}
	if !a.settings.MiniPlayer || a.miniSplashActive() {
		if a.mini.win != nil {
			a.keepMiniPosition()
			a.mini.win.Hide()
		}
		return
	}

	style := a.settings.effectiveMiniStyle()
	// When switching style while open, anchor around the bottom-right corner
	// of the current window so compact bar <-> split card transitions stay put naturally.
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
	a.ensureMiniWindow(bounds, style)
	a.mini.win.ShowInactive()
	a.mini.win.Invalidate()
}

// ensureMiniWindow creates (hidden) or repositions the mini player window
// without showing it yet, allowing morph animations to pre-warm it cleanly.
func (a *app) ensureMiniWindow(bounds mygo.Rectangle, style string) {
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
			if a.quitting {
				return
			}
			e.PreventDefault()
			a.closeMiniWindow()
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
}

func (a *app) setMiniPlayerEnabled(on bool) {
	if a.settings.MiniPlayer == on {
		return
	}
	if !on {
		a.keepMiniPosition()
	}
	a.settings.MiniPlayer = on
	a.syncMiniPlayer()
	a.redraw()
	if !on {
		go a.settings.save()
	}
}

// restoreMainFromMini switches back from mini mode to the normal main window.
func (a *app) restoreMainFromMini() {
	a.setMiniPlayerEnabled(false)
	a.showMain()
}

// closeMiniWindow handles closing the frameless mini window consistently
// whether triggered via OS close, UI close buttons, or the context menu.
func (a *app) closeMiniWindow() {
	switch {
	case a.settings.QuitOnClose:
		mygo.App.Quit()
	case runtime.GOOS != "darwin" && a.tray == nil:
		a.restoreMainFromMini()
	default:
		a.setMiniPlayerEnabled(false)
	}
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
	if menu.Item("紧凑长条").Checked(style == miniStyleBar).Chosen() {
		a.setMiniStyle(miniStyleBar)
	}
	if menu.Item("封面卡片").Checked(style == miniStyleCard).Chosen() {
		a.setMiniStyle(miniStyleCard)
	}
	menu.Submenu("进度条样式", a.progressStyleMenu)
	menu.Separator()
	if menu.Item("始终置顶").Checked(a.settings.MiniAlwaysOnTop).Chosen() {
		a.toggleMiniAlwaysOnTop()
	}
	menu.Separator()
	if menu.Item("打开 " + appName).Chosen() {
		a.restoreMainFromMini()
	}
	if menu.Item("关闭迷你播放器").Chosen() {
		a.closeMiniWindow()
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

// miniRoundedRectPath constructs a cubic-Bezier rounded rectangle path for
// crisp inset hairline strokes and directional specular rims.
func miniRoundedRectPath(r ui.Rect, rad float32) *ui.Path {
	if rad < 0 {
		rad = 0
	}
	if maxR := min(r.W, r.H) / 2; rad > maxR {
		rad = maxR
	}
	const k float32 = 0.55228475
	c := rad * k
	x0, y0 := r.X, r.Y
	x1, y1 := r.X+r.W, r.Y+r.H

	var p ui.Path
	p.MoveTo(x0+rad, y0)
	p.LineTo(x1-rad, y0)
	p.CubeTo(x1-rad+c, y0, x1, y0+rad-c, x1, y0+rad)
	p.LineTo(x1, y1-rad)
	p.CubeTo(x1, y1-rad+c, x1-rad+c, y1, x1-rad, y1)
	p.LineTo(x0+rad, y1)
	p.CubeTo(x0+rad-c, y1, x0, y1-rad+c, x0, y1-rad)
	p.LineTo(x0, y0+rad)
	p.CubeTo(x0, y0+rad-c, x0+rad-c, y0, x0+rad, y0)
	p.Close()
	return &p
}

// drawMiniSoftRim paints an inset hairline border fully inside r (so .Clip()
// never slices straight segments differently from rounded corners) with a
// subtle directional top-edge sheen.
func drawMiniSoftRim(pt *ui.Painter, r ui.Rect, rad float32, dark, fullBleedTop bool) {
	in := ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}
	inRad := max(rad-0.5, 0)
	p := miniRoundedRectPath(in, inRad)
	if dark {
		topAlpha := float32(0.16)
		if fullBleedTop {
			topAlpha = 0.12
		}
		pt.StrokePathGradient(p, 1, ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{
				{Color: ui.RGBA(255, 255, 255, topAlpha), At: 0},
				{Color: ui.RGBA(255, 255, 255, 0.08), At: 0.28},
				{Color: ui.RGBA(255, 255, 255, 0.05), At: 1},
			},
		})
		return
	}
	pt.StrokePathGradient(p, 1, ui.LinearGradient{
		Angle: 180,
		Stops: []ui.GradientStop{
			{Color: ui.RGBA(0, 0, 0, 0.055), At: 0},
			{Color: ui.RGBA(0, 0, 0, 0.075), At: 1},
		},
	})
	if !fullBleedTop {
		hi := ui.Rect{X: r.X + 1.5, Y: r.Y + 1.5, W: r.W - 3, H: r.H - 3}
		hiRad := max(rad-1.5, 0)
		pt.StrokePathGradient(miniRoundedRectPath(hi, hiRad), 1, ui.LinearGradient{
			Angle: 180,
			Stops: []ui.GradientStop{
				{Color: ui.RGBA(255, 255, 255, 0.72), At: 0},
				{Color: ui.RGBA(255, 255, 255, 0), At: 0.32},
			},
		})
	}
}

// miniBarView renders the Apple Music Compact MiniPlayer (360×56):
// crisp pure-light or deep-dark surface with strictly concentric 16px outer
// corners (8px padding + 8px cover radius), inset soft specular rim, 40×40
// rounded cover on the left, centered title/artist + synced progress bar at
// rest crossfading in-place to full transport controls on hover, plus restore
// and close actions on the right.
func (a *app) miniBarView(c *ui.Context, pal palette) {
	cur := a.pl.Current()

	var tint ui.Color
	if cur != nil && cur.Album.PicURL != "" {
		art := a.art.get(spotify.Sized(cur.Album.PicURL, 240), false)
		if art != nil && !art.failed {
			tint = art.tint
		}
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}
	smokeCol := miniSmokeColor(tint, pal.accent, pal.dark)

	const rad float32 = 16 // Strictly concentric: cover rad (8) + padding (8) = 16
	var (
		shadowCol  ui.Color
		surfaceTop ui.Color
		surfaceBot ui.Color
		coverRim   ui.Color
		trackBG    ui.Color
		fillCol    ui.Color
	)
	if pal.dark {
		shadowCol = ui.RGBA(0, 0, 0, 0.46)
		surfaceTop = ui.RGBA(34, 34, 39, 0.97)
		surfaceBot = ui.RGBA(24, 24, 28, 0.97)
		coverRim = ui.RGBA(255, 255, 255, 0.10)
		trackBG = ui.RGBA(255, 255, 255, 0.14)
		fillCol = smokeCol
	} else {
		shadowCol = ui.RGBA(0, 0, 0, 0.13)
		surfaceTop = ui.RGBA(255, 255, 255, 0.98)
		surfaceBot = ui.RGBA(247, 247, 250, 0.98)
		coverRim = ui.RGBA(0, 0, 0, 0.07)
		trackBG = ui.RGBA(0, 0, 0, 0.09)
		fillCol = pal.accent
	}

	shell := ui.Box(c.Key("mini-bar-shell")).Fill().Radius(rad).
		Shadow(0, 8, 22, -4, shadowCol)

	hovered := shell.Hovered()
	hoverAlpha := shell.AnimateTo("mini-bar-hover", boolFloat(hovered), ui.Animation{Duration: 160 * time.Millisecond})

	shell.Children(func() {
		body := ui.Box(c.Key("mini-bar")).Fill().Radius(rad).Clip().
			DragWindow().ContextMenu(a.miniMenu).
			Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.FillGradient(r, ui.LinearGradient{
					Angle: 180,
					From:  surfaceTop,
					To:    surfaceBot,
				}, rad)
			})

		body.Children(func() {
			ui.Row(c).Fill().AlignItems(ui.Center).Padding(0, 8, 0, 8).Gap(10).DragWindow().Children(func() {
				// Left: 40×40 crisp rounded album artwork.
				// Clicking toggles Like for Spotify tracks (since the right side already
				// has a dedicated expand button), falling back to restoring the main window
				// when no likeable track is playing.
				canLike := cur != nil && !cur.Local() && !cur.Episode
				liked := canLike && a.liked[libraryID(cur)]
				artLabel, artTip := "打开主窗口", "点击返回 Sonora 主窗口"
				if canLike {
					if liked {
						artLabel, artTip = "取消喜欢", "点击取消喜欢"
					} else {
						artLabel, artTip = "喜欢这首歌", "点击添加到我喜欢的音乐"
					}
				}
				artBtn := ui.ButtonBase(c.Key("mini-cover")).Size(40, 40).Shrink(0).Radius(8).
					Label(artLabel).Tooltip(artTip)
				if artBtn.Clicked() {
					if canLike {
						a.toggleLike(cur)
					} else {
						a.restoreMainFromMini()
					}
				}
				artHovered := artBtn.Hovered()
				artBtn.Children(func() {
					if cur != nil && cur.Album.PicURL != "" {
						a.cover(c, cur.Album.PicURL, 160, 40, 8).Shadow(0, 2, 8, -2, ui.RGBA(0, 0, 0, 0.22))
					} else {
						ui.Box(c).Size(40, 40).Radius(8).Center().Background(pal.hover).Children(func() {
							ui.Icon(c, iconNote).Size(18, 18).TextColor(pal.muted)
						})
					}
					if canLike && artHovered {
						heartIcon := iconHeart
						heartCol := ui.RGB(255, 255, 255)
						if liked {
							heartIcon = iconHeartFill
							heartCol = pal.accent
						}
						ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(8).Center().
							Background(ui.RGBA(0, 0, 0, 0.44)).PassThrough().Children(func() {
							ui.Icon(c, heartIcon).Size(18, 18).TextColor(heartCol)
						})
					} else if liked {
						ui.Box(c).Absolute().Right(3).Bottom(3).Size(14, 14).Radius(7).Center().
							Background(ui.RGBA(0, 0, 0, 0.56)).PassThrough().Children(func() {
							ui.Icon(c, iconHeartFill).Size(8.5, 8.5).TextColor(pal.accent)
						})
					}
					ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
						in := ui.Rect{X: r.X + 0.5, Y: r.Y + 0.5, W: r.W - 1, H: r.H - 1}
						pt.Stroke(in, coverRim, 7.5, 1)
					})
				})

				// Center: Apple Music signature crossfade — centered title/artist at rest,
				// in-place playback controls on hover, with slim synced progress bar anchored underneath.
				ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Height(44).Justify(ui.SpaceBetween).DragWindow().Children(func() {
					stage := ui.Box(c.Key("mini-bar-stage")).Grow(1).WidthPercent(100).DragWindow()
					stage.Children(func() {
						// Resting layer: centered title & artist (pass-through so dragging works anywhere).
						restAlpha := 1 - hoverAlpha
						if restAlpha > 0.02 {
							rest := ui.Column(c.Key("mini-bar-meta")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
								Center().Gap(1).Opacity(restAlpha).PassThrough()
							rest.Children(func() {
								title := "此刻静悄悄的"
								artist := "选一首歌，让旋律流淌起来"
								if cur != nil {
									title = cmp.Or(cur.Name, "未知歌曲")
									artist = cmp.Or(cur.ArtistNames(), "未知歌手")
								}
								ui.Text(c, title).FontSize(12.5).FontWeight(600).TextColor(pal.text).SingleLine()
								ui.Text(c, artist).FontSize(10.5).TextColor(pal.muted).SingleLine()
							})
						}

						// Hover layer: crisp borderless transport controls in-place.
						if hoverAlpha > 0.02 {
							ctrl := ui.Box(c.Key("mini-bar-ctrls")).Absolute().Top(0).Left(0).Right(0).Bottom(0).
								Center().Opacity(hoverAlpha).DragWindow()
							if hoverAlpha < 0.2 {
								ctrl.PassThrough()
							}
							ctrl.Children(func() {
								a.miniTransport(c, pal.text, pal.muted, pal.accent, pal.dark, false)
							})
						}
					})

					// Slim interactive progress bar synced with main player style.
					a.miniProgressBar(c.Key("mini-bar-scrub"), trackBG, fillCol, tint, pal.accent, pal.dark, 11, 2.8, 4.5).
						WidthPercent(100)
				})

				// Right: crisp minimal 2-icon strip (restore main window, close mini player).
				ui.Row(c).AlignItems(ui.Center).Gap(2).Shrink(0).Children(func() {
					iconAlpha := float32(0.72) + 0.28*hoverAlpha
					if miniActionBtn(c.Key("mini-restore-main"), iconExpand, 12.5, 22, pal.muted, pal.text, pal.hover, "返回主窗口").
						Opacity(iconAlpha).Clicked() {
						a.restoreMainFromMini()
					}
					closeAlpha := float32(0.45) + 0.55*hoverAlpha
					if miniActionBtn(c.Key("mini-close"), iconX, 11.5, 22, pal.muted, pal.text, pal.hover, "关闭迷你播放器").
						Opacity(closeAlpha).Clicked() {
						a.closeMiniWindow()
					}
				})
			})

			// Inset directional soft rim fully inside clip bounds.
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
				drawMiniSoftRim(pt, r, rad, pal.dark, false)
			})
		})
	})
}

// miniCardView renders the Apple Music Expanded MiniPlayer (232×324) with a
// seamless gradient-melt transition: full-bleed unobstructed album cover on top
// whose bottom 74px feathers smoothly via an Oklab gradient into the Light or
// Dark control deck below (zero hard horizontal divider), finished with an
// inset directional soft rim. Hovering reveals a sleek frosted top-right
// toolbar (restore main window, close).
func (a *app) miniCardView(c *ui.Context, pal palette) {
	cur := a.pl.Current()

	var bmp *ui.Bitmap
	var tint ui.Color
	if cur != nil && cur.Album.PicURL != "" {
		bmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 460), false))
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}
	smokeCol := miniSmokeColor(tint, pal.accent, pal.dark)

	const rad float32 = 18
	const artMeltH float32 = 226
	white := ui.RGB(255, 255, 255)
	var (
		shadowCol ui.Color
		artBG     ui.Color
		deckTop   ui.Color
		deckBot   ui.Color
		trackBG   ui.Color
		fillCol   ui.Color
	)
	if pal.dark {
		shadowCol = ui.RGBA(0, 0, 0, 0.50)
		artBG = ui.Hex("#18181c")
		deckTop = ui.Hex("#1e1e23")
		deckBot = ui.Hex("#151519")
		trackBG = ui.RGBA(255, 255, 255, 0.16)
		fillCol = smokeCol
	} else {
		shadowCol = ui.RGBA(0, 0, 0, 0.16)
		artBG = ui.Hex("#eceef2")
		deckTop = ui.Hex("#ffffff")
		deckBot = ui.Hex("#f5f5f8")
		trackBG = ui.RGBA(0, 0, 0, 0.10)
		fillCol = pal.accent
	}

	shell := ui.Box(c.Key("mini-card-shell")).Fill().Radius(rad).
		Shadow(0, 10, 26, -4, shadowCol)

	hovered := shell.Hovered()
	hoverAlpha := shell.AnimateTo("mini-card-hover", boolFloat(hovered), ui.Animation{Duration: 150 * time.Millisecond})

	shell.Children(func() {
		card := ui.Box(c.Key("mini-card")).Fill().Radius(rad).Clip().
			DragWindow().ContextMenu(a.miniMenu).
			Draw(func(pt *ui.Painter, r ui.Rect) {
				pt.FillGradient(r, ui.LinearGradient{
					Angle: 180,
					From:  deckTop,
					To:    deckBot,
				}, rad)
			})

		card.Children(func() {
			// Top: Full-bleed unobstructed album artwork extended slightly behind
			// the deck top edge so its bottom 74px dissolves smoothly into deckTop.
			artZone := ui.Box(c.Key("mini-card-art")).Absolute().Top(0).Left(0).Right(0).Height(artMeltH).
				Radius(rad, rad, 0, 0).Clip().Background(artBG).DragWindow().
				Draw(func(pt *ui.Painter, r ui.Rect) {
					if bmp == nil {
						accentAlpha := float32(0.18)
						if pal.dark {
							accentAlpha = 0.28
						}
						pt.FillRadialGradient(r, ui.RadialGradient{
							Center: ui.Point{X: 0.35, Y: 0.30},
							Radius: r.W * 0.80,
							Stops: []ui.GradientStop{
								{Color: pal.accent.Alpha(accentAlpha), At: 0},
								{Color: ui.Hex("#0EA5E9").Alpha(accentAlpha * 0.45), At: 0.6},
								{Color: ui.Transparent, At: 1},
							},
						}, 0)
					}
				})

			artZone.Children(func() {
				if bmp != nil {
					ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).
						Fit(ui.Cover).Radius(rad, rad, 0, 0).PassThrough()
				} else {
					ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(16).Center().PassThrough().Children(func() {
						noteCol := pal.muted
						if pal.dark {
							noteCol = white.Alpha(0.36)
						}
						ui.Icon(c, iconNote).Size(44, 44).TextColor(noteCol)
					})
				}

				// Smooth 6-stop Oklab feather dissolving the cover bottom into deckTop.
				ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(74).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
					pt.FillGradient(r, ui.LinearGradient{
						Angle: 180,
						Oklab: true,
						Stops: []ui.GradientStop{
							{Color: deckTop.Alpha(0), At: 0},
							{Color: deckTop.Alpha(0.06), At: 0.24},
							{Color: deckTop.Alpha(0.28), At: 0.50},
							{Color: deckTop.Alpha(0.64), At: 0.74},
							{Color: deckTop.Alpha(0.90), At: 0.90},
							{Color: deckTop, At: 1},
						},
					}, 0)
				})

				// Hover-only frosted floating toolbar in the top-right corner of artwork.
				if hoverAlpha > 0.05 {
					pill := ui.Row(c.Key("card-toolbar")).Absolute().Top(10).Right(10).
						AlignItems(ui.Center).Gap(2).Padding(3, 5).Radius(999).Opacity(hoverAlpha).
						Background(ui.RGBA(18, 18, 22, 0.64)).Border(0.5, white.Alpha(0.16))
					pill.Children(func() {
						if miniActionBtn(c.Key("card-restore-main"), iconExpand, 12.5, 22, white.Alpha(0.86), white, white.Alpha(0.18), "返回主窗口").Clicked() {
							a.restoreMainFromMini()
						}
						if miniActionBtn(c.Key("card-close"), iconX, 11.5, 22, white.Alpha(0.86), white, white.Alpha(0.18), "关闭迷你播放器").Clicked() {
							a.closeMiniWindow()
						}
					})
				}
			})

			// Full-card ambient cover-tinted bloom across the transition zone (no rectangular edge).
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
				alpha := float32(0.05)
				if pal.dark {
					alpha = 0.14
				}
				pt.FillRadialGradient(r, ui.RadialGradient{
					Center: ui.Point{X: 0.5, Y: 0.74},
					Radius: r.W * 0.68,
					Stops: []ui.GradientStop{
						{Color: tint.Alpha(alpha), At: 0},
						{Color: tint.Alpha(0), At: 1},
					},
				}, rad)
			})

			// Bottom: Control deck anchored seamlessly over the melted card base
			// (empty regions remain directly draggable).
			deck := ui.Column(c.Key("mini-card-deck")).Absolute().Left(0).Right(0).Bottom(0).Height(miniCardFootH).
				DragWindow().Padding(10, 14, 11, 14).Justify(ui.SpaceBetween)

			deck.Children(func() {
				// Row 1: Title + artist on the left, interactive like heart on the right.
				ui.Row(c).WidthPercent(100).AlignItems(ui.Center).Gap(8).Children(func() {
					ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(1).Children(func() {
						title := "此刻静悄悄的"
						artist := "选一首歌，让旋律流淌起来"
						if cur != nil {
							title = cmp.Or(cur.Name, "未知歌曲")
							artist = cmp.Or(cur.ArtistNames(), "未知歌手")
						}
						ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(pal.text).SingleLine()
						ui.Text(c, artist).FontSize(11).TextColor(pal.muted).SingleLine()
					})

					if cur != nil && !cur.Local() && !cur.Episode {
						liked := a.liked[libraryID(cur)]
						heartIcon := iconHeart
						heartCol := pal.muted
						heartTip := "喜欢这首歌"
						if liked {
							heartIcon = iconHeartFill
							heartCol = pal.accent
							heartTip = "取消喜欢"
						}
						if miniActionBtn(c.Key("card-like"), heartIcon, 14, 24, heartCol, pal.accent, pal.hover, heartTip).Clicked() {
							a.toggleLike(cur)
						}
					}
				})

				// Row 2: Interactive progress bar synced with main player style.
				a.miniProgressBar(c.Key("mini-card-scrub"), trackBG, fillCol, tint, pal.accent, pal.dark, 12, 3, 5).
					WidthPercent(100)

				// Row 3: Evenly spaced borderless Apple Music transport controls.
				a.miniTransport(c, pal.text, pal.muted, pal.accent, pal.dark, true)
			})

			// Unified inset soft specular hairline border over the rounded card.
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
				drawMiniSoftRim(pt, r, rad, pal.dark, true)
			})
		})
	})
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func miniActionBtn(c *ui.Context, icon *ui.SVG, iconSize, boxSize float32, col, hoverCol, hoverBG ui.Color, tip string) ui.Element {
	b := ui.ButtonBase(c).Size(boxSize, boxSize).Center().Radius(999).Label(tip).Tooltip(tip)
	curCol := col
	bg := ui.Transparent
	if b.Hovered() {
		curCol = hoverCol
		bg = hoverBG
	}
	b.TextColor(curCol).Background(b.AnimateColor("mini-act-bg", bg, ui.Animation{Duration: 110 * time.Millisecond}))
	pose := ui.Pose{}
	if b.Pressed() {
		pose.Scale = 0.90
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() { ui.Icon(c, icon).Size(iconSize, iconSize) })
	return b
}

// miniTransport renders compact borderless shuffle, prev, play/pause, next,
// and repeat controls tailored for both the Apple Music compact bar and split card.
func (a *app) miniTransport(c *ui.Context, color, dim, on ui.Color, onDarkOverlay, spread bool) {
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

	hoverBG := ui.RGBA(0, 0, 0, 0.065)
	if onDarkOverlay {
		hoverBG = ui.RGBA(255, 255, 255, 0.12)
	}

	btn := func(key string, icon *ui.SVG, iconSize, boxSize float32, col ui.Color, tip string) ui.Element {
		b := ui.ButtonBase(c.Key(key)).Size(boxSize, boxSize).Center().Radius(999).TextColor(col).Label(tip).Tooltip(tip)
		bg := ui.Transparent
		if b.Hovered() {
			bg = hoverBG
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

	row := ui.Row(c).AlignItems(ui.Center).Shrink(0)
	if spread {
		row.WidthPercent(100).Justify(ui.SpaceBetween)
	} else {
		row.Gap(2)
	}
	row.Children(func() {
		btnSize := float32(26)
		navSize := float32(28)
		playBox := float32(32)
		playIconSize := float32(18)
		if spread {
			btnSize = 28
			navSize = 30
			playBox = 34
			playIconSize = 20
		}
		if btn("mini-shuffle", sicon, 12.5, btnSize, shuffleCol, stip).Clicked() {
			a.cycleShuffle()
		}
		if btn("mini-prev", iconPrev, 13.5, navSize, color, "上一首").Clicked() {
			pl.Prev()
		}
		pb := ui.ButtonBase(c.Key("mini-play")).Size(playBox, playBox).Center().Radius(999).Label("播放/暂停").TextColor(color)
		playBG := ui.Transparent
		if pb.Hovered() {
			playBG = hoverBG
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
		if btn("mini-next", iconNext, 13.5, navSize, color, "下一首").Clicked() {
			pl.Next()
		}
		if btn("mini-repeat", ricon, 12.5, btnSize, repeatCol, rtip).Clicked() {
			a.cycleRepeat()
		}
	})
}
