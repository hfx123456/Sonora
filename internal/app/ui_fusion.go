package app

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
	"vinyl/internal/spotify"
)

const fusionFont = "Segoe UI Variable, Segoe UI, Microsoft YaHei UI, Yu Gothic UI"

const fusionDefaultFontLabel = "默认字体"
const fusionFollowFontLabel = "跟随歌词字体"

var fusionFontFamilies = ui.FontFamilies

func (a *app) fusionFont() string {
	if a.settings.FusionFont == "" {
		return fusionFont
	}
	return a.settings.FusionFont + ", " + fusionFont
}

func (a *app) fusionFontForText(text string) string {
	if a.settings.FusionChineseFont != "" && lyrics.Chinese([]lyrics.Line{{Text: text}}) {
		return a.settings.FusionChineseFont + ", Microsoft YaHei UI, Microsoft YaHei, " + fusionFont
	}
	return a.fusionFont()
}

func fusionChineseFontChoices(selected string) []string {
	choices := []string{fusionFollowFontLabel}
	families := fusionFontChoices(selected)[1:]
	preferred := []string{"Microsoft YaHei UI", "Microsoft YaHei", "MiSans", "DengXian", "SimSun", "SimHei", "KaiTi", "FangSong", "微软雅黑", "等线", "宋体", "黑体", "楷体", "仿宋"}
	added := map[string]bool{}
	for _, name := range preferred {
		for _, family := range families {
			if family == name && !added[family] {
				choices = append(choices, family)
				added[family] = true
			}
		}
	}
	for _, family := range families {
		if !added[family] {
			choices = append(choices, family)
			added[family] = true
		}
	}
	return choices
}

func fusionFontChoices(selected string) []string {
	choices := []string{fusionDefaultFontLabel}
	found := selected == ""
	for _, family := range fusionFontFamilies() {
		if family.Name == "" || family.Name == fusionDefaultFontLabel {
			continue
		}
		choices = append(choices, family.Name)
		if family.Name == selected {
			found = true
		}
	}
	if !found {
		choices = append(choices, selected)
	}
	return choices
}

var iconFusion = strokeIcon("<path d=\"M3 9v6M7 5v14M11 3v18M15 6v12M19 9v6M23 11v2\"/><path d=\"M11 14V8l5-1v6\"/>")
var iconFusionLock = strokeIcon("<rect x=\"5\" y=\"10\" width=\"14\" height=\"11\" rx=\"2\"/><path d=\"M8 10V6a4 4 0 0 1 8 0v4M12 14v3\"/>")

func (a *app) fusionButton(c *ui.Context) {
	color := a.pal.muted
	if a.settings.FusionPanel || a.settings.FusionMode != "" {
		color = ui.RGB(0, 174, 211)
	}
	iconButton(c.Key("fusion"), iconFusion, 18, color, "Fusion 双语歌词").Menu(a.fusionMenu)
}

func (a *app) fusionMenu(menu *ui.Menu) {
	if menu.Item("内部双语歌词面板").Checked(a.settings.FusionPanel).Chosen() {
		a.toggleFusionPanel()
	}
	if menu.Item("透明桌面双行歌词").Checked(a.settings.FusionMode == "desktop").Chosen() {
		if a.settings.FusionMode == "desktop" {
			a.setFusionMode("")
		} else {
			a.setFusionMode("desktop")
		}
	}
	if menu.Item("顶部胶囊双语歌词").Checked(a.settings.FusionMode == "capsule").Chosen() {
		if a.settings.FusionMode == "capsule" {
			a.setFusionMode("")
		} else {
			a.setFusionMode("capsule")
		}
	}
	if menu.Item("关闭桌面歌词").Chosen() {
		a.setFusionMode("")
	}
	menu.Separator()
	if menu.Item("歌词动态效果").Checked(!a.settings.FusionStatic).Chosen() {
		a.settings.FusionStatic = !a.settings.FusionStatic
		a.settings.save()
		a.redraw()
	}
	if menu.Item("鼠标移入词岛自动避让").Checked(!a.settings.FusionNoAvoid).Chosen() {
		a.settings.FusionNoAvoid = !a.settings.FusionNoAvoid
		a.settings.save()
		a.updateFusionHover()
	}
	if menu.Item("词岛贴顶居中").Chosen() {
		a.centerFusionIsland()
	}
	if menu.Item("显示中文译文").Checked(!a.settings.FusionHideTranslation).Chosen() {
		a.settings.FusionHideTranslation = !a.settings.FusionHideTranslation
		a.settings.save()
		a.resizeFusion()
		a.redraw()
	}
	if menu.Item("词岛悬浮胶囊形状").Checked(a.settings.FusionFloatingIsland).Chosen() {
		a.settings.FusionFloatingIsland = !a.settings.FusionFloatingIsland
		a.settings.save()
		a.pinFusionIsland()
		a.redraw()
	}
	if menu.Item("锁定桌面歌词（点击穿透）").Checked(a.settings.FusionLocked).Chosen() {
		a.toggleFusionLock()
	}
	if menu.Item("歌词字体设置…").Chosen() {
		a.openSettings()
	}
	if menu.Item("歌词字号放大").Chosen() {
		a.changeFusionScale(10)
	}
	if menu.Item("歌词字号缩小").Chosen() {
		a.changeFusionScale(-10)
	}
	if menu.Item("词岛加宽").Chosen() {
		a.changeFusionIslandWidth(40)
	}
	if menu.Item("词岛缩窄").Chosen() {
		a.changeFusionIslandWidth(-40)
	}
	if menu.Item("重置词岛大小").Chosen() {
		a.settings.FusionIslandWidth, a.settings.FusionScale = 0, 100
		a.settings.save()
		a.resizeFusion()
		a.redraw()
	}
	menu.Separator()
	if menu.Item("歌词提前 0.5 秒").Chosen() {
		a.nudgeFusion(500)
	}
	if menu.Item("歌词延后 0.5 秒").Chosen() {
		a.nudgeFusion(-500)
	}
	if menu.Item("重置歌词时间").Chosen() {
		a.settings.FusionOffset = 0
		a.settings.save()
		a.redraw()
	}
	menu.Separator()
	idle := a.pl.Current() == nil
	if menu.Item("重新查找双语歌词").Disabled(idle).Chosen() {
		a.ensureFusion(true)
	}
	if menu.Item("载入本地双语歌词…").Disabled(idle).Chosen() {
		a.importFusion()
	}
}

func (a *app) fusionPanel(c *ui.Context) {
	a.fusionTick(c, false)
	width, _ := c.Size()
	width = min(float32(420), width-sidebarWidth-80)
	var art *artwork
	if track := a.pl.Current(); track != nil {
		art = a.art.get(spotify.Sized(track.Album.PicURL, 600), true)
	}
	tint := ui.RGB(58, 66, 86)
	if art != nil && !art.failed {
		tint = art.tint.Mix(ui.RGB(30, 32, 40), 0.5)
	}
	ui.Box(c.Key("fusion-panel")).Absolute().Top(44).Right(0).Bottom(playerBarHeight).Width(width).Clip().Background(tint).TextColor(white).Font(a.fusionFont()).Children(func() {
		a.fusionBackdrop(c, art, tint)
		ui.Box(c).Absolute().Fill().Gradient(ui.RGBA(10, 12, 18, 0.20), ui.RGBA(10, 12, 18, 0.74), 180)
		ui.Column(c).Absolute().Fill().Children(func() {
			ui.Row(c).Padding(20, 20, 18, 24).Gap(12).AlignItems(ui.Center).Children(func() {
				if track := a.pl.Current(); track != nil {
					a.fusionCover(c, track.Album.PicURL, 44, 8)
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
						ui.Text(c, track.Name).FontSize(14).FontWeight(600).SingleLine()
						ui.Text(c, track.ArtistNames()).FontSize(12).TextColor(white.Alpha(0.55)).SingleLine()
					})
				} else {
					ui.Text(c, "歌词").FontSize(16).Grow(1)
				}
				iconButton(c.Key("options"), iconMore, 18, white.Alpha(0.65), "歌词选项").Menu(a.fusionMenu)
				if iconButton(c.Key("close"), iconX, 15, white.Alpha(0.65), "关闭双语歌词面板").Clicked() {
					a.toggleFusionPanel()
				}
			})
			if len(a.fusion.lines) == 0 || fusionInstrumentalNotice(a.fusion.lines) {
				line, detail := a.fusionCurrent()
				ui.Column(c).Grow(1).Center().Padding(30).Gap(14).Children(func() {
					ui.Icon(c, iconFusion).Size(32, 32).TextColor(white.Alpha(0.35))
					if line.Text == fusionWaitingNotes {
						a.fusionWaitingAnimation(c, 23, a.fusionPosition(), a.settings.FusionStatic || c.Preferences().ReduceMotion)
					} else {
						ui.Text(c, line.Text).FontSize(23).FontWeight(500).TextAlign(ui.Center)
					}
					if detail != "" {
						ui.Text(c, detail).FontSize(13).TextColor(white.Alpha(0.5)).TextAlign(ui.Center)
					}
				})
			} else {
				a.fusionLines(c)
			}
			ui.Row(c).Padding(12, 18, 14, 24).Gap(5).AlignItems(ui.Center).Children(func() {
				source, _, _ := strings.Cut(a.fusion.source, " · ")
				ui.Text(c, source).Grow(1).FontSize(10.5).TextColor(white.Alpha(0.4)).SingleLine()
				if iconButton(c.Key("earlier"), iconBack, 13, white.Alpha(0.55), "歌词提前半秒").Clicked() {
					a.nudgeFusion(500)
				}
				ui.Text(c, fmt.Sprintf("%+.1fs", float64(a.settings.FusionOffset)/1000)).FontSize(10.5).TextColor(white.Alpha(0.55))
				if iconButton(c.Key("later"), iconForward, 13, white.Alpha(0.55), "歌词延后半秒").Clicked() {
					a.nudgeFusion(-500)
				}
			})
		})
	})
}

func (a *app) fusionLines(c *ui.Context) {
	position := a.fusionPosition()
	current := lyrics.Index(a.fusion.lines, position)
	state := &a.fusion
	chinese := fusionChinese(state.lines)
	var active ui.Element
	scroll := ui.Scroll(c.Key("fusion-lines")).Grow(1).TrackScroll(&state.scroll)
	scroll.MaskLinearGradient(ui.LinearGradient{Angle: 180, Stops: []ui.GradientStop{{Color: ui.Transparent, At: 0}, {Color: white, At: 0.07}, {Color: white, At: 0.84}, {Color: ui.Transparent, At: 1}}})
	scroll.Children(func() {
		ui.Column(c).Padding(42, 24, 220, 24).Gap(27).Children(func() {
			for index, line := range state.lines {
				button := ui.ButtonBase(c.Key(index)).Column().AlignItems(ui.Start).Padding(0, 2).Gap(10).FocusRing(false).Label("跳转歌词：" + line.Text)
				color := white.Alpha(0.32)
				if index == current {
					color = white
					active = button
				} else if button.Hovered() {
					color = white.Alpha(0.6)
				}
				if !a.settings.FusionStatic && !c.Preferences().ReduceMotion {
					scale := float32(.96)
					if index == current {
						scale = 1.025
					}
					button.TransformOrigin(0, .5).AnimatePose(ui.Pose{Scale: scale}, ui.Animation{Spring: ui.SpringOptions{Duration: 430 * time.Millisecond, Bounce: .12}})
				}
				button.Children(func() {
					if index == current && len(line.Words) > 0 {
						a.fusionWords(c, line, position, 28, white.Alpha(0.4), white, !a.settings.FusionStatic)
					} else {
						ui.Text(c, line.Text).Font(a.fusionFontForText(line.Text)).FontSize(28).FontWeight(600).LineHeight(1.3).TextColor(color)
					}
					if translation := visibleFusionTranslation(line, chinese, a.settings.FusionHideTranslation); translation != "" {
						translationColor := white.Alpha(0.24)
						if index == current {
							translationColor = white.Alpha(0.62)
						}
						if index == current && !line.Unsynced {
							a.fusionWords(c, fusionTranslationLine(line, translation), position, 16, white.Alpha(.30), white.Alpha(.86), !a.settings.FusionStatic).FontWeight(400)
						} else {
							ui.Text(c, translation).Font(a.fusionFontForText(translation)).FontSize(16).FontWeight(400).LineHeight(1.4).TextColor(translationColor)
						}
					}
				})
				if button.Clicked() && !line.Unsynced {
					a.pl.Seek(max(0, line.At-time.Duration(a.settings.FusionOffset)*time.Millisecond))
					a.redraw()
				}
			}
		})
	})
	if current != state.at && active.Valid() {
		bounds, lineBounds := scroll.Bounds(), active.LayoutBounds()
		if bounds.H > 0 && lineBounds.H > 0 {
			state.at = current
			target := max(0, lineBounds.Y-bounds.Y+state.scroll.Y-bounds.H*0.32)
			state.scroll.ScrollTo(0, min(target, state.scroll.MaxY), ui.Animation{Duration: 420 * time.Millisecond})
		}
	}
}

func (a *app) fusionWords(c *ui.Context, line lyrics.Line, position time.Duration, size float32, dim, lit ui.Color, animate bool) ui.Element {
	var spans []ui.Span
	chinese := lyrics.Chinese([]lyrics.Line{line})
	for _, word := range line.Words {
		progress, pulse := fusionWordMotion(word, position)
		if !animate || chinese || c.Preferences().ReduceMotion {
			pulse = 0
		}
		spans = append(spans, ui.Span{Text: word.Text, Color: dim.Mix(lit, progress), BaselineShift: size * .11 * pulse, Shadow: ui.NewTextShadow(0, 0, size*.18*pulse, lit.Alpha(.22*pulse))})
	}
	return ui.RichText(c, spans...).Font(a.fusionFontForText(line.Text)).FontSize(size).FontWeight(600).LineHeight(1.35)
}

func (a *app) fusionCover(c *ui.Context, url string, size, radius float32) {
	ui.Box(c).Size(size, size).Shrink(0).Radius(radius).Clip().Background(white.Alpha(0.1)).Center().Children(func() {
		if art := a.art.get(spotify.Sized(url, 160), false); bitmap(art) != nil {
			artImage(c, bitmap(art), radius, 1)
		} else {
			ui.Icon(c, iconNote).Size(size*0.4, size*0.4).TextColor(white.Alpha(0.35))
		}
	})
}

func (a *app) keepFusionPosition() {
	if a.fusion.win == nil || a.fusion.mode == "" {
		return
	}
	if a.settings.FusionPositions == nil {
		a.settings.FusionPositions = map[string][2]int{}
	}
	bounds := a.fusion.win.Bounds()
	a.settings.FusionPositions[a.fusion.mode] = [2]int{bounds.X, bounds.Y}
}

func (a *app) fusionWindowSize() (int, int) {
	scale := a.fusionScale()
	width, height := float32(1040), float32(156)
	if a.settings.FusionMode == "capsule" {
		width, height = float32(a.fusionIslandWidth()), 84
		_, detail := a.fusionCurrent()
		secondRow := detail != ""
		chinese := fusionChinese(a.fusion.lines)
		for _, line := range a.fusion.lines {
			if visibleFusionTranslation(line, chinese, a.settings.FusionHideTranslation) != "" {
				secondRow = true
				break
			}
		}
		if !secondRow {
			height = 50
		}
	}
	return int(width * scale), int(height * scale)
}

func (a *app) resizeFusion() {
	if a.fusion.win == nil || a.settings.FusionMode == "" {
		return
	}
	width, height := a.fusionWindowSize()
	bounds := a.fusion.win.Bounds()
	display := mygo.Screen.PrimaryDisplay()
	width = min(width, display.WorkArea.Width-32)
	if bounds.Width == width && bounds.Height == height {
		return
	}
	bounds.X += (bounds.Width - width) / 2
	bounds.Width, bounds.Height = width, height
	a.fusion.win.SetBounds(bounds)
	a.pinFusionIsland()
}

func (a *app) setFusionMode(mode string) {
	if mode != "desktop" && mode != "capsule" {
		mode = ""
	}
	a.keepFusionPosition()
	a.fusion.hoverGeneration++
	a.settings.FusionMode = mode
	if a.win != nil {
		if mode == "" {
			if a.fusion.win != nil {
				a.fusion.win.Hide()
			}
		} else {
			display := mygo.Screen.PrimaryDisplay()
			width, height := a.fusionWindowSize()
			width = min(width, display.WorkArea.Width-32)
			x, y := display.WorkArea.X+(display.WorkArea.Width-width)/2, display.WorkArea.Y+display.WorkArea.Height-height-42
			if mode == "capsule" {
				x, y = display.Bounds.X+(display.Bounds.Width-width)/2, display.Bounds.Y
			}
			if saved, ok := a.settings.FusionPositions[mode]; ok {
				for _, screen := range mygo.Screen.Displays() {
					area := screen.WorkArea
					if saved[0] >= area.X && saved[1] >= area.Y && saved[0]+width <= area.X+area.Width && saved[1]+height <= area.Y+area.Height {
						x, y = saved[0], saved[1]
						break
					}
				}
			}
			a.fusion.mode = mode
			a.fusion.pauseHidden = false
			a.fusion.manualShow = !a.pl.Playing()
			if a.fusion.win == nil {
				a.fusion.win = mygo.NewWindow(mygo.WindowOptions{Title: appName + " 歌词", Hidden: true, Frameless: true, Transparent: true, DisableShadow: true, DisableResize: true, AlwaysOnTop: true, SkipTaskbar: true, Width: width, Height: height, X: x, Y: y, Content: ui.View(a.fusionOverlay)})
				a.fusion.win.OnClose(func(event *mygo.CloseEvent) {
					if !a.quitting {
						event.PreventDefault()
						a.setFusionMode("")
					}
				})
				a.fusion.win.OnMove(a.pinFusionIsland)
				mygo.Screen.OnDisplaysChanged(func() { a.resizeFusion(); a.pinFusionIsland() })
			}
			a.fusion.win.SetBounds(mygo.Rectangle{X: x, Y: y, Width: width, Height: height})
			a.pinFusionIsland()
			a.fusion.win.SetOpacity(1)
			a.fusion.win.SetIgnoreMouseEvents(a.settings.FusionLocked)
			if !a.fusion.captureOnly {
				a.fusion.win.ShowInactive()
			}
		}
	}
	if mode == "" {
		a.fusion.mode = ""
		a.fusion.pauseHidden = false
		a.fusion.manualShow = false
	}
	a.settings.save()
	a.ensureFusion(false)
	a.syncFusionVisibility()
	if mode != "" && !a.fusion.pauseHidden {
		a.startFusionHover()
	}
	a.redraw()
}

func (a *app) fusionOverlay(c *ui.Context) {
	theme := *c.Theme()
	theme.Font = a.fusionFont()
	c.SetTheme(&theme)
	c.Root().Background(ui.Transparent)
	capsule := a.settings.FusionMode == "capsule"
	if a.fusion.win != nil && capsule && a.fusion.hover.opacity < .01 {
		return
	}
	a.fusionTick(c, true)
	line, translation := a.fusionCurrent()
	if capsule {
		a.resizeFusion()
	}
	scale := a.fusionScale()
	root := ui.Box(c.Key("floating-lyrics")).Fill().Font(a.fusionFont())
	if !capsule {
		root.DragWindow().ContextMenu(a.fusionMenu)
	}
	root.Children(func() {
		if capsule {
			a.fusionIsland(c, line, translation, scale)
			return
		}
		if root.Hovered() {
			root.Background(ui.RGBA(16, 16, 18, 0.36)).Radius(12)
		}
		ui.Column(c).Fill().Center().Padding(24*scale, 28*scale, 8*scale, 28*scale).Children(func() { a.fusionAnimatedCaption(c, line, translation, 38*scale, 23*scale, true, 0) })
		if root.Hovered() {
			a.fusionOverlayControls(c, false)
		}
	})
}

type fusionLineMeasure struct {
	text, translation   string
	fontOrig, fontTrans string
	origSize, transSize float32
	width               float32
}

func (a *app) fusionMeasureWidth(line lyrics.Line, translation string, origSize, transSize float32) float32 {
	if line.Text == fusionWaitingNotes && translation == "" {
		return 96 * (origSize / 23)
	}
	fontOrig := a.fusionFontForText(line.Text)
	fontTrans := ""
	if translation != "" {
		fontTrans = a.fusionFontForText(translation)
	}
	m := &a.fusion.measure
	if m.text == line.Text && m.translation == translation && m.fontOrig == fontOrig && m.fontTrans == fontTrans && m.origSize == origSize && m.transSize == transSize && m.width > 0 {
		return m.width
	}
	w, _ := ui.ShapeText(line.Text, ui.Font{Family: fontOrig, Size: origSize, Weight: 400}, 0).Size()
	if translation != "" {
		tw, _ := ui.ShapeText(translation, ui.Font{Family: fontTrans, Size: transSize, Weight: 400}, 0).Size()
		w = max(w, tw)
	}
	*m = fusionLineMeasure{
		text:        line.Text,
		translation: translation,
		fontOrig:    fontOrig,
		fontTrans:   fontTrans,
		origSize:    origSize,
		transSize:   transSize,
		width:       w,
	}
	return w
}

func (a *app) fusionIslandSizing(line lyrics.Line, translation string, scale, coverMaxH, targetCapW float32) (targetPillW, minPillW, fitTextW float32) {
	// Horizontal chrome inside the pill: 16*scale padding each side, emblem, and cover art.
	chromeW := 32*scale + 26*scale + 12*scale
	if a.pl.Current() != nil {
		coverSize := min(32*scale, coverMaxH)
		chromeW += 12*scale + coverSize
	}
	minPillW = min(targetCapW, chromeW+84*scale)
	textW := a.fusionMeasureWidth(line, translation, 23*scale, 14*scale)
	targetPillW = min(targetCapW, max(minPillW, chromeW+textW+22*scale))
	fitTextW = max(1, targetCapW-chromeW-10*scale)
	return targetPillW, minPillW, fitTextW
}

func (a *app) fusionIsland(c *ui.Context, line lyrics.Line, translation string, scale float32) {
	winW, winH := c.Size()
	inset := 20 * scale
	maxPillClampW := max(160*scale, winW-inset*2)
	targetCapW := max(160*scale, maxPillClampW-18*scale)
	maxPillH := max(40*scale, winH-2*scale)
	targetPillH := min(maxPillH, 46*scale)
	if translation != "" {
		targetPillH = min(maxPillH, 76*scale)
	}
	coverMaxH := max(24*scale, targetPillH-18*scale)
	targetPillW, minPillW, fitTextW := a.fusionIslandSizing(line, translation, scale, coverMaxH, targetCapW)

	still := a.settings.FusionStatic || c.Preferences().ReduceMotion
	a.fusion.caption.update(fusionTrackKey(a.pl.Current())+a.settings.FusionMode, line, translation, a.fusionPosition(), c.Now())
	pulse := a.fusion.caption.pulse(c.Now(), still)

	stage := ui.Box(c.Key("island-stage")).Absolute().Fill()
	var pillW, pillH float32
	if still {
		pillW = stage.Jump("pill-w", targetPillW)
		pillH = stage.Jump("pill-h", targetPillH)
	} else {
		spring := ui.Animation{Spring: ui.SpringOptions{Duration: 520 * time.Millisecond, Bounce: 0.40}}
		pillW = stage.AnimateTo("pill-w", targetPillW, spring) + 10*scale*pulse
		pillH = stage.AnimateTo("pill-h", targetPillH, spring) + 2.5*scale*pulse
		pillW = max(max(120*scale, minPillW-24*scale), min(maxPillClampW, pillW))
		pillH = max(max(36*scale, targetPillH-8*scale), min(maxPillH, pillH))
	}
	a.fusion.capsuleW, a.fusion.capsuleH = pillW, pillH
	pillLeft := (winW - pillW) / 2

	stage.Draw(func(painter *ui.Painter, bounds ui.Rect) {
		if a.settings.FusionFloatingIsland {
			painter.Fill(ui.Rect{X: bounds.X + (bounds.W-pillW)/2, Y: bounds.Y + 2*scale, W: pillW, H: pillH - 2*scale}, ui.RGB(0, 0, 0), (pillH-2*scale)/2)
		} else {
			painter.FillPath(shape(bounds.X+bounds.W/2, pillW, pillH, 16*scale, min(38*scale, pillH*0.5)), ui.RGB(0, 0, 0))
		}
	})
	stage.Children(func() {
		pill := ui.Row(c.Key("island-pill")).Absolute().Top(0).Left(pillLeft).Width(pillW).Height(pillH).
			Padding(2*scale, 16*scale, 6*scale, 16*scale).Gap(12 * scale).AlignItems(ui.Center).
			DragWindow().ContextMenu(a.fusionMenu)
		pill.Children(func() {
			a.fusionEmblem(c, 26*scale)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2 * scale).Justify(ui.Center).Children(func() {
				a.fusionAnimatedCaption(c, line, translation, 23*scale, 14*scale, false, fitTextW)
			})
			if track := a.pl.Current(); track != nil {
				size := min(32*scale, pillH-16*scale)
				a.fusionCover(c, track.Album.PicURL, size, 7*scale)
			}
			if pill.Hovered() {
				if iconButton(c.Key("close-float"), iconX, 11, white.Alpha(0.65), "关闭桌面歌词").
					Absolute().Right(5*scale).Top(2).Size(20, 20).Clicked() {
					a.setFusionMode("")
				}
			}
		})
	})
}

func (a *app) fusionEmblem(c *ui.Context, size float32) {
	ui.Box(c).Size(size, size).Shrink(0).Draw(func(painter *ui.Painter, bounds ui.Rect) {
		green := ui.RGB(0, 186, 111)
		if track := a.pl.Current(); track != nil {
			if art := a.art.get(spotify.Sized(track.Album.PicURL, 160), false); art != nil && !art.failed {
				green = art.tint.Mix(white, 0.15)
			}
		}
		moving := a.pl.Playing() && !a.settings.FusionStatic && !c.Preferences().ReduceMotion
		unit := bounds.W / 32
		for index, bar := range []struct{ x, y, w, h, alpha float32 }{{3, 9, 5, 16, .55}, {8, 4, 5, 26, .8}, {13, 1, 5, 31, 1}, {18, 7, 5, 21, .75}, {23, 11, 5, 13, .5}} {
			if moving {
				factor := float32(.86 + .14*math.Sin(a.fusionPosition().Seconds()*5+float64(index)*1.3))
				bar.y += (bar.h - bar.h*factor) / 2
				bar.h *= factor
			}
			painter.Fill(ui.Rect{X: bounds.X + bar.x*unit, Y: bounds.Y + bar.y*unit, W: bar.w * unit, H: bar.h * unit}, green.Alpha(bar.alpha), 2*unit)
		}
	})
}

func (a *app) fusionOverlayControls(c *ui.Context, island bool) {
	if island {
		if iconButton(c.Key("close-float"), iconX, 11, white.Alpha(0.65), "关闭桌面歌词").Absolute().Right(23*a.fusionScale()).Top(2).Size(20, 20).Clicked() {
			a.setFusionMode("")
		}
		return
	}
	ui.Row(c).Absolute().Top(0).Left(16).Right(10).Height(34).Gap(3).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, iconFusion).Size(18, 18).TextColor(white.Alpha(0.65))
		name := appName
		if track := a.pl.Current(); track != nil {
			name = track.Name + " · " + track.ArtistNames()
		}
		ui.Text(c, name).FontSize(12).TextColor(white.Alpha(0.75)).Grow(1).MinWidth(0).SingleLine()
		if iconButton(c.Key("prev"), iconPrev, 13, white.Alpha(0.8), "上一首").Clicked() {
			a.pl.Prev()
		}
		play, caption := iconPlay, "播放"
		if a.pl.Playing() {
			play, caption = iconPause, "暂停"
		}
		if iconButton(c.Key("play"), play, 13, white.Alpha(0.8), caption).Clicked() {
			a.pl.TogglePlay()
		}
		if iconButton(c.Key("next"), iconNext, 13, white.Alpha(0.8), "下一首").Clicked() {
			a.pl.Next()
		}
		if iconButton(c.Key("lock"), iconFusionLock, 13, white.Alpha(0.8), "锁定桌面歌词").Clicked() {
			a.toggleFusionLock()
		}
		if iconButton(c.Key("close-float"), iconX, 14, white.Alpha(0.8), "关闭桌面歌词").Clicked() {
			a.setFusionMode("")
		}
	})
}

func (a *app) fusionSingleLine(c *ui.Context, line lyrics.Line, position time.Duration, size float32, lit, dim ui.Color, outline, animate bool, fitWidth float32) {
	box := ui.Box(c).Height(size * 1.5).AlignSelf(ui.Stretch)
	cache := ui.Local(box, "lyric-layout", func() fusionLineLayout { return fusionLineLayout{} })
	box.Draw(func(painter *ui.Painter, bounds ui.Rect) {
		maxWidth := max(1, bounds.W-10)
		if fitWidth > 0 {
			maxWidth = fitWidth
		}
		layout := cache.shape(line.Text, a.fusionFontForText(line.Text), size, maxWidth)
		width, height := layout.Size()
		origin, y := bounds.X+(bounds.W-width)/2, bounds.Y+(bounds.H-height)/2
		words := line.Words
		if len(words) == 0 && !line.Unsynced && !lyrics.IsCredit(line.Text) && line.End > line.At {
			words = lyrics.SpreadWords(line.Text, line.At, line.End)
		}
		painter.Clip(bounds, 0, func() {
			if outline {
				dim = ui.RGB(180, 184, 190)
				for _, offset := range [][2]float32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}, {-.7, -.7}, {.7, -.7}, {-.7, .7}, {.7, .7}} {
					painter.TextLayout(layout, origin+offset[0], y+offset[1], ui.RGBA(0, 0, 0, .8))
				}
			}
			if len(words) == 0 {
				painter.TextLayout(layout, origin, y, lit)
				return
			}
			painter.TextLayout(layout, origin, y, dim)
			before, sweep := float32(0), float32(0)
			offsets := cache.wordOffsets(words)
			for index, word := range words {
				end := layout.Caret(offsets[index]).X
				progress, _ := fusionWordMotion(word, position)
				if progress > 0 {
					sweep = max(sweep, before+(end-before)*progress)
				}
				before = end
			}
			if sweep > 0 {
				painter.Clip(ui.Rect{X: origin - 1, Y: bounds.Y, W: min(width, sweep) + 2, H: bounds.H}, 0, func() {
					painter.TextLayout(layout, origin, y, lit)
				})
			}
		})
	})
}

type fusionLineLayout struct {
	text, family string
	size, width  float32
	layout       *ui.TextLayout
	wordTexts    []string
	offsets      []int
}

func (cache *fusionLineLayout) wordOffsets(words []lyrics.Word) []int {
	same := len(cache.wordTexts) == len(words)
	if same {
		for index, word := range words {
			if cache.wordTexts[index] != word.Text {
				same = false
				break
			}
		}
	}
	if !same {
		cache.wordTexts = make([]string, len(words))
		for index, word := range words {
			cache.wordTexts[index] = word.Text
		}
		cache.offsets = fusionWordOffsets(cache.text, words)
	}
	return cache.offsets
}

func (cache *fusionLineLayout) shape(text, family string, size, width float32) *ui.TextLayout {
	if cache.layout != nil && cache.text == text && cache.family == family && cache.size == size && cache.width == width {
		return cache.layout
	}
	font := ui.Font{Family: family, Size: size, Weight: 400}
	layout := ui.ShapeText(text, font, 0)
	measured, _ := layout.Size()
	if measured > width {
		font.Size = max(13, size*width/measured)
		layout = ui.ShapeText(text, font, 0)
	}
	*cache = fusionLineLayout{text: text, family: family, size: size, width: width, layout: layout}
	return layout
}
