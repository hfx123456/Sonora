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

	miniShadowPad = 14
	miniBarW      = 404
	miniBarH      = 76
	miniCardW     = 216
	miniCardH     = 216
)

type miniState struct {
	win       *mygo.Window
	style     string
	placing   bool
	saveTimer *time.Timer
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
		x := area.X + area.Width - w - 16
		y := area.Y + area.Height - h - 16
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

// miniProgress returns the current playback ratio [0, 1] and schedules a
// lightweight refresh while music is playing.
func (a *app) miniProgress(c *ui.Context) float32 {
	pl := a.pl
	dur := pl.Duration().Seconds()
	pos := pl.SongPosition().Seconds()
	if pl.Playing() && dur > 0 {
		c.After(250 * time.Millisecond)
	}
	if dur <= 0 || pos <= 0 {
		return 0
	}
	p := float32(pos / dur)
	if p > 1 {
		return 1
	}
	return p
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

// miniBarView renders the compact QQ-Music-inspired horizontal floating bar:
// cover + title/artist + shuffle/prev/play/next/repeat + bottom progress line.
func (a *app) miniBarView(c *ui.Context, pal palette) {
	cur := a.pl.Current()
	progress := a.miniProgress(c)

	var tint ui.Color
	if cur != nil {
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}

	surface := ui.RGBA(252, 252, 254, 0.94)
	border := ui.RGBA(0, 0, 0, 0.09)
	shadowCol := ui.RGBA(0, 0, 0, 0.20)
	if pal.dark {
		surface = ui.RGBA(28, 28, 32, 0.92)
		border = ui.RGBA(255, 255, 255, 0.12)
		shadowCol = ui.RGBA(0, 0, 0, 0.45)
	}

	const rad float32 = 20
	card := ui.Box(c.Key("mini-bar")).Fill().Radius(rad).Clip().
		DragWindow().ContextMenu(a.miniMenu).
		Shadow(0, 8, 22, -4, shadowCol).
		Draw(func(pt *ui.Painter, r ui.Rect) {
			pt.Fill(r, surface, rad)
			// Subtle ambient tint from the active album cover.
			glowAlpha := float32(0.11)
			if pal.dark {
				glowAlpha = 0.22
			}
			pt.FillRadialGradient(r, ui.RadialGradient{
				Center: ui.Point{X: 0.14, Y: 0.50},
				Radius: r.W * 0.55,
				Stops: []ui.GradientStop{
					{Color: tint.Alpha(glowAlpha), At: 0},
					{Color: tint.Alpha(0), At: 1},
				},
			}, rad)
			// Sleek playback progress hairline along the bottom edge.
			if progress > 0 {
				pt.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 2.5, W: r.W * progress, H: 2.5}, tint.Alpha(0.85), 1.5)
			}
			pt.Stroke(r, border, rad, 1)
		})

	hovered := card.Hovered()
	card.Children(func() {
		ui.Row(c).Fill().AlignItems(ui.Center).Padding(0, 12, 0, 12).Gap(10).Children(func() {
			// Album art button: click to open the main window.
			artBtn := ui.ButtonBase(c.Key("mini-cover")).Size(50, 50).Shrink(0).Radius(12).Label("打开主窗口").Tooltip("点击打开 Sonora 主窗口")
			if artBtn.Clicked() {
				a.showMain()
			}
			artBtn.Children(func() {
				if cur != nil && cur.Album.PicURL != "" {
					a.cover(c, cur.Album.PicURL, 160, 50, 12)
				} else {
					ui.Box(c).Size(50, 50).Radius(12).Center().Background(pal.hover).Children(func() {
						ui.Icon(c, iconNote).Size(20, 20).TextColor(pal.muted)
					})
				}
			})

			// Track title & artist.
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(2).Justify(ui.Center).Children(func() {
				title := "暂未播放"
				artist := appName
				if cur != nil {
					title = cmp.Or(cur.Name, "未知歌曲")
					artist = cmp.Or(cur.ArtistNames(), "未知歌手")
				}
				ui.Text(c, title).FontSize(13.5).FontWeight(600).TextColor(pal.text).SingleLine()
				ui.Text(c, artist).FontSize(11.5).TextColor(pal.muted).SingleLine()
			})

			// Compact playback controls: shuffle, prev, play/pause, next, repeat.
			a.miniTransport(c, pal.text, pal.muted, pal.accent, false)
		})

		// Subtle hover actions on top-right corner: switch to card, open main, close.
		if hovered {
			ui.Row(c.Key("mini-bar-actions")).Absolute().Top(4).Right(8).Gap(2).AlignItems(ui.Center).Children(func() {
				if miniCornerBtn(c.Key("mini-to-card"), iconExpand, pal.muted, "切换为封面方卡").Clicked() {
					a.setMiniStyle(miniStyleCard)
				}
				if miniCornerBtn(c.Key("mini-close"), iconX, pal.muted, "关闭迷你播放器").Clicked() {
					a.setMiniPlayerEnabled(false)
				}
			})
		}
	})
}

// miniCardView renders the Apple-Music-style square artwork mini player:
// crisp album cover + bottom title & progress bar at rest, frosted controls on hover.
func (a *app) miniCardView(c *ui.Context, pal palette) {
	cur := a.pl.Current()
	progress := a.miniProgress(c)

	var bmp *ui.Bitmap
	var tint ui.Color
	if cur != nil && cur.Album.PicURL != "" {
		bmp = bitmap(a.art.get(spotify.Sized(cur.Album.PicURL, 420), false))
		tint = a.coverTint(cur.Album.PicURL, 120)
	}
	if tint == (ui.Color{}) {
		tint = pal.accent
	}

	const rad float32 = 24
	white := ui.RGB(255, 255, 255)
	fallbackBG := ui.Hex("#1e2026")
	if !pal.dark && bmp == nil {
		fallbackBG = ui.Hex("#262932")
	}

	card := ui.Box(c.Key("mini-card")).Fill().Radius(rad).Clip().
		DragWindow().ContextMenu(a.miniMenu).
		Shadow(0, 12, 28, -6, ui.RGBA(0, 0, 0, 0.36)).
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

	hovered := card.Hovered()
	hoverAlpha := card.AnimateTo("mini-card-hover", boolFloat(hovered), ui.Animation{Duration: 160 * time.Millisecond})

	card.Children(func() {
		if bmp != nil {
			ui.Image(c, bmp).Absolute().Top(0).Left(0).Right(0).Bottom(0).Fit(ui.Cover).Radius(rad)
		} else {
			ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Center().Children(func() {
				ui.Icon(c, iconNote).Size(46, 46).TextColor(white.Alpha(0.32))
			})
		}

		// Bottom readability scrim + full hover veil + progress bar + crisp border.
		ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).PassThrough().Draw(func(pt *ui.Painter, r ui.Rect) {
			if hoverAlpha > 0.01 {
				pt.Fill(r, ui.RGBA(12, 13, 18, 0.46*hoverAlpha), rad)
			}
			pt.FillGradient(r, ui.LinearGradient{
				Angle: 180,
				Stops: []ui.GradientStop{
					{Color: ui.RGBA(0, 0, 0, 0.14*hoverAlpha), At: 0},
					{Color: ui.RGBA(0, 0, 0, 0.0), At: 0.40},
					{Color: ui.RGBA(0, 0, 0, 0.72), At: 1},
				},
			}, rad)
			if progress > 0 {
				pt.Fill(ui.Rect{X: r.X, Y: r.Y + r.H - 3, W: r.W * progress, H: 3}, white.Alpha(0.88), 1.5)
			}
			pt.Stroke(r, white.Alpha(0.16), rad, 1)
		})

		// Top-right hover actions: switch to bar, open main window, close.
		if hoverAlpha > 0.05 {
			ui.Row(c.Key("mini-card-top")).Absolute().Top(8).Right(8).Gap(4).Opacity(hoverAlpha).Children(func() {
				if miniOverlayBtn(c.Key("card-to-bar"), iconShrink, "切换为经典横条").Clicked() {
					a.setMiniStyle(miniStyleBar)
				}
				if miniOverlayBtn(c.Key("card-open"), iconExpand, "打开 Sonora 主窗口").Clicked() {
					a.showMain()
				}
				if miniOverlayBtn(c.Key("card-close"), iconX, "关闭迷你播放器").Clicked() {
					a.setMiniPlayerEnabled(false)
				}
			})
		}

		// Center controls appear smoothly on hover.
		if hoverAlpha > 0.05 {
			ui.Box(c.Key("mini-card-controls")).Absolute().Top(0).Left(0).Right(0).Bottom(34).Center().Opacity(hoverAlpha).Children(func() {
				a.miniTransport(c, white, white.Alpha(0.70), tint.Mix(white, 0.35), true)
			})
		}

		// Track title & artist anchored cleanly along the bottom.
		ui.Column(c).Absolute().Left(12).Right(12).Bottom(10).Gap(1).PassThrough().Children(func() {
			title := "暂未播放"
			artist := appName
			if cur != nil {
				title = cmp.Or(cur.Name, "未知歌曲")
				artist = cmp.Or(cur.ArtistNames(), "未知歌手")
			}
			ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(white).SingleLine()
			ui.Text(c, artist).FontSize(11).TextColor(white.Alpha(0.76)).SingleLine()
		})
	})
}

func boolFloat(v bool) float32 {
	if v {
		return 1
	}
	return 0
}

func miniCornerBtn(c *ui.Context, icon *ui.SVG, col ui.Color, tip string) ui.Element {
	b := ui.ButtonBase(c).Size(18, 18).Center().Radius(999).TextColor(col).Label(tip).Tooltip(tip)
	if b.Hovered() {
		b.Background(ui.RGBA(128, 128, 128, 0.18))
	}
	b.Children(func() { ui.Icon(c, icon).Size(10, 10) })
	return b
}

func miniOverlayBtn(c *ui.Context, icon *ui.SVG, tip string) ui.Element {
	white := ui.RGB(255, 255, 255)
	b := ui.ButtonBase(c).Size(24, 24).Center().Radius(999).TextColor(white).Label(tip).Tooltip(tip)
	bg := ui.RGBA(0, 0, 0, 0.42)
	if b.Hovered() {
		bg = ui.RGBA(0, 0, 0, 0.66)
	}
	b.Background(bg)
	b.Children(func() { ui.Icon(c, icon).Size(12, 12) })
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
				bg = ui.RGBA(255, 255, 255, 0.20)
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
		gap = 4
	}
	ui.Row(c).AlignItems(ui.Center).Gap(gap).Shrink(0).Children(func() {
		if btn("mini-shuffle", sicon, 13, 28, shuffleCol, stip).Clicked() {
			a.cycleShuffle()
		}
		if btn("mini-prev", iconPrev, 15, 30, color, "上一首").Clicked() {
			pl.Prev()
		}
		playBox := float32(34)
		playIconSize := float32(16)
		if cardMode {
			playBox = 40
			playIconSize = 18
		}
		pb := ui.ButtonBase(c.Key("mini-play")).Size(playBox, playBox).Center().Radius(999).Label("播放/暂停").TextColor(color)
		playBG := ui.RGBA(128, 128, 128, 0.14)
		if cardMode {
			playBG = ui.RGBA(255, 255, 255, 0.24)
		}
		if pb.Hovered() {
			if cardMode {
				playBG = ui.RGBA(255, 255, 255, 0.36)
			} else {
				playBG = ui.RGBA(128, 128, 128, 0.24)
			}
		}
		pb.Background(playBG)
		pose := ui.Pose{}
		if pb.Pressed() {
			pose.Scale = 0.90
		}
		pb.AnimatePose(pose, ui.Snappy)
		pb.Children(func() { playPause(c, pb, pl.Playing(), playIconSize) })
		if pb.Clicked() {
			pl.TogglePlay()
		}
		if btn("mini-next", iconNext, 15, 30, color, "下一首").Clicked() {
			pl.Next()
		}
		if btn("mini-repeat", ricon, 13, 28, repeatCol, rtip).Clicked() {
			a.cycleRepeat()
		}
	})
}
