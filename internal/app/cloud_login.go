package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"vinyl/internal/localmusic"
)

func collectWebViewCookies(urls []string) string {
	seen := map[string]string{}
	order := make([]string, 0, 16)
	for _, u := range urls {
		list, err := mygo.Cookies.List(mygo.CookieFilter{URL: u})
		if err != nil {
			continue
		}
		for _, k := range list {
			if k.Name == "" {
				continue
			}
			if _, ok := seen[k.Name]; !ok {
				order = append(order, k.Name)
			}
			seen[k.Name] = k.Value
		}
	}
	if len(order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(order))
	for _, name := range order {
		parts = append(parts, name+"="+seen[name])
	}
	return strings.Join(parts, "; ")
}

func (a *app) closeCloudLoginWindow() {
	a.cloudLoginGen++
	if a.cloudLoginWin != nil {
		w := a.cloudLoginWin
		a.cloudLoginWin = nil
		w.Close()
	}
	a.cloudFormAuthing = false
}

// openCloudWebLogin opens a native WebView window to pan.quark.cn or 115.com,
// lets the user scan QR / sign in, and automatically captures & verifies the
// Cookie without requiring manual DevTools copying.
func (a *app) openCloudWebLogin(provider string) {
	if a.cloudFormAuthing {
		return
	}
	var title, loginURL, defaultName string
	var cookieURLs []string
	var verify func(context.Context, string) error

	switch provider {
	case localmusic.CloudTypeQuark:
		title = "扫码登录夸克网盘"
		loginURL = "https://pan.quark.cn/"
		defaultName = "夸克网盘"
		cookieURLs = []string{"https://pan.quark.cn/", "https://drive-pc.quark.cn/"}
		verify = testQuarkSource
	case localmusic.CloudType115:
		title = "扫码登录 115 网盘"
		loginURL = "https://115.com/"
		defaultName = "115网盘"
		cookieURLs = []string{"https://115.com/", "https://webapi.115.com/"}
		verify = test115Source
	default:
		return
	}

	a.closeCloudLoginWindow()
	a.cloudLoginGen++
	gen := a.cloudLoginGen
	a.cloudFormAuthing = true
	a.cloudFormErr = ""
	a.cloudFormOK = "请在弹出的窗口中扫码登录，登录成功后将自动连接…"

	customName := strings.TrimSpace(a.cloudFormName)
	folder := strings.TrimSpace(a.cloudFormFolder)

	w := mygo.NewWindow(mygo.WindowOptions{
		Title:  title,
		URL:    loginURL,
		Width:  980,
		Height: 680,
	})
	a.cloudLoginWin = w
	w.OnClose(func(*mygo.CloseEvent) {
		if a.cloudLoginWin == w {
			a.cloudLoginWin = nil
		}
		if a.cloudLoginGen == gen {
			a.cloudLoginGen++
			a.cloudFormAuthing = false
			if a.cloudFormOK != "" && a.cloudFormErr == "" {
				a.cloudFormOK = ""
			}
			a.redraw()
		}
	})
	w.Show()
	w.Focus()

	go func() {
		lastChecked := ""
		deadline := time.Now().Add(5 * time.Minute)
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()

		for time.Now().Before(deadline) {
			<-ticker.C
			var active bool
			mygo.RunOnMain(func() {
				active = !a.quitting && a.cloudLoginGen == gen && a.cloudLoginWin == w
			})
			if !active {
				return
			}

			var cookie string
			mygo.RunOnMain(func() {
				cookie = collectWebViewCookies(cookieURLs)
			})
			if cookie == "" || cookie == lastChecked {
				continue
			}
			lastChecked = cookie

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := verify(ctx, cookie)
			cancel()
			if err != nil {
				continue
			}

			a.post(func() {
				if a.cloudLoginGen != gen {
					return
				}
				a.closeCloudLoginWindow()
				name := cmp.Or(customName, defaultName)
				sum := sha256.Sum256([]byte(provider + "|" + name + "|" + strings.Trim(folder, "/")))
				sealed, plain := sealCloudPassword(cookie)
				entry := cloudSourceSetting{
					ID:            hex.EncodeToString(sum[:8]),
					Type:          provider,
					Name:          name,
					Folder:        folder,
					FolderPending: true,
					SealedPass:    sealed,
					PlainPass:     plain,
				}
				idx := slices.IndexFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
					return existing.ID == entry.ID
				})
				if idx >= 0 {
					a.settings.CloudSources[idx] = entry
				} else {
					a.settings.CloudSources = append(a.settings.CloudSources, entry)
				}
				a.settings.save()
				a.resetCloudForm()
				a.toast = "登录成功，请选择要扫描的音乐文件夹"
				a.openCloudFolderBrowser(entry, "")
			})
			return
		}

		a.post(func() {
			if a.cloudLoginGen == gen {
				a.closeCloudLoginWindow()
				a.cloudFormErr = "等待扫码登录超时，请重试"
				a.cloudFormOK = ""
			}
		})
	}()
}
