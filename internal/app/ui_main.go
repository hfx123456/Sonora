package app

import (
	"log"
	"math/rand/v2"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

const sidebarWidth = 232

var appFont = func() string {
	if runtime.GOOS == "darwin" {
		return "system-ui"
	}
	// Han characters in YaHei: left to the system, they fall back by the
	// language, and outside Chinese ones a word mixes a Japanese font's
	// with YaHei's, of other weights.
	return "MiSans, system-ui, Microsoft YaHei UI"
}()

func (a *app) view(c *ui.Context) {
	t := *c.Theme()
	a.pal = paletteFor(&t)
	p := a.pal
	t.Accent, t.AccentText = p.accent, p.onAccent
	t.AccentHover = p.accent.Mix(ui.RGB(255, 255, 255), 0.14)
	t.AccentPressed = p.accent.Mix(ui.RGB(0, 0, 0), 0.14)
	t.Focus = p.accent.Alpha(0.55)
	t.Selection = p.accent.Alpha(0.3)
	t.Font = appFont
	c.SetTheme(&t)

	a.recSeen = false
	defer func() {
		if a.recording != "" && !a.recSeen {
			a.stopRecording()
		}
	}()
	a.shortcuts(c)
	a.finishSongClick(c)
	a.ensureFusion(false)

	if c.Vibrancy() {
		c.Root().Background(ui.Transparent)
	}
	root := ui.Box(c).Fill()
	if !c.Focused(a.searchBox) && !c.Focused(a.localSearchBox) && !c.Focused(a.playlistSearchBox) &&
		!a.naming.open && !a.cloudFormOpen && !a.proxyTyping && a.recording == "" {
		// Space plays and pauses wherever the focus is, as in Music. On
		// the container around everything, it comes before a button the
		// pointer focused, which would take Space as a click; on the
		// window, it would come after.
		root.OnShortcut(0, ui.KeySpace, a.pl.TogglePlay)
		// And where nothing has the focus, as once Now Playing covers the
		// button that had it.
		c.OnShortcut(0, ui.KeySpace, a.pl.TogglePlay)
	}
	npOpen, npSettled := a.npState(c)
	a.syncFullScreen(npOpen)
	if a.glassEnabled() && !(npOpen && npSettled) {
		a.unifiedBackdrop(c, root)
	}
	root.Children(func() {
		page := ui.Row(c).Fill().AlignItems(ui.Stretch)
		page.Children(func() {
			if npOpen && npSettled {
				// Covered by Now Playing: not built, which would cost each
				// of its frames as much again as the stage itself. Where
				// it is scrolled is the app's (pageScroll, navScroll,
				// trackList), so it shows again as it was.
				return
			}
			side := ui.Column(c).Width(sidebarWidth)
			if !c.Vibrancy() && !a.glassEnabled() {
				side.Background(p.sidebar)
			}
			side.Children(func() { a.sidebar(c) })
			main := ui.Box(c.Key("main")).Grow(1)
			if !a.glassEnabled() {
				main.Background(p.bg).BorderWidth(0, 0, 0, 1).BorderColor(p.hairline)
			}
			// Keyed, so that a popover of the sidebar opening or closing
			// keeps what the page and its bars hold, their edges' fades.
			main.Children(func() {
				a.content(c)
				// The bars float over the page, which scrolls under them:
				// positioned, they paint over it.
				a.pageTop(c)
				a.playerBar(c)
			})
		})
		a.nowPlayingLayer(c, npOpen, npSettled)
		if a.settings.FusionPanel {
			a.fusionPanel(c)
		}
		a.playlistNameDialog(c)
		a.deletePlaylistDialog(c)
	})
	if a.toast != "" {
		c.Toast(a.toast)
		a.toast = ""
	}
	if a.lyricsToast != "" {
		c.AddToast(ui.Toast{ID: "lyrics-nudge", Title: a.lyricsToast})
		a.lyricsToast = ""
	}
	if r := a.next; r != nil {
		a.next = nil
		a.open(*r)
		c.AnimationFrame()
	}
	a.syncWindows()
}

func (a *app) shortcuts(c *ui.Context) {
	if a.recording != "" {
		return
	}
	// The side buttons of a mouse, the keys of keyboards that have them
	// and swipes between pages, and the keys of Music.
	c.OnShortcut(0, ui.KeyBack, a.back)
	c.OnShortcut(0, ui.KeyForward, a.goForward)
	c.OnShortcut(ui.Cmd, ui.KeyBracketLeft, a.back)
	c.OnShortcut(ui.Cmd, ui.KeyBracketRight, a.goForward)
	c.OnShortcut(ui.Cmd, ui.KeyF, func() {
		a.nowPlaying = false
		if a.route.page == pagePlaylist {
			a.playlistSearchBox.Focus(c)
			return
		}
		if a.route.page == pageLocal {
			a.localSearchBox.Focus(c)
			return
		}
		a.searchBox.Focus(c)
	})
	if a.nowPlaying {
		c.OnShortcut(0, ui.KeyEscape, a.npEscape)
		if a.pl.ShowsVideo() && a.mvPage() != nil {
			c.OnShortcut(0, ui.KeyF, a.toggleMVFullScreen)
			c.OnShortcut(0, ui.KeyBracketLeft, func() { a.nudgeLyrics(-lyricsNudge) })
			c.OnShortcut(0, ui.KeyBracketRight, func() { a.nudgeLyrics(lyricsNudge) })
		}
	}
}

func (a *app) pushHistory(r route) {
	a.history = append(a.history, r)
	const maxHistory = 50
	if len(a.history) > maxHistory {
		a.history = slices.Clone(a.history[len(a.history)-maxHistory:])
	}
}

func isTrackListPage(p pageKind) bool {
	switch p {
	case pagePlaylist, pageDaily, pageSearch, pageArtist, pageRecent, pageLocal:
		return true
	}
	return false
}

func (a *app) pruneSpots() {
	if len(a.spots) == 0 {
		return
	}
	keep := make(map[route]bool, len(a.history)+len(a.forward)+1)
	keep[a.route] = true
	for _, r := range a.history {
		keep[r] = true
	}
	for _, r := range a.forward {
		keep[r] = true
	}
	for r := range a.spots {
		if !keep[r] {
			delete(a.spots, r)
		}
	}
}

// go opens a page, remembering the one before for the back button. The
// page open already is shown too, from under Now Playing.
func (a *app) open(r route) {
	a.nowPlaying = false
	if r == a.route {
		return
	}
	a.pushHistory(a.route)
	a.forward = nil
	a.show(r)
	a.pruneSpots()
}

// pageSpot is where a page is scrolled.
type pageSpot struct {
	list   ui.ListState
	scroll ui.ScrollState
}

// leave keeps where the page is scrolled, for going back to it, and has the
// next start at its top.
func (a *app) leave() {
	// A hotkey left waiting for its keys will not see its button lose the
	// focus again: the page it sat on is not built once left. Leaving a
	// page gives the recording up, and the global shortcuts, unregistered
	// while the keys were waited for, come back.
	if a.recording != "" {
		a.stopRecording()
	}
	if a.route.page == pageSettings && a.proxyTyping {
		a.proxyTyping = false
		a.setProxy()
	}
	if a.trackListHandle == (ui.Handle{}) {
		a.trackListHandle = a.trackList.Handle
	}
	if a.spots == nil {
		a.spots = map[route]pageSpot{}
	}
	var listSpot ui.ListState
	if isTrackListPage(a.route.page) {
		listSpot = a.trackList
		listSpot.Handle = ui.Handle{}
	}
	a.spots[a.route] = pageSpot{listSpot, a.pageScroll}
	a.trackList, a.pageScroll = ui.ListState{Handle: a.trackListHandle}, ui.ScrollState{}
	a.startMarkW, a.startMarkTop, a.startMarkY = 0, 0, 0
}

// showAgain shows the page of r, one of the history, where it was left.
func (a *app) showAgain(r route) {
	a.show(r)
	if s, ok := a.spots[r]; ok {
		s.list.Handle = a.trackListHandle
		a.trackList, a.pageScroll = s.list, ui.ScrollState{Y: s.scroll.Y}
	}
}

// show shows the page of r.
func (a *app) show(r route) {
	a.leave()
	a.route = r
	switch r.page {
	case pageLocal:
		if !a.localLoaded {
			a.scanLocalMusic()
		}
	case pageSettings:
		// What opening did while the settings were a window of their own:
		// the proxy as saved, its error dropped, and the cache's size for
		// the page to show.
		a.proxyText, a.proxyErr, a.proxyTyping = a.settings.Proxy, "", false
		a.measureCache()
	case pagePlaylist:
		a.loadPlaylist(r.playlist, false)
	case pageArtist:
		a.loadArtist(r.artist)
	case pageRecent:
		a.loadRecent()
	case pageTrack:
		a.loadTrack(r.track)
	case pageBrowse:
		a.loadCategories()
	case pageCategory:
		a.loadCategory(r.category)
	case pageUser:
		a.loadUser(r.user)
	case pageSection:
		a.loadSection(r.section)
	}
}

// back goes to the page before, as the back button of a mouse does, or
// closes Now Playing, which covers the pages.
func (a *app) back() {
	if a.nowPlaying {
		a.nowPlaying = false
		return
	}
	if len(a.history) == 0 {
		return
	}
	a.forward = append(a.forward, a.route)
	r := a.history[len(a.history)-1]
	a.history = a.history[:len(a.history)-1]
	a.showAgain(r)
}

// goForward goes to the page back left, as the forward button of a mouse
// does.
func (a *app) goForward() {
	if len(a.forward) == 0 {
		return
	}
	a.history = append(a.history, a.route)
	r := a.forward[len(a.forward)-1]
	a.forward = a.forward[:len(a.forward)-1]
	a.nowPlaying = false
	a.showAgain(r)
}

func (a *app) sidebar(c *ui.Context) {
	// Under the window buttons where they sit over the sidebar, as the
	// traffic lights do; elsewhere, as close to the top as to the sides.
	top := float32(14)
	if c.TitleBar().Left > 0 {
		top = topBar(c) + 4
	}
	// Where nothing in it takes the press, the sidebar moves the window, as
	// the title bar it reaches up into would.
	ui.Column(c).Fill().Padding(top, 14, 14, 14).Gap(4).DragWindow().Children(func() {
		// The search field.
		field, s := a.searchField(c, "search", "clear-search", &a.searchBox, &a.query, "搜索", "搜索歌曲、歌手")
		field.Margin(0, 0, 14, 0)
		if s.submitted && strings.TrimSpace(a.query) != "" {
			a.search(strings.TrimSpace(a.query))
		}
		if s.cleared {
			a.searchBox.Focus(c)
		}

		nav := ui.Scroll(c.Key("nav")).TrackScroll(&a.navScroll).Grow(1).Gap(4)
		if a.glassEnabled() {
			nav.Margin(0, -8, 0, -6).Padding(0, 14, 0, 6)
		} else {
			nav.Margin(0, -6).Padding(0, 6)
		}
		nav.Children(func() {
			a.navItem(c, iconHome, "为你推荐", route{page: pageHome})
			a.navItem(c, iconDaily, "每日推荐", route{page: pageDaily})
			a.navItem(c, iconCharts, "排行榜", route{page: pageCharts})
			a.navItem(c, iconBrowse, "浏览", route{page: pageBrowse})
			a.navItem(c, iconRecent, "最近播放", route{page: pageRecent})
			a.navItem(c, iconDevice, "本地音乐", route{page: pageLocal})
			if a.searched != "" {
				a.navItem(c, iconSearch, "搜索结果", route{page: pageSearch})
			}
			a.myPlaylists(c, "我的歌单", a.mine, true)
			a.myPlaylists(c, "我的收藏", a.saved, false)
			a.followedArtists(c)
		})

		a.updateBadge(c)
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			a.accountRow(c)
			a.appearanceButton(c)
		})
	})
}

// appearanceButton toggles between dark and light mode from the sidebar footer.
func (a *app) appearanceButton(c *ui.Context) {
	p := a.pal
	icon, label, tip := iconMoon, "黑夜模式", "切换到黑夜模式"
	if p.dark {
		icon, label, tip = iconSun, "浅色模式", "切换到浅色模式"
	}
	b := ui.ButtonBase(c.Key("appearance-toggle")).Size(36, 36).Shrink(0).Radius(9).Center().FocusRing(false).Label(label).Tooltip(tip)
	bg := ui.Transparent
	fg := p.muted
	if b.Hovered() {
		bg = p.hover
		fg = p.text
	}
	b.Background(b.AnimateColor("bg", bg, ui.Animation{Duration: 120 * time.Millisecond}))
	b.Children(func() {
		ui.Icon(c, icon).Size(17, 17).TextColor(fg)
	})
	if b.Clicked() {
		a.toggleAppearance()
	}
}

func (a *app) navItem(c *ui.Context, icon *ui.SVG, label string, r route) {
	p := a.pal
	selected := a.route.page == r.page && !a.nowPlaying
	b := ui.ButtonBase(c.Key(label)).Height(34).PaddingX(10).Gap(10).Radius(10).AlignItems(ui.Center).Justify(ui.Start).FocusRing(false)
	a.styleSidebarItem(b, selected, b.Hovered())
	color := p.text
	if selected {
		color = p.accent
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(17, 17).TextColor(color)
		ui.Text(c, label).FontSize(13.5).FontWeight(500).TextColor(p.text)
	})
	if b.Clicked() {
		a.open(r)
	}
}

// playerBarHeight is the height of the player along the foot of the pages.
const playerBarHeight = 84

// pageGap is the room between the bar over a page and its content.
const pageGap = 12

// insets returns the room the bars over a page take at its top and its
// bottom: pages pad their content by it, and scroll under the bars.
func (a *app) insets(c *ui.Context) (top, bottom float32) {
	top = topBar(c)
	if runtime.GOOS == "darwin" {
		// The edge reaches a little below the traffic lights' strip.
		top += topEdgePad
	}
	return top, playerBarHeight
}

// topEdgePad is the room the edge over a page leaves below the strip, on
// macOS.
const topEdgePad = 6

// buttonsHeight is the height of the window buttons off macOS, which fill
// the edge over a page.
const buttonsHeight = topBarHeight + topEdgePad

// topBar returns the height of the strip along the top of the window, as
// tall as the window buttons and the room above and below them, which
// what sits in it centers on.
func topBar(c *ui.Context) float32 {
	if runtime.GOOS != "darwin" {
		// The whole edge, which the buttons fill: what sits in the strip
		// centers on their glyphs.
		return buttonsHeight
	}
	return max(c.TitleBar().Height, topBarHeight)
}

// topBarHeight is the least height of the strip along the top.
const topBarHeight = 40

// startMark marks where a page's content starts, first in what scrolls:
// once it has moved up, content is under the bar at the top.
func (a *app) startMark(c *ui.Context) {
	b := ui.Box(c.Key("start-mark")).Height(0).Bounds()
	a.startMarkW = b.W
	if b.W > 0 {
		top, _ := a.insets(c)
		a.startMarkTop, a.startMarkY = top+pageGap, b.Y
		a.underTop = b.Y < top+pageGap-0.5
	}
}

// endMark marks where a page's content ends, last in what scrolls: while it
// is below the top of the player, content is under the player.
func (a *app) endMark(c *ui.Context) {
	b := ui.Box(c.Key("end-mark")).Height(0).Bounds()
	if b.W > 0 {
		_, h := c.Size()
		_, bottom := a.insets(c)
		a.underBottom = b.Y > h-bottom+0.5
	}
}

// edgeShown returns how much of a scroll edge shows over el: fully while
// content is under its bar, and none at rest, as AppKit shows its edges.
func edgeShown(el ui.Element, under bool) float32 {
	target := float32(0)
	if under {
		target = 1
	}
	return el.AnimateTo("shown", target, ui.Animation{Duration: 180 * time.Millisecond})
}

// pageTop is the strip over the top of a page, frosting what scrolls under
// it as macOS 26's hard scroll edge does: it drags the window, and holds
// the back button once there is a page to go back to.
// unifiedBackdrop paints one continuous window-wide canvas behind both the
// floating sidebar and the main content area so there is never a seam between them.
func (a *app) unifiedBackdrop(c *ui.Context, root ui.Element) {
	p := a.pal
	top, _ := a.insets(c)
	var washColor ui.Color
	var washStrength float32
	switch a.route.page {
	case pageHome, pageCharts, pageBrowse, pageCategory, pageRecent, pageSection, pageUser, pageSearch:
		if tint := a.pageTint(a.route.page); tint != (ui.Color{}) {
			washColor = root.AnimateColor("page-wash", tint, ui.Animation{Duration: 600 * time.Millisecond})
			washStrength = 0.22
			if p.dark {
				washStrength = 0.42
			}
			switch a.route.page {
			case pageRecent, pageSearch:
				if offset, visible := a.trackHeaderOffset(); visible {
					washStrength *= max(0, 1-offset/400)
				} else {
					washStrength = 0
				}
			default:
				washStrength *= max(0, 1-a.pageScroll.Y/400)
			}
		} else {
			washColor = p.accent
			if tr := a.pl.Current(); tr != nil {
				if art := a.art.get(spotify.Sized(tr.Album.PicURL, 600), true); art != nil && art.tint.A > 0 {
					washColor = art.tint
				}
			}
			washStrength = 0.08
			if p.dark {
				washStrength = 0.16
			}
		}
	case pagePlaylist:
		if pl := a.playlists[a.route.playlist]; pl != nil {
			washColor = ui.Hex("#5E46E8")
			if !pl.Liked() && pl.Cover() != "" {
				if art := a.art.get(spotify.Sized(pl.Cover(), 600), true); art != nil && art.tint.A > 0 {
					washColor = art.tint
				} else {
					washColor = p.accent
				}
			}
			washStrength = 0.16
			if p.dark {
				washStrength = 0.28
			}
			if offset, visible := a.trackHeaderOffset(); visible {
				hero := max(a.playlistHeaderHeights[pl.ID], playlistCoverSize+playlistHeaderGap)
				washStrength *= max(0, 1-offset/hero)
			} else {
				washStrength = 0
			}
		}
	default:
		washColor = p.accent
		if tr := a.pl.Current(); tr != nil {
			if art := a.art.get(spotify.Sized(tr.Album.PicURL, 600), true); art != nil && art.tint.A > 0 {
				washColor = art.tint
			}
		}
		washStrength = 0.08
		if p.dark {
			washStrength = 0.16
		}
	}
	root.Draw(func(dp *ui.Painter, r ui.Rect) {
		dp.Fill(r, p.bg, 0)
		if washColor.A > 0 && washStrength > 0.005 {
			washH := min(r.H, top+360)
			dp.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: washH}, ui.LinearGradient{
				Angle: 180,
				Stops: []ui.GradientStop{
					{Color: washColor.Alpha(washStrength), At: 0},
					{Color: washColor.Alpha(0), At: 1},
				},
			}, 0)
		}
	})
}

func (a *app) pageTop(c *ui.Context) {
	top, _ := a.insets(c)
	// The input strip is always present. Glass removes the scroll-edge
	// background, and an unscrolled page fades it away; neither should
	// remove the place where the pointer can move the window.
	ui.Box(c.Key("titlebar-drag")).Absolute().Top(0).Left(0).Right(0).Height(top).DragWindow().Children(func() {
		if !a.glassEnabled() {
			edge := ui.Box(c.Key("top-edge")).Absolute().Fill().PassThrough().
				Material(glass.ScrollEdge{Hard: true, Background: a.pal.bg})
			edge.Opacity(edgeShown(edge, a.underTop))
		}
	})
	a.titlebarMiniButton(c, false)
	if len(a.history) > 0 || len(a.forward) > 0 {
		if a.glassEnabled() {
			ui.Row(c).Absolute().Top(max(6, (topBar(c)-34)/2)).Left(20).Height(34).Gap(8).AlignItems(ui.Center).Children(func() {
				back := iconButton(c.Key("back"), iconBack, 16, a.pal.muted, "返回").Disabled(len(a.history) == 0).
					Material(a.liquidGlass(true, ui.Color{}))
				if !a.pal.dark {
					back.Shadow(0, 2, 8, -2, ui.RGBA(0, 0, 0, 0.08))
				}
				if len(a.history) == 0 {
					back.Opacity(0.35)
				}
				if back.Clicked() {
					a.back()
				}
				if len(a.forward) > 0 {
					fwd := iconButton(c.Key("forward"), iconForward, 16, a.pal.muted, "前进").
						Material(a.liquidGlass(true, ui.Color{}))
					if !a.pal.dark {
						fwd.Shadow(0, 2, 8, -2, ui.RGBA(0, 0, 0, 0.08))
					}
					if fwd.Clicked() {
						a.goForward()
					}
				}
			})
			return
		}
		ui.Row(c).Absolute().Top(0).Left(20).Height(topBar(c)).AlignItems(ui.Center).Children(func() {
			back := iconButton(c.Key("back"), iconBack, 18, a.pal.muted, "返回").Disabled(len(a.history) == 0)
			if len(a.history) == 0 {
				back.Opacity(0.35)
			}
			if back.Clicked() {
				a.back()
			}
			if len(a.forward) > 0 && iconButton(c.Key("forward"), iconForward, 18, a.pal.muted, "前进").Clicked() {
				a.goForward()
			}
		})
	}
}

func (a *app) content(c *ui.Context) {
	// Pages mark where their content is, as they scroll.
	a.underTop, a.underBottom = false, false
	// Signed out, the pages everyone's are still there, browsed as an
	// anonymous visitor: only a page that has nothing to show says to sign
	// in, and it says so itself.
	if a.profile == nil && !a.homeLoading && a.homeErr != "" && a.route.page == pageHome {
		a.signedOutPage(c)
		return
	}
	switch a.route.page {
	case pageLocal:
		a.localMusicPage(c)
	case pageHome:
		a.homePage(c)
	case pageDaily:
		a.dailyPage(c)
	case pageCharts:
		a.chartsPage(c)
	case pagePlaylist:
		a.playlistPage(c)
	case pageSearch:
		a.searchPage(c)
	case pageArtist:
		a.artistPage(c)
	case pageRecent:
		a.recentPage(c)
	case pageTrack:
		a.trackPage(c)
	case pageBrowse:
		a.browsePage(c)
	case pageCategory:
		a.categoryPage(c)
	case pageUser:
		a.userPage(c)
	case pageSection:
		a.sectionPage(c)
	case pageSettings:
		a.settingsPage(c)
	}
}

// centered shows a message, with a retry button if retry is set: what a
// page shows in place of what failed to load.
func (a *app) centered(c *ui.Context, err string, retry func()) {
	top, bottom := a.insets(c)
	ui.Column(c).Grow(1).Padding(top, 0, bottom, 0).Center().Gap(12).Children(func() {
		ui.Text(c, err).TextColor(a.pal.muted).FontSize(13)
		if retry != nil && ui.Button(c, "重试").Clicked() {
			retry()
		}
	})
}

// Loading what the pages show.

// loadHome refreshes the home feed, the daily mix and the charts in the
// background. The chips' feeds stay: one loaded before this ends would be gone
// with nothing to load it again, and its page would stay on the skeleton.
func (a *app) loadHome() {
	a.homeLoading, a.homeErr = true, ""
	gen := a.accountGen
	go func() {
		var (
			wg     sync.WaitGroup
			charts []spotify.Playlist
			err2   error
		)
		wg.Add(1)
		go func() {
			defer wg.Done()
			charts, err2 = spotify.Charts()
		}()
		home, err1 := homeFeed("")
		var daily []spotify.Track
		var dailyURI string
		dailyOK := err1 == nil
		if dailyOK && home.Daily != "" {
			dailyURI = home.Daily
			if pl, err := spotify.PlaylistDetail(home.Daily); err == nil {
				daily = pl.Tracks
			} else {
				dailyOK = false
			}
		}
		wg.Wait()
		a.post(func() {
			if a.accountGen != gen {
				return
			}
			a.homeLoading = false
			// What failed to load again shows as it did, as restoreHome
			// had it.
			if home != nil {
				a.homeSections, a.homeShortcuts = home.Sections, home.Shortcuts
				// The sections' pages load again as asked.
				a.sectionAll = nil
			}
			if err2 == nil {
				a.charts = charts
			}
			if dailyOK {
				a.daily = daily
				a.dailyURI = dailyURI
			}
			if err1 != nil && err2 != nil {
				log.Printf("home: %v; charts: %v", err1, err2)
				a.homeErr = "无法连接 Spotify，请检查网络"
			}
			a.keepHome()
		})
	}()
}

func (a *app) loadPlaylist(id string, thenPlay bool) {
	a.requestPlaylist(id, thenPlay, false)
}

func (a *app) playPlaylist(pl *spotify.Playlist) {
	if pl == nil {
		return
	}
	tracks := a.orderedPlaylistTracks(pl)
	a.playFrom(spotify.HomeItem{List: pl}, tracks, firstPlayable(tracks))
}

func (a *app) requestPlaylist(id string, thenPlay, refresh bool) {
	if pl := a.playlists[id]; pl != nil && !refresh {
		if thenPlay {
			a.playPlaylist(pl)
		}
		return
	}
	if a.loadingPl[id] {
		if thenPlay {
			if a.plThenPlay == nil {
				a.plThenPlay = map[string]bool{}
			}
			a.plThenPlay[id] = true
		}
		if refresh {
			if a.plRefreshAgain == nil {
				a.plRefreshAgain = map[string]bool{}
			}
			a.plRefreshAgain[id] = true
		}
		return
	}
	a.loadingPl[id] = true
	if thenPlay {
		if a.plThenPlay == nil {
			a.plThenPlay = map[string]bool{}
		}
		a.plThenPlay[id] = true
	}
	delete(a.plErr, id)
	asked := time.Now()
	user := ""
	if a.profile != nil {
		user = a.profile.UserID
	}
	root, generation, epoch, fetch := a.playlistCacheRoot, a.accountGen, accountEpoch(), playlistDetail
	showCached := a.playlists[id] == nil
	go func() {
		var cached *playlistSnapshot
		if showCached {
			cached = readPlaylistSnapshot(root, user, id)
		}
		if cached != nil {
			a.post(func() {
				if a.accountGen != generation || a.quitting {
					return
				}
				playNow := a.plThenPlay[id]
				delete(a.plThenPlay, id)
				a.receivePlaylist(id, cached.Playlist, nil, playNow, cached.Stored)
				a.loadingPl[id] = true
			})
		}
		pl, err := fetch(id)
		if err == nil && accountEpoch() == epoch {
			savePlaylistSnapshot(root, user, id, pl, asked)
		}
		a.post(func() {
			if a.accountGen != generation || a.quitting {
				return
			}
			if err != nil {
				log.Printf("playlist refresh: %s: %v", id, err)
			}
			playNow := a.plThenPlay[id]
			delete(a.plThenPlay, id)
			a.receivePlaylist(id, pl, err, playNow, asked)
			if a.plRefreshAgain[id] {
				delete(a.plRefreshAgain, id)
				a.requestPlaylist(id, false, true)
				return
			}
			a.playlistSync.complete(id, time.Now())
			a.startPlaylistSync()
		})
	}()
}

// receivePlaylist takes the playlist id as asked for at asked.
func (a *app) receivePlaylist(id string, pl *spotify.Playlist, err error, thenPlay bool, asked time.Time) {
	delete(a.loadingPl, id)
	if err != nil {
		if a.playlists[id] == nil {
			a.plErr[id] = "歌单加载失败"
		}
		return
	}
	a.playlists[id] = pl
	if id == spotify.LikedURI && a.profile != nil {
		// The liked songs as they are now: hearts went by the ones at sign
		// in, empty for songs liked elsewhere since, and for all if that
		// failed.
		ids := make([]string, len(pl.Tracks))
		for i, t := range pl.Tracks {
			ids[i] = t.ID
		}
		a.likedFrom(ids, asked)
	}
	if thenPlay {
		a.playPlaylist(pl)
	}
}

func firstPlayable(tracks []spotify.Track) int {
	for i := range tracks {
		if tracks[i].Playable() {
			return i
		}
	}
	return 0
}

func randomPlayable(tracks []spotify.Track) int {
	var playable []int
	for i := range tracks {
		if tracks[i].Playable() {
			playable = append(playable, i)
		}
	}
	if len(playable) == 0 {
		return 0
	}
	return playable[rand.IntN(len(playable))]
}

func (a *app) search(q string) {
	if a.route.page != pageSearch {
		a.pushHistory(a.route)
		a.forward = nil
	}
	a.leave()
	a.route = route{page: pageSearch}
	a.nowPlaying = false
	a.runSearch(q)
}

// runSearch loads the results of q, wherever the listener is.
func (a *app) runSearch(q string) {
	a.searchGen++
	gen, account := a.searchGen, a.accountGen
	a.searched, a.searching, a.searchErr, a.results, a.found = q, true, "", nil, nil
	a.foundAlbums, a.foundLists, a.foundShows = nil, nil, nil
	go func() {
		var found []spotify.ArtistCard
		var albums, lists, shows []spotify.Playlist
		var wg sync.WaitGroup
		wg.Go(func() { found, _ = spotify.SearchArtists(q, 12) })
		wg.Go(func() { albums, lists, shows, _ = spotify.SearchLists(q, 12) })
		res, err := spotify.Search(q, 60)
		wg.Wait()
		a.post(func() {
			if gen != a.searchGen || account != a.accountGen || a.quitting {
				return
			}
			a.searching = false
			a.results, a.found = res, found
			a.foundAlbums, a.foundLists, a.foundShows = albums, lists, shows
			if err != nil {
				a.searchErr = "搜索失败，请稍后再试"
			}
		})
	}()
}
