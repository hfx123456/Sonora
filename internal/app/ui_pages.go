package app

import (
	"cmp"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

const pagePad = 40

// columns returns how many cards of at least min DIPs fit across the page.
func (a *app) columns(c *ui.Context, min float32) int {
	w, _ := c.Size()
	inner := w - sidebarWidth - 2*pagePad
	return max(2, int((inner+20)/(min+20)))
}

func (a *app) heading(c *ui.Context, title, sub string) {
	ui.Column(c).Gap(4).Margin(0, 0, 18, 0).Children(func() {
		if sub != "" {
			ui.Text(c, sub).FontSize(12).FontWeight(600).TextColor(a.pal.muted).LetterSpacing(0.4)
		}
		ui.Text(c, title).FontSize(30).FontWeight(700).LetterSpacing(-0.6)
	})
}

func (a *app) section(c *ui.Context, title string) {
	ui.Text(c, title).FontSize(19).FontWeight(700).LetterSpacing(-0.3).Margin(34, 0, 14, 0)
}

func weekday(t time.Time) string {
	return [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[t.Weekday()]
}

// inCurrentWeek reports whether day falls between the Monday of now's calendar
// week and today.
func inCurrentWeek(day, now time.Time) bool {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	return !day.Before(monday) && !day.After(today)
}

// episodeDate is a release date, as 2006-01-02, as an episode's row shows it:
// its weekday within this week, mm-dd within this year, yyyy-mm-dd before.
func episodeDate(released string, now time.Time) string {
	day, err := time.ParseInLocation(time.DateOnly, released, time.Local)
	if err != nil {
		return ""
	}
	if inCurrentWeek(day, now) {
		return weekday(day)
	}
	if day.Year() == now.Year() {
		return day.Format("01-02")
	}
	return day.Format(time.DateOnly)
}

func (a *app) chartsPage(c *ui.Context) {
	loading := len(a.charts) == 0
	if loading && !a.homeLoading {
		a.centered(c, a.homeErr, a.loadHome)
		return
	}
	top, bottom := a.insets(c)
	scroll := ui.Scroll(c.Key("charts")).TrackScroll(&a.pageScroll).Grow(1).Padding(top+pageGap, pagePad, bottom+48, pagePad).ScrollbarInsets(top, 0, bottom, 0)
	if !a.glassEnabled() {
		a.pageWash(c, scroll, top, a.pageTint(pageCharts))
	}
	scroll.Children(func() {
		a.startMark(c)
		defer a.endMark(c)
		a.heading(c, "排行榜", "Spotify 官方榜单")
		if loading {
			s := a.pageShimmer(c)
			a.chartCardsSkeleton(c, s)
			a.section(c, "全部榜单")
			a.playlistCardsSkeleton(c, s, a.columns(c, 150)*2, 150, true)
			return
		}
		official := a.charts[:min(4, len(a.charts))]
		cols := a.officialChartColumns(c, len(official))
		var hoveredTint, fallbackTint ui.Color
		ui.Grid(c).Columns(cols).Gap(8).Margin(0, -12, 0, -12).Children(func() {
			for i := range official {
				t, hov := a.chartCard(c.Key(official[i].ID), &official[i])
				if hov && t != (ui.Color{}) {
					hoveredTint = t
				}
				if i == 0 && fallbackTint == (ui.Color{}) {
					fallbackTint = t
				}
			}
		})
		if len(a.charts) > 4 {
			a.section(c, "全部榜单")
			rest := a.charts[4:]
			ui.Grid(c).Columns(a.columns(c, 150)).GapX(0).GapY(12).Margin(0, -10, 0, -10).Children(func() {
				for i := range rest {
					if t, hov := a.playlistCard(c.Key(rest[i].ID), &rest[i]); hov && t != (ui.Color{}) {
						hoveredTint = t
					}
				}
			})
		}
		a.setPageTint(c, pageCharts, cmp.Or(hoveredTint, fallbackTint))
	})
}

// playlistCard is a square cover with the playlist's name below: it lifts
// under the pointer, where a play button shows on it.
func (a *app) playlistCard(c *ui.Context, pl *spotify.Playlist) (ui.Color, bool) {
	p := a.pal
	// Justify(Start) resets ButtonBase's centering, which in a grid pushes
	// the cover down beside a taller card.
	card := ui.ButtonBase(c).Column().Gap(10).Padding(10).AlignItems(ui.Stretch).Justify(ui.Start).FocusRing(false).Label(pl.Name)
	hovered := card.Hovered()
	a.styleBorderlessCard(card, 12, hovered)
	var tint ui.Color
	card.Children(func() {
		art := ui.Box(c).AspectRatio(1).Radius(10)
		lift := ui.Pose{}
		if hovered {
			lift = ui.Pose{Y: -3}
			art.Shadow(0, 12, 28, -6, p.cardShade)
		} else {
			art.Shadow(0, 2, 8, -2, p.cardShade)
		}
		art.AnimatePose(lift, ui.Snappy)
		art.Children(func() {
			tint = a.listArt(c, pl, 400, 10)
			a.coverPlayButton(c, pl.ID, hovered)
		})
		ui.Text(c, pl.Name).FontSize(13).FontWeight(500).MaxLines(2).LineHeight(1.35)
		if note := cmp.Or(pl.Note, pl.Creator.Nickname); note != "" {
			ui.Text(c, note).FontSize(12).TextColor(p.muted).SingleLine().Margin(-6, 0, 0, 0)
		}
	})
	if card.Clicked() {
		a.goTo(route{page: pagePlaylist, playlist: pl.ID})
	}
	return tint, hovered
}

// coverFill fills its parent with a cover, its corners rounded by radius.
// The cover rounds itself rather than its parent clipping it: a clip moved
// by a transform, as a card lifting under the pointer, is drawn on the CPU.
func (a *app) coverFill(c *ui.Context, url string, px int, radius float32) {
	bmp := bitmap(a.art.get(spotify.Sized(url, px), false))
	box := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Bottom(0).Radius(radius)
	in := artIn(box, bmp)
	if in < 1 {
		box.Background(a.pal.hover)
	}
	box.Children(func() { artImage(c, bmp, radius, in) })
}

// listArt fills its parent with pl's cover, the liked songs' drawn as
// everywhere else, and returns the cover's tint once loaded.
func (a *app) listArt(c *ui.Context, pl *spotify.Playlist, px int, radius float32) ui.Color {
	if pl.Liked() {
		likedArt(c, radius, float32(px)/6)
		return ui.Hex("#5038a0")
	}
	url := pl.Cover()
	a.coverFill(c, url, px, radius)
	return a.coverTint(url, px)
}

// coverPlayButton plays a playlist from its card, without opening it.
func (a *app) coverPlayButton(c *ui.Context, id string, shown bool) {
	b := ui.ButtonBase(c.Key("play")).Absolute().Bottom(10).Right(10).Size(38, 38).Radius(999).Center().
		Background(a.pal.accent).TextColor(ui.RGB(255, 255, 255)).Label("播放").FocusRing(false).
		Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, 0.3))
	pose := ui.Pose{Fade: 1, Scale: 0.8, Y: 6}
	if shown || b.Hovered() {
		pose = ui.Pose{Scale: 1}
		if b.Hovered() {
			pose.Scale = 1.08
		}
	}
	b.AnimatePose(pose, ui.Snappy)
	b.Children(func() { ui.Icon(c, iconPlay).Size(15, 15).Margin(0, 0, 0, 2) })
	if b.Clicked() {
		a.loadPlaylist(id, true)
	}
}

func (a *app) officialChartColumns(c *ui.Context, n int) int {
	cols := min(4, max(2, a.columns(c, 260)))
	if n == 4 && cols == 3 {
		return 2
	}
	return cols
}

// chartCard shows a chart with its top three songs beside its cover.
func (a *app) chartCard(c *ui.Context, pl *spotify.Playlist) (ui.Color, bool) {
	p := a.pal
	card := ui.ButtonBase(c).Gap(14).Padding(12).AlignItems(ui.Center).FocusRing(false).Label(pl.Name)
	hovered := card.Hovered()
	a.styleBorderlessCard(card, 14, hovered)
	cover := pl.Cover()
	card.Children(func() {
		ui.Box(c).Size(84, 84).Radius(9).Shadow(0, 3, 10, -2, p.cardShade).Children(func() {
			a.coverFill(c, cover, 200, 9)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(5).Children(func() {
			ui.Text(c, pl.Name).FontSize(14).FontWeight(700).SingleLine()
			for i, line := range pl.Preview {
				ui.Row(c).Gap(7).Children(func() {
					ui.Textf(c, "%d", i+1).FontSize(12).FontWeight(700).Width(10).FontFeatures("tnum").TextColor(p.muted)
					ui.RichText(c,
						ui.Span{Text: line.First, Weight: 500},
						ui.Span{Text: "  " + line.Second, Color: p.muted},
					).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
				})
			}
		})
	})
	if card.Clicked() {
		a.open(route{page: pagePlaylist, playlist: pl.ID})
	}
	return a.coverTint(cover, 200), hovered
}

func (a *app) playlistPage(c *ui.Context) {
	id := a.route.playlist
	pl := a.playlists[id]
	if pl == nil {
		if !a.loadingPl[id] {
			a.centered(c, a.plErr[id], func() { a.loadPlaylist(id, false) })
			return
		}
		s := a.pageShimmer(c)
		a.trackSkeleton(c, s, a.settings.PlaylistTrackCovers, func(c *ui.Context) { a.playlistHeaderSkeleton(c, s) })
		return
	}
	ordered := a.orderedPlaylistTracks(pl)
	tracks := cachedFilterTracks(&a.playlistFilterCache, id, ordered, a.playlistQueries[id])
	if a.playlistHeaderHeights == nil {
		a.playlistHeaderHeights = map[string]float32{}
	}
	list := a.trackRows(c, tracks, a.settings.PlaylistTrackCovers, func(c *ui.Context) {
		header := ui.Column(c.Key("playlist-hero")).Children(func() { a.playlistHeader(c, pl) })
		if height := header.LayoutBounds().H; height > 0 && height != a.playlistHeaderHeights[id] {
			a.playlistHeaderHeights[id] = height
			c.AnimationFrame()
		}
		ui.Box(c.Key("playlist-search-space")).Height(playlistToolbarHeight)
		if len(tracks) == 0 && strings.TrimSpace(a.playlistQueries[id]) != "" {
			ui.Text(c, "此歌单中没有匹配的歌曲").FontSize(14).TextColor(a.pal.muted).Padding(30, 12)
		}
	}, nil)
	headerHeight := a.playlistHeaderHeights[id]
	if headerHeight <= 0 {
		headerHeight = playlistCoverSize + playlistHeaderGap
	}
	a.pinTrackToolbar(c, list, "playlist-search-toolbar", headerHeight, func() {
		ui.Box(c).PaddingX(12).Children(func() { a.playlistSearch(c, pl) })
		a.playlistColumns(c, pl)
	})
}

const (
	playlistCoverSize = 176
	playlistHeaderGap = 18
)

func (a *app) playlistHeader(c *ui.Context, pl *spotify.Playlist) {
	p := a.pal
	tracks := a.orderedPlaylistTracks(pl)
	ui.Row(c).Gap(24).AlignItems(ui.End).Padding(0, 0, playlistHeaderGap, 0).Children(func() {
		ui.Box(c).Size(playlistCoverSize, playlistCoverSize).Shrink(0).Radius(14).Shadow(0, 12, 28, -10, p.cardShade).Children(func() {
			a.listArt(c, pl, 600, 14)
		})
		ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Gap(8).Children(func() {
			kind, unit := "歌单", "首"
			switch {
			case pl.Hybrid():
				kind = "本机混合歌单"
			case pl.NetEase():
				kind = "网易云歌单"
			case strings.HasPrefix(pl.ID, "spotify:album:"):
				kind = "专辑"
			case strings.HasPrefix(pl.ID, "spotify:show:"):
				kind, unit = "播客", "集"
			}
			ui.Text(c, kind).FontSize(12).FontWeight(600).TextColor(p.accent)
			ui.Text(c, pl.Name).FontSize(28).FontWeight(700).LetterSpacing(-0.5).MaxLines(2).LineHeight(1.2)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				if len(pl.Artists) > 0 {
					a.artistLinks(c.Key("by"), pl.Artists, 13, p.muted, p.text)
					ui.Text(c, " · ").FontSize(13).TextColor(p.muted)
				} else if pl.Owner != "" {
					if link(c.Key("owner"), pl.Creator.Nickname, 13, p.muted, p.text).Clicked() {
						a.goTo(route{page: pageUser, user: pl.Owner})
					}
					ui.Text(c, " · ").FontSize(13).TextColor(p.muted).Shrink(0)
				} else if pl.Creator.Nickname != "" {
					ui.Text(c, pl.Creator.Nickname+" · ").FontSize(13).TextColor(p.muted)
				}
				n := len(pl.Tracks)
				if unit == "集" {
					// The latest episodes list; the podcast has them all.
					n = max(n, pl.TrackCount)
				}
				countText := fmt.Sprintf("%d %s", n, unit)
				if pl.Hybrid() && n > 0 {
					if playable := countPlayableTracks(pl.Tracks); playable < n {
						countText = fmt.Sprintf("%d %s · 当前可播 %d %s", n, unit, playable, unit)
					}
				}
				ui.Text(c, countText).FontSize(13).TextColor(p.muted).Shrink(0)
			})
			if d := strings.TrimSpace(pl.Description); d != "" {
				ui.Text(c, strings.ReplaceAll(d, "\n", " ")).FontSize(12.5).TextColor(p.muted).MaxLines(2).LineHeight(1.45).Margin(2, 0, 0, 0)
			}
			ui.Row(c).Gap(10).Margin(10, 0, 0, 0).Children(func() {
				if pl.Hybrid() {
					if pillButton(c.Key("play"), iconShuffle, "随机混播", p.accent, p.onAccent).Clicked() {
						a.playHybridShuffled(pl)
					}
					if a.secondaryPillButton(c.Key("shuffle-order"), iconShuffle, "打乱顺序").Clicked() {
						a.shuffleHybridPlaylist(pl.ID)
					}
					importBtn := a.secondaryPillButton(c.Key("import-hybrid"), iconPlus, "导入歌单")
					importBtn.Menu(func(m *ui.Menu) {
						a.hybridImportSourcesMenu(m, pl.ID)
					})
				} else {
					if pillButton(c.Key("play"), iconPlay, "播放", p.accent, p.onAccent).Clicked() {
						a.playPlaylist(pl)
					}
					if a.secondaryPillButton(c.Key("shuffle"), iconShuffle, "随机播放").Clicked() {
						a.enableShuffle()
						a.playFrom(spotify.HomeItem{List: pl}, tracks, randomPlayable(tracks))
					}
				}
				if pl.NetEase() {
					toHybrid := a.secondaryPillButton(c.Key("netease-to-hybrid"), iconPlus, "加入混合歌单")
					toHybrid.Menu(func(m *ui.Menu) {
						a.playlistToHybridMenuItems(m, pl)
					})
				}
				if a.profile != nil && !pl.Liked() && !pl.NetEase() && a.editable(pl.ID) == nil {
					icon, label := iconPlus, "收藏"
					if a.isSaved(pl.ID) {
						icon, label = iconCheck, "已收藏"
					}
					if a.secondaryPillButton(c.Key("save"), icon, label).Clicked() {
						a.toggleSaved(pl)
					}
				}
				mine := a.editable(pl.ID)
				isShow := strings.HasPrefix(pl.ID, "spotify:show:")
				if mine != nil || a.inLibrary(pl.ID) || !isShow {
					more := iconButton(c.Key("more"), iconMore, 18, p.muted, "更多")
					more.Menu(func(m *ui.Menu) {
						hasTop := false
						if pl.Hybrid() {
							if m.Item("顺序播放").Chosen() {
								a.playHybridSequential(pl)
							}
							hasTop = true
						}
						if pl.NetEase() {
							if m.Item("刷新歌单").Chosen() {
								a.requestPlaylist(pl.ID, false, true)
							}
							hasTop = true
						}
						if mine != nil || a.inLibrary(pl.ID) {
							if hasTop {
								m.Separator()
							}
							pinLabel := "置顶歌单"
							if a.isPlaylistPinned(pl.ID) {
								pinLabel = "取消置顶"
							}
							if m.Item(pinLabel).Chosen() {
								a.togglePlaylistPinned(pl.ID)
							}
							hasTop = true
						}
						if !isShow {
							if hasTop {
								m.Separator()
							}
							a.playlistToHybridMenu(m, pl)
							hasTop = true
						}
						if mine != nil {
							m.Separator()
							if m.Item("重命名…").Chosen() {
								a.startRename(mine)
							}
							m.Separator()
							if m.Item("删除歌单…").Chosen() {
								a.confirmDelete(mine)
							}
						}
					})
				}
			})
		})
	})
}

func (a *app) searchPage(c *ui.Context) {
	hasOtherResults := len(a.found)+len(a.foundAlbums)+len(a.foundLists)+len(a.foundShows) > 0
	empty := len(a.results) == 0 && !hasOtherResults
	if empty && a.searching {
		a.trackSkeleton(c, a.pageShimmer(c), true, func(c *ui.Context) {
			a.heading(c, "“"+a.searched+"”", "搜索")
		})
		return
	}
	if empty {
		msg := a.searchErr
		if msg == "" {
			msg = "没有找到相关歌曲"
		}
		top, bottom := a.insets(c)
		ui.Column(c).Grow(1).Padding(top+pageGap, pagePad, bottom, pagePad).Children(func() {
			a.heading(c, "“"+a.searched+"”", "搜索")
			a.centered(c, msg, nil)
		})
		return
	}
	a.trackRows(c, a.results, a.settings.PlaylistTrackCovers, func(c *ui.Context) {
		a.heading(c, "“"+a.searched+"”", "搜索")
		if !hasOtherResults {
			return
		}
		ui.Column(c).PaddingX(12).Margin(0, 0, 8, 0).Children(func() {
			title := func(s string) {
				ui.Text(c, s).FontSize(19).FontWeight(700).LetterSpacing(-0.3).Margin(4, 0, 14, 0)
			}
			var hoveredTint, fallbackTint ui.Color
			if len(a.found) > 0 {
				title("艺人")
				if t, hov := a.artistRow(c, a.found); hov {
					hoveredTint = cmp.Or(t, hoveredTint)
				} else if fallbackTint == (ui.Color{}) {
					fallbackTint = t
				}
				ui.Box(c).Height(26)
			}
			// A row of each, the best: the rest a search more precise finds.
			cols := a.columns(c, 150)
			for _, s := range []struct {
				name  string
				lists []spotify.Playlist
			}{{"专辑", a.foundAlbums}, {"歌单", a.foundLists}, {"播客", a.foundShows}} {
				if len(s.lists) == 0 {
					continue
				}
				title(s.name)
				ui.Grid(c.Key(s.name)).Columns(cols).GapX(0).GapY(8).Margin(0, -10, 26, -10).Children(func() {
					for i := range s.lists[:min(len(s.lists), cols)] {
						t, hov := a.playlistCard(c.Key(s.lists[i].ID), &s.lists[i])
						if hov && t != (ui.Color{}) {
							hoveredTint = t
						}
						if i == 0 && fallbackTint == (ui.Color{}) {
							fallbackTint = t
						}
					}
				})
			}
			a.setPageTint(c, pageSearch, cmp.Or(hoveredTint, fallbackTint))
			if len(a.results) > 0 {
				ui.Text(c, "歌曲").FontSize(19).FontWeight(700).LetterSpacing(-0.3).Margin(4, 0, 6, 0)
			}
		})
	}, nil)
}

// trackRows lists tracks under a header, and over a footer if any, which
// scroll with them; a click plays the list from the track clicked.
func (a *app) trackRows(c *ui.Context, tracks []spotify.Track, covers bool, header, footer func(c *ui.Context)) ui.Element {
	p := a.pal
	cur := a.pl.Current()
	w, _ := c.Size()
	wide := w-sidebarWidth > 820
	top, bottom := a.insets(c)
	// Rows out of view are not built, their marks with them: a list
	// scrolled away from its start or end has content under the bars.
	a.underTop, a.underBottom = true, true
	// The playlist the tracks are listed in, if any, which they can be
	// taken out of.
	var pid string
	if a.route.page == pagePlaylist {
		pid = a.route.playlist
	}
	isShow := strings.HasPrefix(pid, "spotify:show:")
	timeWidth := float32(44)
	if isShow {
		timeWidth = 148
	}
	n := len(tracks) + 1
	if footer != nil {
		n++
	}
	return ui.List(c.Key(a.route), &a.trackList, n, func(i int) {
		if i == 0 {
			ui.Column(c).Children(func() {
				a.startMark(c)
				header(c)
			})
			return
		}
		if i == len(tracks)+1 {
			ui.Column(c).Children(func() {
				footer(c)
				a.endMark(c)
			})
			return
		}
		if i == len(tracks) && footer == nil {
			defer a.endMark(c)
		}
		i--
		tr := &tracks[i]
		playing := cur != nil && cur.ID == tr.ID
		row := ui.ButtonBase(c.Key(tr.ID)).Height(54).PaddingX(12).Gap(14).Radius(10).AlignItems(ui.Center).FocusRing(false).Label(tr.Name).
			ContextMenu(a.songItems(tr, pid))
		hovered := row.Hovered()
		bg := ui.Transparent
		switch {
		case playing:
			bg = p.accent.Alpha(0.09)
		case hovered:
			bg = p.hover
		}
		row.Background(row.AnimateColor("bg", bg, ui.Animation{Duration: 100 * time.Millisecond}))
		if !tr.Playable() {
			row.Opacity(0.42)
		}
		row.Children(func() {
			ui.Box(c).Width(26).Center().Children(func() {
				switch {
				case playing:
					a.nowPlayingBars(c)
				case hovered && tr.Playable():
					ui.Icon(c, iconPlay).Size(13, 13).TextColor(p.text)
				default:
					ui.Textf(c, "%d", i+1).FontSize(13).FontFeatures("tnum").TextColor(p.faint)
				}
			})
			if covers {
				a.cover(c, tr.Album.PicURL, 96, 38, 6)
			}
			ui.Column(c).Grow(3).Basis(0).MinWidth(0).Gap(3).Children(func() {
				name := p.text
				if playing {
					name = p.accent
				}
				ui.Text(c, tr.Name).FontWeight(500).TextColor(name).FontSize(14).SingleLine()
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					if !tr.Playable() {
						label := "不可播放"
						if tr.LocalError != "" {
							label = tr.LocalError
						}
						ui.Text(c, label).FontSize(9).FontWeight(800).TextColor(p.accent).Padding(0, 4).Radius(3).Border(1, p.accent.Alpha(0.6))
					} else if tr.Video && a.mvTag(c.Key("mv"), tr).Clicked() {
						if pid != "" && strings.TrimSpace(a.playlistQueries[pid]) != "" {
							a.playPlaylistMVMatch(pid, *tr)
						} else {
							a.playMV(a.routeItem(), tracks, i)
						}
					}
					if spotify.IsHybridPlaylistID(pid) {
						srcLabel, cached := hybridTrackSourceLabel(tr)
						if cached {
							srcLabel += " · 已缓存"
						}
						ui.Text(c, srcLabel).FontSize(9).FontWeight(600).TextColor(p.muted).
							Background(p.hover).Padding(0, 5).Radius(3).Shrink(0)
					}
					a.artistLinks(c.Key("by"), tr.Artists, 12.5, p.muted, p.text)
				})
			})
			if wide {
				ui.Row(c).Grow(2).Basis(0).MinWidth(0).Children(func() {
					if tr.Album.ID == "" {
						ui.Text(c, tr.Album.Name).FontSize(12.5).TextColor(p.muted).SingleLine()
					} else if link(c.Key("album"), tr.Album.Name, 12.5, p.muted, p.text).Clicked() {
						a.goTo(route{page: pagePlaylist, playlist: tr.Album.ID})
					}
				})
			}
			// A liked song's heart shows; the others' as the pointer is on them.
			ui.Box(c).Size(32, 32).Center().Children(func() {
				if a.liked[libraryID(tr)] || hovered {
					a.likeButton(c.Key("like"), tr, 15, p.muted)
				}
			})
			ui.Row(c).Width(timeWidth).Gap(6).AlignItems(ui.Center).Justify(ui.End).Shrink(0).Children(func() {
				if isShow && tr.Episode {
					if date := episodeDate(tr.Released, c.Now()); date != "" {
						ui.Text(c, date).FontSize(12.5).FontFeatures("tnum").TextColor(p.faint)
						ui.Text(c, "·").FontSize(12.5).TextColor(p.accent)
					}
				}
				ui.Text(c, clock(tr.Length())).FontSize(12.5).FontFeatures("tnum").TextColor(p.faint)
			})
		})
		if a.rowDoubleClicked(c, row, playing) && tr.Playable() {
			if pid != "" && strings.TrimSpace(a.playlistQueries[pid]) != "" {
				a.playPlaylistMatch(pid, *tr)
			} else if a.route.page == pageLocal && strings.TrimSpace(a.localQuery) != "" {
				a.playLocalMatch(*tr)
			} else if spotify.IsHybridPlaylistID(pid) {
				a.playPlaylistTrack(a.playlists[pid], tracks, i)
			} else {
				a.playFrom(a.routeItem(), tracks, i)
			}
		}
	}).Grow(1).Padding(top+pageGap, pagePad-12, bottom+32, pagePad-12).ScrollbarInsets(top, 0, bottom, 0)
}

func (a *app) trackHeaderOffset() (offset float32, visible bool) {
	// Once row zero leaves the virtualized list, its start mark stops
	// updating. The list's visible range still tells the toolbar to pin.
	if first, _ := a.trackList.Visible(); first > 0 {
		return 0, false
	}
	if a.startMarkW <= 0 {
		return 0, true
	}
	return max(0, a.startMarkTop-a.startMarkY), true
}

// nowPlayingBars dance in the row of the track playing.
func (a *app) nowPlayingBars(c *ui.Context) {
	color := a.pal.accent
	ui.Box(c).Size(16, 14).Draw(func(p *ui.Painter, r ui.Rect) {
		lv, live := a.pl.Levels()
		a.rowBars.step(p.Now(), lv, live)
		drawBars(p, r, &a.rowBars, 4, color, 2.5)
		if live || a.rowBars.group(0, player.Bands) > 0.01 {
			p.AnimationFrame()
		}
	})
}
