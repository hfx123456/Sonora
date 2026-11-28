package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/spotify"
)

var (
	fetchNetEaseAccount       = spotify.NetEaseAccountInfo
	fetchNetEaseUserPlaylists = spotify.NetEaseUserPlaylists
	fetchNetEasePlaylist      = spotify.NetEasePlaylist
	resolveNetEasePlaylistID  = spotify.ResolveNetEasePlaylistID
	searchSpotifyForNetEase   = spotify.Search
)

type neteaseImportState struct {
	open           bool
	targetHybridID string // "" means create a new hybrid playlist named after the NetEase playlist
	createNew      bool
	autoShuffle    bool
	input          string
	err            string
	status         string

	loginWin *mygo.Window
	loginGen int
	logging  bool

	playlists        []spotify.NetEasePlaylistSummary
	playlistsLoading bool
	playlistsErr     string
	playlistsGen     int
	listScroll       ui.ScrollState

	importing    bool
	importGen    int
	importCancel context.CancelFunc
	importTitle  string
	done         int
	total        int
	localHits    int
	spotifyHits  int
	unmatched    int
}

func (a *app) openNetEaseImport(targetHybridID string) {
	st := &a.netease
	st.open = true
	st.targetHybridID = targetHybridID
	st.createNew = targetHybridID == ""
	if !st.importing {
		st.err = ""
		st.status = ""
	}
	if !st.autoShuffle && !st.importing && st.input == "" && len(st.playlists) == 0 {
		st.autoShuffle = true
	}
	if a.settings.NetEaseUserID > 0 && a.settings.neteaseCookie() != "" && len(st.playlists) == 0 && !st.playlistsLoading {
		a.loadNetEasePlaylists()
	}
}

func (a *app) closeNetEaseLoginWindow() {
	a.netease.loginGen++
	if a.netease.loginWin != nil {
		w := a.netease.loginWin
		a.netease.loginWin = nil
		w.Close()
	}
	a.netease.logging = false
}

func (a *app) cancelNetEaseImport() {
	a.netease.importGen++
	if a.netease.importCancel != nil {
		a.netease.importCancel()
		a.netease.importCancel = nil
	}
	if a.netease.importing {
		a.netease.importing = false
		a.netease.status = ""
		a.toast = "已取消网易云歌单导入"
	}
}

func (a *app) signOutNetEase() {
	a.closeNetEaseLoginWindow()
	a.settings.clearNetEaseAccount()
	a.settings.save()
	a.netease.playlists = nil
	a.netease.playlistsErr = ""
	a.netease.playlistsLoading = false
	a.netease.playlistsGen++
	a.toast = "已退出网易云账号"
}

func (a *app) loadNetEasePlaylists() {
	uid := a.settings.NetEaseUserID
	cookie := a.settings.neteaseCookie()
	if uid <= 0 || cookie == "" {
		return
	}
	a.netease.playlistsGen++
	gen := a.netease.playlistsGen
	a.netease.playlistsLoading = true
	a.netease.playlistsErr = ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		lists, err := fetchNetEaseUserPlaylists(ctx, cookie, uid)
		a.post(func() {
			if a.netease.playlistsGen != gen || a.quitting {
				return
			}
			a.netease.playlistsLoading = false
			if err != nil {
				a.netease.playlistsErr = "读取网易云歌单失败：" + err.Error()
				return
			}
			a.netease.playlists = lists
		})
	}()
}

func (a *app) openNetEaseLogin() {
	if a.netease.logging {
		return
	}
	a.closeNetEaseLoginWindow()
	a.netease.loginGen++
	gen := a.netease.loginGen
	a.netease.logging = true
	a.netease.err = ""
	a.netease.status = "请在弹出的窗口中扫码登录网易云音乐，登录成功后将自动读取歌单…"

	w := mygo.NewWindow(mygo.WindowOptions{
		Title:  "扫码登录网易云音乐",
		URL:    "https://music.163.com/",
		Width:  980,
		Height: 680,
	})
	a.netease.loginWin = w
	w.OnClose(func(*mygo.CloseEvent) {
		if a.netease.loginWin == w {
			a.netease.loginWin = nil
		}
		if a.netease.loginGen == gen {
			a.netease.loginGen++
			a.netease.logging = false
			if a.netease.err == "" {
				a.netease.status = ""
			}
			a.redraw()
		}
	})
	w.Show()
	w.Focus()

	cookieURLs := []string{"https://music.163.com/", "https://y.music.163.com/"}
	go func() {
		lastChecked := ""
		var retryAt time.Time
		deadline := time.Now().Add(5 * time.Minute)
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()

		for time.Now().Before(deadline) {
			<-ticker.C
			var active bool
			mygo.RunOnMain(func() {
				active = !a.quitting && a.netease.loginGen == gen && a.netease.loginWin == w
			})
			if !active {
				return
			}

			var cookie string
			mygo.RunOnMain(func() {
				cookie = collectWebViewCookies(cookieURLs)
			})
			if cookie == "" || !strings.Contains(cookie, "MUSIC_U=") || (cookie == lastChecked && time.Now().Before(retryAt)) {
				continue
			}
			lastChecked = cookie

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			acc, err := fetchNetEaseAccount(ctx, cookie)
			cancel()
			if err != nil || acc == nil {
				retryAt = time.Now().Add(4 * time.Second)
				continue
			}

			a.post(func() {
				if a.netease.loginGen != gen || a.quitting {
					return
				}
				a.closeNetEaseLoginWindow()
				a.settings.setNetEaseAccount(acc.UserID, acc.Nickname, cookie)
				a.settings.save()
				a.netease.status = ""
				a.netease.err = ""
				a.toast = "已登录网易云音乐：" + acc.Nickname
				a.loadNetEasePlaylists()
			})
			return
		}

		a.post(func() {
			if a.netease.loginGen == gen {
				a.closeNetEaseLoginWindow()
				a.netease.err = "等待网易云扫码登录超时，请重试"
				a.netease.status = ""
			}
		})
	}()
}

func (a *app) startNetEaseImportFromInput() {
	raw := strings.TrimSpace(a.netease.input)
	if raw == "" {
		a.netease.err = "请输入网易云歌单链接或歌单 ID"
		return
	}
	a.startNetEaseImport(0, raw, "")
}

func (a *app) startNetEaseImportBySummary(pl spotify.NetEasePlaylistSummary) {
	a.startNetEaseImport(pl.ID, "", pl.Name)
}

func (a *app) startNetEaseImport(playlistID int64, rawInput, fallbackTitle string) {
	if a.netease.importing {
		return
	}
	if a.netease.importCancel != nil {
		a.netease.importCancel()
	}
	a.netease.importGen++
	gen := a.netease.importGen
	ctx, cancel := context.WithCancel(context.Background())
	a.netease.importCancel = cancel
	a.netease.importing = true
	a.netease.err = ""
	a.netease.importTitle = fallbackTitle
	a.netease.status = "正在读取网易云歌单…"
	a.netease.done, a.netease.total = 0, 0
	a.netease.localHits, a.netease.spotifyHits, a.netease.unmatched = 0, 0, 0

	cookie := a.settings.neteaseCookie()
	localSnap := slices.Clone(a.localTracks)
	canSearchSpotify := a.profile != nil
	targetID := a.netease.targetHybridID
	if a.netease.createNew {
		targetID = ""
	}
	autoShuffle := a.netease.autoShuffle

	go func() {
		defer cancel()

		pid := playlistID
		if pid <= 0 {
			resolved, err := resolveNetEasePlaylistID(ctx, rawInput)
			if err != nil {
				a.post(func() {
					if a.netease.importGen != gen {
						return
					}
					a.netease.importing = false
					a.netease.importCancel = nil
					a.netease.status = ""
					a.netease.err = err.Error()
				})
				return
			}
			pid = resolved
		}

		detail, err := fetchNetEasePlaylist(ctx, cookie, pid)
		if err != nil {
			a.post(func() {
				if a.netease.importGen != gen {
					return
				}
				a.netease.importing = false
				a.netease.importCancel = nil
				a.netease.status = ""
				a.netease.err = "读取网易云歌单失败：" + err.Error()
			})
			return
		}
		if len(detail.Tracks) == 0 {
			a.post(func() {
				if a.netease.importGen != gen {
					return
				}
				a.netease.importing = false
				a.netease.importCancel = nil
				a.netease.status = ""
				a.netease.err = "该网易云歌单没有可导入的歌曲"
			})
			return
		}

		title := detail.Name
		if title == "" {
			title = fallbackTitle
		}
		total := len(detail.Tracks)
		a.post(func() {
			if a.netease.importGen != gen {
				return
			}
			a.netease.importTitle = title
			a.netease.total = total
			a.netease.status = fmt.Sprintf("正在匹配「%s」中的 %d 首歌曲…", title, total)
		})

		matched := make([]spotify.Track, total)
		var (
			mu          sync.Mutex
			doneCount   int
			localHits   int
			spotifyHits int
			unmatched   int
			lastPost    time.Time
		)

		reportProgress := func(force bool) {
			mu.Lock()
			d, l, s, u := doneCount, localHits, spotifyHits, unmatched
			shouldPost := force || time.Since(lastPost) >= 120*time.Millisecond || d == total
			if shouldPost {
				lastPost = time.Now()
			}
			mu.Unlock()
			if !shouldPost {
				return
			}
			a.post(func() {
				if a.netease.importGen != gen {
					return
				}
				a.netease.done = d
				a.netease.localHits = l
				a.netease.spotifyHits = s
				a.netease.unmatched = u
				a.netease.status = fmt.Sprintf("正在匹配「%s」：%d / %d（本机/云盘 %d · Spotify %d · 未找到 %d）", title, d, total, l, s, u)
			})
		}

		const workers = 3
		jobs := make(chan int, total)
		for i := range total {
			jobs <- i
		}
		close(jobs)

		var wg sync.WaitGroup
		for range min(workers, total) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range jobs {
					if ctx.Err() != nil {
						return
					}
					item := detail.Tracks[idx]
					tr, kind := matchNetEaseTrack(ctx, item, localSnap, canSearchSpotify)
					matched[idx] = tr
					mu.Lock()
					doneCount++
					switch kind {
					case neteaseMatchLocal:
						localHits++
					case neteaseMatchSpotify:
						spotifyHits++
					default:
						unmatched++
					}
					mu.Unlock()
					reportProgress(false)
				}
			}()
		}
		wg.Wait()
		if ctx.Err() != nil {
			return
		}
		reportProgress(true)

		a.post(func() {
			if a.netease.importGen != gen || a.quitting {
				return
			}
			a.finishNetEaseImport(targetID, detail.ID, title, matched, autoShuffle, localHits, spotifyHits, unmatched)
		})
	}()
}

func (a *app) finishNetEaseImport(targetID string, neteaseID int64, title string, matched []spotify.Track, autoShuffle bool, localHits, spotifyHits, unmatched int) {
	a.netease.importing = false
	a.netease.importCancel = nil
	a.netease.status = ""
	a.netease.open = false
	a.netease.input = ""

	dstID := targetID
	if dstID == "" || a.hybridEntryIndex(dstID) < 0 {
		name := title
		if name == "" {
			name = fmt.Sprintf("网易云歌单 %d", neteaseID)
		}
		made := a.createHybridPlaylist(name, nil)
		if made == nil {
			return
		}
		dstID = made.ID
	}
	added, skipped := a.addManyToHybrid(dstID, matched)
	if autoShuffle && len(a.playlists[dstID].Tracks) >= 2 {
		a.shuffleHybridPlaylist(dstID)
	}
	a.open(route{page: pagePlaylist, playlist: dstID})

	summary := fmt.Sprintf("网易云导入完成：新增 %d 首（本机/云盘 %d · Spotify %d · 未匹配 %d）", added, localHits, spotifyHits, unmatched)
	if skipped > 0 {
		summary = fmt.Sprintf("网易云导入完成：新增 %d 首，跳过 %d 首重复（本机/云盘 %d · Spotify %d · 未匹配 %d）", added, skipped, localHits, spotifyHits, unmatched)
	}
	a.toast = summary
}

type neteaseMatchKind int

const (
	neteaseMatchNone neteaseMatchKind = iota
	neteaseMatchLocal
	neteaseMatchSpotify
)

func matchNetEaseTrack(ctx context.Context, item spotify.NetEasePlaylistTrack, localTracks []spotify.Track, canSearchSpotify bool) (spotify.Track, neteaseMatchKind) {
	if hit, ok := spotify.MatchCandidateForNetEase(item, localTracks, false); ok {
		return hit, neteaseMatchLocal
	}
	if canSearchSpotify {
		for _, q := range spotify.NetEaseSearchQueries(item) {
			if ctx.Err() != nil {
				break
			}
			results, err := searchSpotifyWithRetry(ctx, q)
			if err != nil {
				continue
			}
			if hit, ok := spotify.MatchCandidateForNetEase(item, results, true); ok {
				return hit, neteaseMatchSpotify
			}
		}
	}
	artists := make([]spotify.Artist, 0, len(item.Artists))
	for _, name := range item.Artists {
		artists = append(artists, spotify.Artist{Name: name})
	}
	return spotify.Track{
		ID:         fmt.Sprintf("netease:%d", item.ID),
		Name:       item.Name,
		Artists:    artists,
		Album:      spotify.Album{Name: item.Album, PicURL: item.PicURL},
		Duration:   item.Duration,
		Unplayable: true,
		LocalError: "未匹配到音源",
	}, neteaseMatchNone
}

func searchSpotifyWithRetry(ctx context.Context, query string) ([]spotify.Track, error) {
	for attempt := range 2 {
		results, err := searchSpotifyForNetEase(query, 8)
		if err == nil {
			return results, nil
		}
		var throttled *spotify.Throttled
		if errors.As(err, &throttled) && attempt == 0 {
			wait := throttled.Wait
			if wait <= 0 || wait > 5*time.Second {
				wait = 1500 * time.Millisecond
			}
			select {
			case <-time.After(wait):
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return nil, err
	}
	return nil, context.Canceled
}

func (a *app) neteaseImportDialog(c *ui.Context) {
	p := a.pal
	st := &a.netease
	ui.DialogBase(c.Key("netease-import"), &st.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.38))
		panel.Width(480).Padding(24).Gap(14).
			Transition(ui.ElementTransition{Animation: &ui.Snappy, Enter: &ui.Motion{Scale: 0.94}, Exit: &ui.Motion{Scale: 0.96}})
		a.styleGlassPanel(panel, 20, p.bg, p.hairline, 60, 24, ui.RGBA(0, 0, 0, 0.4))

		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, "从网易云歌单导入").FontSize(17).FontWeight(700)
				ui.Text(c, "免费账号即可使用，自动匹配本机/云盘与 Spotify 完整正版音源。").FontSize(12).TextColor(p.muted)
			})
			if iconButton(c.Key("close-netease"), iconX, 14, p.muted, "关闭").Clicked() {
				st.open = false
			}
		})

		if st.targetHybridID != "" {
			targetName := "当前混合歌单"
			if pl := a.playlists[st.targetHybridID]; pl != nil {
				targetName = pl.Name
			}
			mode := 0
			if st.createNew {
				mode = 1
			}
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, "导入到").FontSize(12).TextColor(p.muted)
				if segmented(c.Key("netease-dst-mode"), &mode, "「"+targetName+"」", "新建混合歌单") {
					st.createNew = mode == 1
				}
			})
		}

		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "粘贴网易云歌单链接或 ID（公开歌单免登录）").FontSize(12).FontWeight(600).TextColor(p.muted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c.Key("netease-url"), &st.input).
					Placeholder("例如：https://music.163.com/#/playlist?id=3778678 或 3778678").
					Label("网易云歌单链接或 ID").FontSize(13).Grow(1).Disabled(st.importing)
				if in.Submitted() && !st.importing {
					a.startNetEaseImportFromInput()
				}
				goBtn := pillButton(c.Key("netease-go"), nil, "导入", p.accent, p.onAccent)
				if st.importing || strings.TrimSpace(st.input) == "" {
					goBtn.Disabled(true).Opacity(0.5)
				}
				if goBtn.Clicked() {
					a.startNetEaseImportFromInput()
				}
			})
		})

		ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
			ui.Text(c, "导入完成后自动打乱顺序").FontSize(12.5).TextColor(p.text).Grow(1)
			ui.Switch(c.Key("netease-autoshuffle"), &st.autoShuffle).Label("导入完成后自动打乱顺序").Disabled(st.importing)
		})

		if st.importing {
			ui.Column(c).Padding(12).Gap(8).Radius(12).Background(p.hover).Children(func() {
				ui.Text(c, st.status).FontSize(12.5).FontWeight(500)
				if st.total > 0 {
					frac := float32(st.done) / float32(st.total)
					ui.Box(c).Height(5).Radius(3).Background(p.hairline).Children(func() {
						ui.Box(c).Absolute().Top(0).Left(0).Bottom(0).Width(max(4, frac*408)).Radius(3).Background(p.accent)
					})
				}
				ui.Row(c).Justify(ui.End).Children(func() {
					if ui.Button(c.Key("netease-cancel-import"), "取消导入").Clicked() {
						a.cancelNetEaseImport()
					}
				})
			})
		} else if st.status != "" {
			ui.Text(c, st.status).FontSize(12).TextColor(p.muted)
		}
		if st.err != "" {
			ui.Text(c, st.err).FontSize(12).TextColor(p.accent)
		}

		ui.Box(c).Height(1).Background(p.hairline)

		// Optional NetEase account section for browsing personal/private playlists.
		loggedIn := a.settings.NetEaseUserID > 0 && a.settings.neteaseCookie() != ""
		ui.Column(c).Gap(8).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				title := "我的网易云歌单"
				if loggedIn && a.settings.NetEaseNickname != "" {
					title = "我的网易云歌单 · " + a.settings.NetEaseNickname
				}
				ui.Text(c, title).FontSize(13).FontWeight(600).Grow(1).SingleLine()
				if loggedIn {
					if ui.Button(c.Key("netease-refresh"), "刷新").Disabled(st.playlistsLoading || st.importing).Clicked() {
						a.loadNetEasePlaylists()
					}
					if ui.Button(c.Key("netease-logout"), "退出").Disabled(st.importing).Clicked() {
						a.signOutNetEase()
					}
				} else {
					label := "扫码登录网易云"
					if st.logging {
						label = "等待扫码…"
					}
					if ui.Button(c.Key("netease-login"), label).Disabled(st.logging || st.importing).Clicked() {
						a.openNetEaseLogin()
					}
				}
			})

			switch {
			case !loggedIn:
				ui.Text(c, "扫码登录后可直接导入你的「我喜欢的音乐」、私密歌单和收藏歌单。").FontSize(12).TextColor(p.muted)
			case st.playlistsLoading:
				ui.Text(c, "正在读取你的网易云歌单…").FontSize(12).TextColor(p.muted).Padding(8, 0)
			case st.playlistsErr != "":
				ui.Text(c, st.playlistsErr).FontSize(12).TextColor(p.accent)
			case len(st.playlists) == 0:
				ui.Text(c, "没有读取到歌单，点击上方「刷新」重试。").FontSize(12).TextColor(p.muted)
			default:
				ui.Scroll(c.Key("netease-pl-list")).Height(200).TrackScroll(&st.listScroll).Children(func() {
					ui.Column(c).Gap(4).Children(func() {
						for _, pl := range st.playlists {
							row := ui.ButtonBase(c.Key(pl.ID)).Height(44).PaddingX(8).Gap(10).Radius(8).
								AlignItems(ui.Center).Justify(ui.Start).FocusRing(false).Label(pl.Name).Disabled(st.importing)
							if row.Hovered() {
								row.Background(p.hover)
							}
							row.Children(func() {
								a.cover(c, pl.CoverURL, 80, 30, 6).Shrink(0)
								ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
									ui.Text(c, pl.Name).FontSize(13).FontWeight(500).SingleLine()
									sub := fmt.Sprintf("%d 首", pl.TrackCount)
									if pl.Subscribed {
										sub += " · 收藏"
									}
									ui.Text(c, sub).FontSize(11).TextColor(p.muted).SingleLine()
								})
								ui.Text(c, "导入").FontSize(12).FontWeight(600).TextColor(p.accent).Shrink(0)
							})
							if row.Clicked() {
								a.startNetEaseImportBySummary(pl)
							}
						}
					})
				})
			}
		})
	})
}
