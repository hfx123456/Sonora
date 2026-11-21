package app

import (
	_ "embed"
	"log"
	"runtime"

	"github.com/egoist/mygo"
)

// trayIcon is the full-color badge shown in the Windows notification area;
// trayTemplate is the black-and-alpha template tinted by macOS's menu bar.
//
//go:embed tray.png
var trayIcon []byte

//go:embed tray-template.png
var trayTemplate []byte

// newTray puts the app in the notification area, where it stays while its
// window is closed: a click shows the window, a right click the player's
// controls and a way to quit. It returns nil where there is none.
func (a *app) newTray() *mygo.Tray {
	name := appName
	icon, template := trayIcon, false
	if runtime.GOOS == "darwin" {
		icon, template = trayTemplate, true
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "main", Icon: icon, IconIsTemplate: template, ToolTip: name})
	if err != nil {
		log.Printf("tray: %v", err)
		return nil
	}
	t.OnClick(a.showMain)
	t.OnRightClick(func() {
		run := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
			return func(*mygo.MenuItem, *mygo.Window) {
				fn()
				a.redraw()
			}
		}
		toggle := "播放"
		if a.pl.Playing() {
			toggle = "暂停"
		}
		idle := a.pl.Current() == nil
		lyricsLabel, nextLyricsMode := "关闭桌面歌词", ""
		if a.settings.FusionMode == "" {
			lyricsLabel, nextLyricsMode = "开启顶部胶囊歌词", "capsule"
		}
		lock := "锁定桌面歌词"
		if a.settings.FusionLocked {
			lock = "解锁桌面歌词"
		}
		t.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开 " + name, Click: run(a.showMain)},
			mygo.Separator(),
			{Label: toggle, Disabled: idle, Click: run(func() { a.pl.TogglePlay() })},
			{Label: "上一首", Disabled: idle, Click: run(func() { a.pl.Prev() })},
			{Label: "下一首", Disabled: idle, Click: run(func() { a.pl.Next() })},
			{Label: lyricsLabel, Click: run(func() { a.setFusionMode(nextLyricsMode) })},
			{Label: lock, Disabled: a.settings.FusionMode == "", Click: run(a.toggleFusionLock)},
			mygo.Separator(),
			{Label: "退出 " + name, Click: run(mygo.App.Quit)},
		}))
		// Without a menu, a click reaches OnClick.
		defer t.SetMenu(nil)
		t.PopUpMenu()
	})
	return t
}
