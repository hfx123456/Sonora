package app

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

var chooseLocalSources = func(window *mygo.Window, directory bool) ([]string, error) {
	options := mygo.OpenDialogOptions{Parent: window, Title: "添加本地音乐", Directory: directory, Multiple: true}
	if !directory {
		options.Filters = []mygo.FileFilter{{Name: "音频文件", Extensions: []string{"mp3", "flac", "m4a", "wav", "ogg", "opus", "aac"}}}
	}
	return mygo.Dialog.Open(options)
}

func (a *app) addLocalMusic(directory bool) {
	window := a.win
	go func() {
		paths, err := chooseLocalSources(window, directory)
		a.post(func() {
			if err != nil {
				a.toast = "无法选择音乐文件：" + err.Error()
				return
			}
			if len(paths) == 0 {
				return
			}
			for _, path := range paths {
				if !slices.Contains(a.settings.LocalSources, path) {
					a.settings.LocalSources = append(a.settings.LocalSources, path)
				}
			}
			a.settings.save()
			a.scanLocalMusic()
		})
	}()
}

func (a *app) scanLocalMusic() {
	if a.localLoading {
		a.localAgain = true
		return
	}
	a.localLoading = true
	roots := slices.Clone(a.settings.LocalSources)
	coverDir := ""
	if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
		coverDir = filepath.Join(dir, "local-covers")
	}
	go func() {
		tracks, warnings := localmusic.Scan(roots, coverDir)
		a.post(func() {
			a.localLoading, a.localLoaded = false, true
			a.localTracks, a.localWarnings = tracks, warnings
			spotify.SetLocalTracks(tracks)
			for _, warning := range warnings {
				log.Printf("local music: %s", warning)
			}
			for _, playlist := range a.playlists {
				for index := range playlist.Tracks {
					track := &playlist.Tracks[index]
					if track.LocalURI != "" {
						if matched, ok := spotify.MatchLocal(track.LocalURI); ok {
							matched.UID, matched.LocalURI = track.UID, track.LocalURI
							*track = matched
						}
					}
				}
			}
			if a.localAgain {
				a.localAgain = false
				a.scanLocalMusic()
			}
		})
	}()
}

func (a *app) restoreLocalSession() {
	path, err := sessionPath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var saved player.Session
	if json.Unmarshal(data, &saved) != nil || saved.At < 0 || saved.At >= len(saved.Order) {
		return
	}
	index := saved.Order[saved.At]
	if index >= 0 && index < len(saved.Queue) && saved.Queue[index].Local() {
		a.restoreSession()
	}
}

func (a *app) localMusicPage(c *ui.Context) {
	tracks := cachedFilterTracks(&a.localFilterCache, "local", a.localTracks, a.localQuery)
	a.trackRows(c, tracks, true, func(c *ui.Context) {
		header := ui.Column(c.Key("local-hero")).Children(func() {
			a.heading(c, "本地音乐", "电脑上的歌曲")
			ui.Text(c, "添加音乐文件夹或文件，可直接播放；支持 MP3、FLAC、M4A、WAV、OGG、Opus、AAC 和同名 LRC 歌词。").FontSize(12).TextColor(a.pal.muted).Margin(0, 0, 14, 0)
			ui.Row(c).Gap(10).Margin(0, 0, 14, 0).Children(func() {
				if ui.PrimaryButton(c.Key("add-folder"), "添加文件夹").Clicked() {
					a.addLocalMusic(true)
				}
				if ui.Button(c.Key("add-files"), "添加歌曲").Clicked() {
					a.addLocalMusic(false)
				}
				if ui.Button(c.Key("rescan"), "重新扫描").Disabled(a.localLoading).Clicked() {
					a.scanLocalMusic()
				}
				if len(tracks) > 0 && ui.Button(c.Key("play-local"), "播放全部").Clicked() {
					a.playQueue(tracks, 0)
				}
			})
			for _, source := range slices.Clone(a.settings.LocalSources) {
				ui.Row(c.Key(source)).Gap(10).AlignItems(ui.Center).Margin(0, 0, 6, 0).Children(func() {
					ui.Text(c, source).FontSize(12).TextColor(a.pal.muted).Grow(1).Shrink(1).SingleLine()
					if ui.Button(c.Key("remove"), "移除来源").Clicked() {
						a.settings.LocalSources = slices.DeleteFunc(a.settings.LocalSources, func(path string) bool { return path == source })
						a.settings.save()
						a.scanLocalMusic()
					}
				})
			}
			ui.Text(c, "移除来源只会从音乐库中移除，不会删除原文件。").FontSize(11).TextColor(a.pal.muted).Margin(0, 0, 12, 0)
			status := fmt.Sprintf("%d 首歌曲", len(tracks))
			if a.localLoading {
				status += " · 正在扫描…"
			}
			if len(a.localWarnings) > 0 {
				status += fmt.Sprintf(" · %d 个文件或目录无法读取，详情见日志", len(a.localWarnings))
			}
			if !a.localLoading && len(a.localTracks) == 0 {
				status += " · 添加文件夹或歌曲即可开始"
			}
			ui.Text(c, status).FontSize(12).TextColor(a.pal.muted).Margin(0, 0, 12, 0)
			if len(a.localWarnings) > 0 {
				ui.Text(c, "OneDrive 云端歌曲需先下载到本机：在资源管理器中右键音乐文件夹，选择“始终保留在此设备上”，下载完成后重新扫描。").FontSize(12).TextColor(a.pal.muted).Margin(0, 0, 12, 0)
			}
		})
		if height := header.LayoutBounds().H; height > 0 {
			a.localHeaderHeight = height
		}
		ui.Box(c.Key("local-search-space")).Height(54)
		if len(tracks) == 0 && strings.TrimSpace(a.localQuery) != "" {
			ui.Text(c, "本地音乐中没有匹配的歌曲").FontSize(14).TextColor(a.pal.muted).Padding(30, 12)
		}
	}, nil)
	top, _ := a.insets(c)
	headerHeight := a.localHeaderHeight
	if headerHeight <= 0 {
		headerHeight = 220
	}
	searchTop := top + pageGap + max(0, headerHeight-a.pageScroll.Y)
	ui.Box(c.Key("local-search-toolbar")).Absolute().Top(searchTop).Left(0).Right(0).Height(54).PaddingX(pagePad).Background(a.pal.bg).Children(func() {
		a.localSearch(c, tracks)
	})
}

func (a *app) localSearch(c *ui.Context, tracks []spotify.Track) {
	query := a.localQuery
	ui.Row(c.Key("local-search")).AlignItems(ui.Center).Gap(12).Padding(0, 0, 18, 0).Children(func() {
		field := ui.Row(c.Key("local-search-capsule")).Width(300).Shrink(1).Height(36).PaddingX(12).Gap(8).AlignItems(ui.Center).Radius(18)
		field.Children(func() {
			ui.Icon(c, iconSearch).Size(14, 14).TextColor(a.pal.muted)
			if ui.TextInputBase(c.Key("local-filter"), &query).Bind(&a.localSearchBox).Label("筛选本地音乐").Placeholder("搜索本地音乐").FontSize(12.5).Grow(1).MinWidth(0).Changed() {
				a.localQuery = query
				a.trackList.ScrollTo(0, ui.Start)
			}
			if query != "" && iconButton(c.Key("clear-local-search"), iconX, 11, a.pal.muted, "清除搜索").Clicked() {
				a.localQuery = ""
				a.trackList.ScrollTo(0, ui.Start)
			}
		})
		background, edge := a.pal.hover, ui.Transparent
		if field.FocusWithin() {
			background, edge = a.pal.bg, a.pal.accent.Alpha(.35)
		}
		field.Background(field.AnimateColor("search-bg", background, ui.Animation{Duration: 120 * time.Millisecond})).Border(1, edge)
		if query != "" {
			ui.Text(c, fmt.Sprintf("找到 %d 首", len(tracks))).FontSize(12).TextColor(a.pal.muted)
		}
	})
}

func (a *app) playLocalMatch(match spotify.Track) {
	for index, track := range a.localTracks {
		if track.ID == match.ID {
			a.playQueue(a.localTracks, index)
			return
		}
	}
}
