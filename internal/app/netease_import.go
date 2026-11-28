package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/spotify"
)

var (
	fetchNetEaseAccount       = spotify.NetEaseAccountInfo
	fetchNetEaseUserPlaylists = spotify.NetEaseUserPlaylists
	fetchNetEasePlaylist      = spotify.NetEasePlaylist
	fetchNetEaseSongMedia     = spotify.NetEaseSongMedia
	resolveNetEasePlaylistID  = spotify.ResolveNetEasePlaylistID
	searchSpotifyForNetEase   = spotify.Search
	resolveSpotifyForNetEase  = spotify.ResolveMedia
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

	cache *neteasePlaybackCache
}

type neteasePlaybackCache struct {
	mu              sync.RWMutex
	cachedCookie    string
	cookieLoaded    bool
	spotifyFallback map[int64]string // netease song ID -> resolved Spotify track URI
}

var neteaseCacheInitMu sync.Mutex

func (a *app) neteaseCache() *neteasePlaybackCache {
	neteaseCacheInitMu.Lock()
	defer neteaseCacheInitMu.Unlock()
	if a.netease.cache == nil {
		a.netease.cache = &neteasePlaybackCache{spotifyFallback: map[int64]string{}}
	}
	return a.netease.cache
}

func (a *app) syncNetEaseCookieCache(cookie string) {
	c := a.neteaseCache()
	c.mu.Lock()
	c.cachedCookie = cookie
	c.cookieLoaded = true
	c.mu.Unlock()
}

func (a *app) neteasePlaybackCookie() string {
	c := a.neteaseCache()
	c.mu.RLock()
	loaded := c.cookieLoaded
	cookie := c.cachedCookie
	c.mu.RUnlock()
	if loaded {
		return cookie
	}
	cookie = a.settings.neteaseCookie()
	a.syncNetEaseCookieCache(cookie)
	return cookie
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
	cookie := a.settings.neteaseCookie()
	a.syncNetEaseCookieCache(cookie)
	if a.settings.NetEaseUserID > 0 && cookie != "" && len(st.playlists) == 0 && !st.playlistsLoading {
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
	a.syncNetEaseCookieCache("")
	a.netease.playlists = nil
	a.netease.playlistsErr = ""
	a.netease.playlistsLoading = false
	a.netease.playlistsGen++
	a.toast = "已退出网易云账号"
}

func (a *app) loadNetEasePlaylists() {
	uid := a.settings.NetEaseUserID
	cookie := a.settings.neteaseCookie()
	a.syncNetEaseCookieCache(cookie)
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
				a.syncNetEaseCookieCache(cookie)
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
	a.netease.status = "正在读取网易云歌单…"

	cookie := a.settings.neteaseCookie()
	a.syncNetEaseCookieCache(cookie)
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
		if ctx.Err() != nil {
			return
		}

		title := detail.Name
		if title == "" {
			title = fallbackTitle
		}
		tracks := make([]spotify.Track, 0, len(detail.Tracks))
		for _, item := range detail.Tracks {
			if item.ID <= 0 || item.Name == "" {
				continue
			}
			tracks = append(tracks, item.ToTrack())
		}

		a.post(func() {
			if a.netease.importGen != gen || a.quitting {
				return
			}
			a.finishNetEaseImport(targetID, detail.ID, title, tracks, autoShuffle)
		})
	}()
}

func (a *app) finishNetEaseImport(targetID string, neteaseID int64, title string, tracks []spotify.Track, autoShuffle bool) {
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
	added, skipped := a.addManyToHybrid(dstID, tracks)
	if autoShuffle && len(a.playlists[dstID].Tracks) >= 2 {
		a.shuffleHybridPlaylist(dstID)
	}
	a.open(route{page: pagePlaylist, playlist: dstID})

	switch {
	case added == 0 && skipped > 0:
		a.toast = "网易云歌单中的歌曲已全部在混合歌单中"
	case skipped > 0:
		a.toast = fmt.Sprintf("已导入 %d 首网易云歌曲（跳过 %d 首重复）· 不可播时将自动切换 Spotify", added, skipped)
	default:
		a.toast = fmt.Sprintf("已直接导入 %d 首网易云歌曲 · 默认走网易云播放，不可播时自动切换 Spotify", added)
	}
}

// resolveNetEasePlayback resolves a NetEase track for playback:
//  1. Default: fetch the direct audio stream from NetEase's playback API.
//  2. Fallback 1: if unavailable/trial-only on NetEase, check the user's local/cloud library.
//  3. Fallback 2: if still unavailable, search Spotify and play the matched Spotify track.
func (a *app) resolveNetEasePlayback(ctx context.Context, tr spotify.Track) (*spotify.Media, error) {
	id := tr.NetEaseID()
	if id <= 0 {
		return nil, spotify.ErrNetEaseUnavailable
	}

	// 1. Try NetEase direct playback first.
	cookie := a.neteasePlaybackCookie()
	media, neErr := fetchNetEaseSongMedia(ctx, cookie, id)
	if neErr == nil && media != nil {
		if media.Duration <= 0 {
			media.Duration = tr.Length()
		}
		return media, nil
	}

	want := trackToNetEaseQuery(tr)

	// 2. Fallback to local or cloud library if the user already has this song.
	if localSnap := spotify.LocalTracksSnapshot(); len(localSnap) > 0 {
		if hit, ok := spotify.MatchCandidateForNetEase(want, localSnap, false); ok {
			if m, err := resolveLocalOrCloudFallback(ctx, hit); err == nil && m != nil {
				log.Printf("netease: %q (%d) unavailable (%v), playing local/cloud fallback %s", tr.Name, id, neErr, hit.ID)
				return m, nil
			}
		}
	}

	// 3. Fallback to Spotify playback (using cached URI when available).
	cache := a.neteaseCache()
	cache.mu.RLock()
	cachedURI := cache.spotifyFallback[id]
	cache.mu.RUnlock()
	if cachedURI != "" {
		if m, err := resolveSpotifyForNetEase(cachedURI); err == nil && m != nil {
			log.Printf("netease: %q (%d) unavailable (%v), playing cached Spotify fallback %s", tr.Name, id, neErr, cachedURI)
			return m, nil
		}
	}

	for _, q := range spotify.NetEaseSearchQueries(want) {
		if ctx.Err() != nil {
			break
		}
		results, err := searchSpotifyWithRetry(ctx, q)
		if err != nil {
			continue
		}
		if hit, ok := spotify.MatchCandidateForNetEase(want, results, true); ok {
			uri := hit.URI()
			m, err := resolveSpotifyForNetEase(uri)
			if err == nil && m != nil {
				cache.mu.Lock()
				cache.spotifyFallback[id] = uri
				cache.mu.Unlock()
				log.Printf("netease: %q (%d) unavailable (%v), playing Spotify fallback %s (%s)", tr.Name, id, neErr, hit.Name, uri)
				return m, nil
			}
		}
	}

	if neErr != nil {
		return nil, neErr
	}
	return nil, spotify.ErrNetEaseUnavailable
}

func trackToNetEaseQuery(tr spotify.Track) spotify.NetEasePlaylistTrack {
	artists := make([]string, 0, len(tr.Artists))
	for _, ar := range tr.Artists {
		if name := strings.TrimSpace(ar.Name); name != "" {
			artists = append(artists, name)
		}
	}
	return spotify.NetEasePlaylistTrack{
		ID:       tr.NetEaseID(),
		Name:     tr.Name,
		Artists:  artists,
		Album:    tr.Album.Name,
		PicURL:   tr.Album.PicURL,
		Duration: tr.Duration,
	}
}

func resolveLocalOrCloudFallback(ctx context.Context, hit spotify.Track) (*spotify.Media, error) {
	if hit.Cloud() {
		if cached := localmusic.CachedCloudAudio(hit.ID); cached != "" {
			return &spotify.Media{LocalPath: cached, Duration: hit.Length()}, nil
		}
		if hit.CloudURL == "" {
			return nil, errors.New("empty cloud url")
		}
		streamURL, headers, err := localmusic.ResolveCloudMedia(ctx, hit.CloudURL)
		if err != nil {
			return nil, err
		}
		localmusic.CacheCloudAudioAsync(hit, streamURL, headers)
		return &spotify.Media{File: streamURL, Headers: headers, Duration: hit.Length()}, nil
	}
	if hit.LocalPath != "" {
		if info, err := os.Stat(hit.LocalPath); err == nil && !info.IsDir() {
			return &spotify.Media{LocalPath: hit.LocalPath, Duration: hit.Length()}, nil
		}
	}
	return nil, errors.New("local fallback unreadable")
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
				ui.Text(c, "直接导入网易云歌单。默认走网易云播放，无版权或仅试听时自动切换本地或 Spotify。").FontSize(12).TextColor(p.muted)
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
			ui.Row(c).Padding(12).Gap(10).Radius(12).Background(p.hover).AlignItems(ui.Center).Children(func() {
				ui.Text(c, st.status).FontSize(12.5).FontWeight(500).Grow(1)
				if ui.Button(c.Key("netease-cancel-import"), "取消").Clicked() {
					a.cancelNetEaseImport()
				}
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
