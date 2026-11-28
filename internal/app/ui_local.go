package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"vinyl/internal/localmusic"
	"vinyl/internal/player"
	"vinyl/internal/spotify"
)

var (
	testCloudSource     = localmusic.TestCloudSource
	testQuarkSource     = localmusic.TestQuarkSource
	test115Source       = localmusic.Test115Source
	listCloudFolders    = localmusic.ListCloudFolders
	scanCloudSources    = localmusic.ScanCloud
	uploadToCloud       = localmusic.UploadToCloud
	authorizeCloudOAuth = localmusic.AuthorizeCloudOAuth
	openExternalURL     = mygo.Shell.OpenExternal
)

var chooseLocalSources = func(window *mygo.Window, directory bool) ([]string, error) {
	options := mygo.OpenDialogOptions{Parent: window, Title: "添加本地音乐", Directory: directory, Multiple: true}
	if !directory {
		options.Filters = []mygo.FileFilter{{Name: "音频文件", Extensions: []string{"mp3", "flac", "m4a", "wav", "ogg", "opus", "aac"}}}
	}
	return mygo.Dialog.Open(options)
}

func sameLocalSource(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
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
				if !slices.ContainsFunc(a.settings.LocalSources, func(existing string) bool { return sameLocalSource(existing, path) }) {
					a.settings.LocalSources = append(a.settings.LocalSources, path)
				}
			}
			a.settings.save()
			a.scanLocalMusic()
		})
	}()
}

func (a *app) buildCloudFormSource() (localmusic.CloudSource, error) {
	norm, err := localmusic.NormalizeCloudURL(a.cloudFormURL)
	if err != nil {
		return localmusic.CloudSource{}, err
	}
	name := strings.TrimSpace(a.cloudFormName)
	if name == "" {
		name = "WebDAV 云盘"
	}
	sum := sha256.Sum256([]byte(norm))
	return localmusic.CloudSource{
		ID:       hex.EncodeToString(sum[:8]),
		Type:     localmusic.CloudTypeWebDAV,
		Name:     name,
		URL:      norm,
		Username: strings.TrimSpace(a.cloudFormUser),
		Password: a.cloudFormPass,
	}, nil
}

func (a *app) testCloudForm() {
	if a.cloudFormTesting {
		return
	}
	src, err := a.buildCloudFormSource()
	if err != nil {
		a.cloudFormErr, a.cloudFormOK = err.Error(), ""
		return
	}
	a.cloudFormTesting, a.cloudFormErr, a.cloudFormOK = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := testCloudSource(ctx, src)
		a.post(func() {
			a.cloudFormTesting = false
			if err != nil {
				a.cloudFormErr, a.cloudFormOK = err.Error(), ""
				return
			}
			a.cloudFormErr, a.cloudFormOK = "", "连接成功，可以保存并扫描"
		})
	}()
}

func (a *app) saveCloudForm() {
	src, err := a.buildCloudFormSource()
	if err != nil {
		a.cloudFormErr, a.cloudFormOK = err.Error(), ""
		return
	}
	sealed, plain := sealCloudPassword(src.Password)
	entry := cloudSourceSetting{
		ID:         src.ID,
		Type:       localmusic.CloudTypeWebDAV,
		Name:       src.Name,
		URL:        src.URL,
		Username:   src.Username,
		SealedPass: sealed,
		PlainPass:  plain,
	}
	idx := slices.IndexFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
		return existing.effectiveType() == localmusic.CloudTypeWebDAV &&
			strings.EqualFold(strings.TrimRight(existing.URL, "/"), strings.TrimRight(entry.URL, "/"))
	})
	if idx >= 0 {
		a.settings.CloudSources[idx] = entry
	} else {
		a.settings.CloudSources = append(a.settings.CloudSources, entry)
	}
	a.settings.save()
	a.resetCloudForm()
	a.toast = "已添加云盘：" + entry.Name
	a.scanLocalMusic()
}

func (a *app) authorizeOAuthForm(provider string) {
	if a.cloudFormAuthing {
		return
	}
	defaultID, defaultSecret := localmusic.DefaultOAuthCreds(provider)
	clientID := cmp.Or(strings.TrimSpace(a.cloudFormClientID), defaultID)
	clientSecret := cmp.Or(strings.TrimSpace(a.cloudFormSecret), defaultSecret)
	if clientID == "" {
		a.cloudFormAdvanced = true
		a.cloudFormErr = "当前构建未内置默认 Client ID，请在下方「自定义开发者凭据」中填入 Client ID 后登录"
		a.cloudFormOK = ""
		return
	}
	folder := strings.TrimSpace(a.cloudFormFolder)
	if folder == "" {
		switch provider {
		case localmusic.CloudTypeOneDrive:
			folder = "/Music"
		case localmusic.CloudTypeBaidu:
			folder = "/apps/Sonora"
		default:
			folder = "Music"
		}
	}
	customName := strings.TrimSpace(a.cloudFormName)

	a.cloudFormAuthing = true
	a.cloudFormErr = ""
	a.cloudFormOK = "正在打开浏览器，请在浏览器中完成授权登录…"
	ctx, cancel := context.WithCancel(context.Background())
	a.cloudOAuthCancel = cancel
	go func() {
		authCtx, stop := context.WithTimeout(ctx, 3*time.Minute)
		defer stop()
		res, err := authorizeCloudOAuth(authCtx, provider, clientID, clientSecret, openExternalURL)
		a.post(func() {
			defer cancel()
			if ctx.Err() != nil || a.quitting {
				return
			}
			a.cloudOAuthCancel = nil
			a.cloudFormAuthing = false
			if err != nil {
				a.cloudFormErr, a.cloudFormOK = err.Error(), ""
				return
			}
			defaultName := "OneDrive"
			switch provider {
			case localmusic.CloudTypeGoogleDrive:
				defaultName = "Google Drive"
			case localmusic.CloudTypeBaidu:
				defaultName = "百度网盘"
			}
			name := cmp.Or(customName, defaultName)
			sealedRefresh, plainRefresh := sealCloudPassword(res.RefreshToken)
			sealedSecret, plainSecret := sealCloudPassword(clientSecret)
			var entry cloudSourceSetting
			if res.Email != "" {
				key := provider + "|" + strings.ToLower(res.Email) + "|" + strings.Trim(folder, "/")
				sum := sha256.Sum256([]byte(key))
				entry = cloudSourceSetting{
					ID:            hex.EncodeToString(sum[:8]),
					Type:          provider,
					Name:          name,
					Folder:        "",
					FolderPending: true,
					Username:      res.Email,
					ClientID:      clientID,
					SealedSecret:  sealedSecret,
					PlainSecret:   plainSecret,
					SealedRefresh: sealedRefresh,
					PlainRefresh:  plainRefresh,
				}
				idx := slices.IndexFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
					return existing.ID == entry.ID
				})
				if idx >= 0 {
					a.settings.CloudSources[idx] = entry
				} else {
					a.settings.CloudSources = append(a.settings.CloudSources, entry)
				}
			} else {
				tokSum := sha256.Sum256([]byte(res.RefreshToken))
				baseKey := provider + "|" + strings.ToLower(clientID) + "|" + strings.Trim(folder, "/") + "|" + hex.EncodeToString(tokSum[:8])
				id, finalName := a.uniqueCloudIdentity(baseKey, name)
				entry = cloudSourceSetting{
					ID:            id,
					Type:          provider,
					Name:          finalName,
					Folder:        "",
					FolderPending: true,
					Username:      res.Email,
					ClientID:      clientID,
					SealedSecret:  sealedSecret,
					PlainSecret:   plainSecret,
					SealedRefresh: sealedRefresh,
					PlainRefresh:  plainRefresh,
				}
				a.settings.CloudSources = append(a.settings.CloudSources, entry)
			}
			a.settings.save()
			a.resetCloudForm()
			a.toast = "登录成功，请选择要扫描的音乐文件夹"
			a.openCloudFolderBrowser(entry, "")
		})
	}()
}

func (a *app) testQuarkForm() {
	if a.cloudFormTesting {
		return
	}
	cookie := strings.TrimSpace(a.cloudFormPass)
	if cookie == "" {
		a.cloudFormErr, a.cloudFormOK = "请先粘贴夸克网盘 Cookie", ""
		return
	}
	a.cloudFormTesting, a.cloudFormErr, a.cloudFormOK = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := testQuarkSource(ctx, cookie)
		a.post(func() {
			a.cloudFormTesting = false
			if err != nil {
				a.cloudFormErr, a.cloudFormOK = err.Error(), ""
				return
			}
			a.cloudFormErr, a.cloudFormOK = "", "Cookie 验证通过，可以保存并扫描"
		})
	}()
}

func (a *app) saveQuarkForm() {
	cookie := strings.TrimSpace(a.cloudFormPass)
	if cookie == "" {
		a.cloudFormErr, a.cloudFormOK = "请先粘贴夸克网盘 Cookie", ""
		return
	}
	name := cmp.Or(strings.TrimSpace(a.cloudFormName), "夸克网盘")
	folder := strings.TrimSpace(a.cloudFormFolder)
	cookieSum := sha256.Sum256([]byte(cookie))
	id, finalName := a.uniqueCloudIdentity("quark|"+name+"|"+strings.Trim(folder, "/")+"|"+hex.EncodeToString(cookieSum[:8]), name)
	sealed, plain := sealCloudPassword(cookie)
	entry := cloudSourceSetting{
		ID:            id,
		Type:          localmusic.CloudTypeQuark,
		Name:          finalName,
		Folder:        folder,
		FolderPending: folder == "",
		SealedPass:    sealed,
		PlainPass:     plain,
	}
	a.settings.CloudSources = append(a.settings.CloudSources, entry)
	a.settings.save()
	a.resetCloudForm()
	if entry.FolderPending {
		a.toast = "已连接夸克网盘，请选择要扫描的音乐文件夹"
		a.openCloudFolderBrowser(entry, "")
		return
	}
	a.toast = "已添加云盘：" + entry.Name
	a.scanLocalMusic()
}

func (a *app) test115Form() {
	if a.cloudFormTesting {
		return
	}
	cookie := strings.TrimSpace(a.cloudFormPass)
	if cookie == "" {
		a.cloudFormErr, a.cloudFormOK = "请先粘贴 115 网盘 Cookie", ""
		return
	}
	a.cloudFormTesting, a.cloudFormErr, a.cloudFormOK = true, "", ""
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		err := test115Source(ctx, cookie)
		a.post(func() {
			a.cloudFormTesting = false
			if err != nil {
				a.cloudFormErr, a.cloudFormOK = err.Error(), ""
				return
			}
			a.cloudFormErr, a.cloudFormOK = "", "Cookie 验证通过，可以保存并扫描"
		})
	}()
}

func (a *app) save115Form() {
	cookie := strings.TrimSpace(a.cloudFormPass)
	if cookie == "" {
		a.cloudFormErr, a.cloudFormOK = "请先粘贴 115 网盘 Cookie", ""
		return
	}
	name := cmp.Or(strings.TrimSpace(a.cloudFormName), "115网盘")
	folder := strings.TrimSpace(a.cloudFormFolder)
	cookieSum := sha256.Sum256([]byte(cookie))
	id, finalName := a.uniqueCloudIdentity("115|"+name+"|"+strings.Trim(folder, "/")+"|"+hex.EncodeToString(cookieSum[:8]), name)
	sealed, plain := sealCloudPassword(cookie)
	entry := cloudSourceSetting{
		ID:            id,
		Type:          localmusic.CloudType115,
		Name:          finalName,
		Folder:        folder,
		FolderPending: folder == "",
		SealedPass:    sealed,
		PlainPass:     plain,
	}
	a.settings.CloudSources = append(a.settings.CloudSources, entry)
	a.settings.save()
	a.resetCloudForm()
	if entry.FolderPending {
		a.toast = "已连接 115 网盘，请选择要扫描的音乐文件夹"
		a.openCloudFolderBrowser(entry, "")
		return
	}
	a.toast = "已添加云盘：" + entry.Name
	a.scanLocalMusic()
}

func (a *app) resetCloudForm() {
	a.cancelCloudOAuth()
	a.cloudFormOpen = false
	a.cloudFormAdvanced = false
	a.cloudFormName, a.cloudFormURL, a.cloudFormFolder = "", "", ""
	a.cloudFormUser, a.cloudFormPass = "", ""
	a.cloudFormClientID, a.cloudFormSecret = "", ""
	a.cloudFormErr, a.cloudFormOK = "", ""
}

func (a *app) cancelCloudOAuth() {
	if a.cloudOAuthCancel != nil {
		a.cloudOAuthCancel()
		a.cloudOAuthCancel = nil
		a.cloudFormAuthing = false
	}
}

func (a *app) uniqueCloudIdentity(baseKey, name string) (string, string) {
	sum := sha256.Sum256([]byte(baseKey))
	id := hex.EncodeToString(sum[:8])
	idExists := func(cand string) bool {
		return slices.ContainsFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
			return existing.ID == cand
		})
	}
	for idExists(id) {
		sum = sha256.Sum256([]byte(baseKey + "|" + randomHexID(8)))
		id = hex.EncodeToString(sum[:8])
	}
	finalName := name
	nameExists := func(cand string) bool {
		return slices.ContainsFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
			return existing.Name == cand
		})
	}
	if nameExists(finalName) {
		for n := 2; ; n++ {
			cand := fmt.Sprintf("%s %d", name, n)
			if !nameExists(cand) {
				finalName = cand
				break
			}
		}
	}
	return id, finalName
}

func (a *app) registerCloudTokenPersistence() {
	localmusic.SetCloudCallbacks(
		func(sourceID, newRefreshToken string) {
			a.post(func() {
				// Drop stale out-of-order rotation callbacks if a newer token
				// or re-authorization has already replaced this source in memory.
				if cur := localmusic.CurrentOAuthRefreshToken(sourceID); cur != "" && cur != newRefreshToken {
					return
				}
				for i := range a.settings.CloudSources {
					if a.settings.CloudSources[i].ID == sourceID {
						sealed, plain := sealCloudPassword(newRefreshToken)
						a.settings.CloudSources[i].SealedRefresh = sealed
						a.settings.CloudSources[i].PlainRefresh = plain
						a.settings.save()
						break
					}
				}
			})
		},
		func(enriched spotify.Track) {
			a.post(func() {
				a.replaceCloudTrack(enriched)
			})
		},
	)
}

func (a *app) replaceCloudTrack(enriched spotify.Track) {
	if enriched.ID == "" {
		return
	}
	for i := range a.localTracks {
		if a.localTracks[i].ID == enriched.ID {
			a.localTracks[i] = enriched
			a.localOrderCache = playlistOrderCache{}
			a.localScopeCache = localScopeCache{}
			a.localFilterCache = trackFilterCache{}
			spotify.SetLocalTracks(a.localTracks)
			return
		}
	}
}

func (a *app) toggleCacheAllCloud() {
	a.cloudSyncGen++
	gen := a.cloudSyncGen
	if a.cloudSyncing {
		if a.cloudSyncCancel != nil {
			a.cloudSyncCancel()
			a.cloudSyncCancel = nil
		}
		a.cloudSyncing = false
		a.toast = "已停止云盘歌曲缓存"
		return
	}
	tracks := slices.Clone(a.localTracks)
	coverDir, cachePath := "", ""
	if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
		coverDir = filepath.Join(dir, "local-covers")
		cachePath = filepath.Join(dir, "cloud-index.json")
		localmusic.SetCloudCacheDir(filepath.Join(dir, "cloud-audio"))
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cloudSyncing = true
	a.cloudSyncDone = 0
	a.cloudSyncSucceeded = 0
	a.cloudSyncFailed = 0
	a.cloudSyncLastFailed = 0
	a.cloudSyncTotal = 0
	a.cloudSyncCancel = cancel
	a.toast = "开始在后台缓存云盘歌曲并补全封面…"
	go func() {
		defer cancel()
		stats := localmusic.CacheAllCloudTracks(ctx, tracks, coverDir, cachePath, func(done, succeeded, failed, total int, enriched *spotify.Track) {
			a.post(func() {
				if gen != a.cloudSyncGen || a.quitting {
					return
				}
				a.cloudSyncDone = done
				a.cloudSyncSucceeded = succeeded
				a.cloudSyncFailed = failed
				a.cloudSyncTotal = total
				if enriched != nil {
					a.replaceCloudTrack(*enriched)
				}
			})
		})
		a.post(func() {
			if gen != a.cloudSyncGen || a.quitting {
				return
			}
			a.cloudSyncing = false
			a.cloudSyncCancel = nil
			a.cloudSyncLastFailed = stats.Failed
			a.refreshHybridPlaylists()
			switch {
			case stats.Failed > 0:
				a.toast = fmt.Sprintf("已缓存 %d 首，%d 首失败", stats.Succeeded, stats.Failed)
			case stats.Total == 0:
				a.toast = "云盘歌曲已全部缓存到本地，之后将直接读取本地文件"
			default:
				a.toast = fmt.Sprintf("已缓存 %d 首云盘歌曲到本地，之后将直接读取本地文件", stats.Succeeded)
			}
		})
	}()
}

func (a *app) uploadTrackToCloud(tr spotify.Track, dst cloudSourceSetting) {
	if a.cloudUploading || tr.LocalPath == "" {
		return
	}
	a.cloudUploading = true
	a.toast = fmt.Sprintf("正在上传「%s」到 %s…", tr.Name, cmp.Or(dst.Name, "云盘"))
	src := dst.toCloudSource()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		_, err := uploadToCloud(ctx, tr.LocalPath, src)
		a.post(func() {
			a.cloudUploading = false
			if err != nil {
				a.toast = "上传失败：" + err.Error()
				return
			}
			a.toast = fmt.Sprintf("已上传「%s」到 %s", tr.Name, cmp.Or(dst.Name, "云盘"))
			a.scanLocalMusic()
		})
	}()
}

func (a *app) hideLocalTrack(tr spotify.Track) {
	if tr.ID == "" {
		return
	}
	if !slices.Contains(a.settings.HiddenLocalIDs, tr.ID) {
		a.settings.HiddenLocalIDs = append(a.settings.HiddenLocalIDs, tr.ID)
		a.settings.save()
	}
	if tr.Cloud() {
		localmusic.DeleteCachedCloudAudio(tr.ID)
	}
	a.localTracks = slices.DeleteFunc(slices.Clone(a.localTracks), func(existing spotify.Track) bool {
		return existing.ID == tr.ID
	})
	spotify.SetLocalTracks(a.localTracks)
	a.toast = fmt.Sprintf("已从音乐库移除「%s」（已清理本地缓存，未删除原文件）", tr.Name)
}

func (a *app) restoreHiddenLocalTracks() {
	if len(a.settings.HiddenLocalIDs) == 0 {
		return
	}
	a.settings.HiddenLocalIDs = nil
	a.settings.save()
	a.toast = "已恢复所有移除的本地与云盘歌曲"
	a.scanLocalMusic()
}

func (a *app) openCloudFolderBrowser(src cloudSourceSetting, folderPath string) {
	a.cloudBrowseID = src.ID
	a.cloudBrowsePath = strings.Trim(strings.TrimSpace(folderPath), "/")
	a.cloudBrowseSelected = ""
	a.cloudBrowseClickPath = ""
	a.cloudBrowseLoading = true
	a.cloudBrowseErr = ""
	a.cloudBrowseDirs = nil
	a.cloudBrowseGen++
	gen := a.cloudBrowseGen
	cloudSrc := src.toCloudSource()
	browsePath := a.cloudBrowsePath
	localmusic.SetCloudSources(a.settings.allCloudSources())

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		dirs, err := listCloudFolders(ctx, cloudSrc, browsePath)
		a.post(func() {
			if a.cloudBrowseGen != gen {
				return
			}
			a.cloudBrowseLoading = false
			if err != nil {
				a.cloudBrowseErr = "读取目录失败：" + err.Error()
				return
			}
			a.cloudBrowseDirs = dirs
		})
	}()
}

func (a *app) selectCloudFolder(sourceID, folderPath string) {
	folder := strings.Trim(strings.TrimSpace(folderPath), "/")
	for i := range a.settings.CloudSources {
		if a.settings.CloudSources[i].ID == sourceID {
			if folder != "" && (a.settings.CloudSources[i].effectiveType() == localmusic.CloudTypeOneDrive || a.settings.CloudSources[i].effectiveType() == localmusic.CloudTypeBaidu) {
				folder = "/" + folder
			}
			a.settings.CloudSources[i].Folder = folder
			a.settings.CloudSources[i].FolderPending = false
			a.settings.save()
			a.cloudBrowseID = ""
			a.cloudBrowseDirs = nil
			a.toast = "已选择目录，正在重新扫描…"
			a.scanLocalMusic()
			return
		}
	}
	a.cloudBrowseID = ""
}

func parentCloudPath(p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return ""
}

func (a *app) applyScannedTracks(tracks []spotify.Track, warnings []string, done bool) {
	if done {
		a.localLoading, a.localLoaded = false, true
	} else {
		a.localLoaded = true
	}
	if len(a.settings.HiddenLocalIDs) > 0 {
		hidden := make(map[string]bool, len(a.settings.HiddenLocalIDs))
		for _, id := range a.settings.HiddenLocalIDs {
			hidden[id] = true
		}
		filtered := make([]spotify.Track, 0, len(tracks))
		for _, tr := range tracks {
			if !hidden[tr.ID] {
				filtered = append(filtered, tr)
			}
		}
		tracks = filtered
	}
	a.localTracks, a.localWarnings = tracks, warnings
	a.localOrderCache = playlistOrderCache{}
	a.localScopeCache = localScopeCache{}
	a.localFilterCache = trackFilterCache{}
	a.cloudOffline = false
	for _, w := range warnings {
		if strings.Contains(w, "离线曲库") {
			a.cloudOffline = true
			break
		}
	}
	spotify.SetLocalTracks(tracks)
	for _, warning := range warnings {
		log.Printf("local music: %s", warning)
	}
	for _, playlist := range a.playlists {
		var updated []spotify.Track
		for index := range playlist.Tracks {
			track := playlist.Tracks[index]
			if track.LocalURI == "" {
				continue
			}
			if matched, ok := spotify.MatchLocal(track.LocalURI); ok {
				matched.UID, matched.LocalURI = track.UID, track.LocalURI
				if updated == nil {
					updated = slices.Clone(playlist.Tracks)
				}
				updated[index] = matched
			} else if done && (track.LocalPath != "" || track.CloudURL != "") {
				track.LocalPath = ""
				track.CloudURL = ""
				track.CloudLyricsURL = ""
				track.CloudSource = ""
				track.LocalLyrics = ""
				track.LocalError = ""
				track.Unplayable = true
				if updated == nil {
					updated = slices.Clone(playlist.Tracks)
				}
				updated[index] = track
			}
		}
		if updated != nil {
			playlist.Tracks = updated
		}
	}
	a.refreshHybridPlaylists()
}

func (a *app) scanLocalMusic() {
	if a.localScanCancel != nil {
		a.localScanCancel()
		a.localScanCancel = nil
	}
	a.localScanGen++
	gen := a.localScanGen
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	a.localScanCancel = cancel
	a.localLoading = true

	a.registerCloudTokenPersistence()
	roots := slices.Clone(a.settings.LocalSources)
	cloudSources := a.settings.cloudSources()
	localmusic.SetCloudSources(cloudSources)
	coverDir, cachePath := "", ""
	if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
		coverDir = filepath.Join(dir, "local-covers")
		cachePath = filepath.Join(dir, "cloud-index.json")
		localmusic.SetCloudCacheDir(filepath.Join(dir, "cloud-audio"))
	}

	go func() {
		defer cancel()

		var localTracks []spotify.Track
		var localWarns []string
		if len(roots) > 0 {
			localTracks, localWarns = localmusic.Scan(roots, coverDir)
		}

		if len(cloudSources) == 0 {
			a.post(func() {
				if a.localScanGen != gen {
					return
				}
				a.localScanCancel = nil
				a.applyScannedTracks(localTracks, localWarns, true)
			})
			return
		}

		// Show local tracks + cached cloud tracks belonging to active sources
		// immediately (< 10ms) while refreshing cloud sources in the background.
		cachedCloud := localmusic.LoadCachedCloud(cachePath, cloudSources)
		if len(localTracks) > 0 || len(cachedCloud) > 0 || len(localWarns) > 0 {
			initial := make([]spotify.Track, 0, len(localTracks)+len(cachedCloud))
			initial = append(initial, localTracks...)
			initial = append(initial, cachedCloud...)
			lw := slices.Clone(localWarns)
			a.post(func() {
				if a.localScanGen != gen {
					return
				}
				a.applyScannedTracks(initial, lw, false)
			})
		}

		cloudTracks, cloudWarns := scanCloudSources(ctx, cloudSources, coverDir, cachePath)
		if len(cloudTracks) == 0 && len(cloudWarns) > 0 && len(cachedCloud) > 0 {
			cloudTracks = cachedCloud
			hasOfflineNotice := false
			for _, w := range cloudWarns {
				if strings.Contains(w, "离线曲库") {
					hasOfflineNotice = true
					break
				}
			}
			if !hasOfflineNotice {
				cloudWarns = append(cloudWarns, "云盘刷新失败，当前显示离线曲库")
			}
		}
		tracks := make([]spotify.Track, 0, len(localTracks)+len(cloudTracks))
		tracks = append(tracks, localTracks...)
		tracks = append(tracks, cloudTracks...)
		warnings := make([]string, 0, len(localWarns)+len(cloudWarns))
		warnings = append(warnings, localWarns...)
		warnings = append(warnings, cloudWarns...)

		a.post(func() {
			if a.localScanGen != gen {
				return
			}
			a.localScanCancel = nil
			a.applyScannedTracks(tracks, warnings, true)
		})
	}()
}

func (a *app) restoreLocalSession() {
	// Register cloud credentials and cache dir early so a restored cloud track can play immediately.
	a.registerCloudTokenPersistence()
	localmusic.SetCloudSources(a.settings.cloudSources())
	if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
		localmusic.SetCloudCacheDir(filepath.Join(dir, "cloud-audio"))
	}
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

type localScopeCache struct {
	source []spotify.Track
	scope  int
	tracks []spotify.Track
}

func (a *app) filterLocalScope(ordered []spotify.Track) []spotify.Track {
	if a.localScope == 0 {
		return ordered
	}
	c := &a.localScopeCache
	sameSource := len(c.source) == len(ordered) && (len(ordered) == 0 || &c.source[0] == &ordered[0])
	if sameSource && c.scope == a.localScope && c.tracks != nil {
		return c.tracks
	}
	out := make([]spotify.Track, 0, len(ordered))
	for _, tr := range ordered {
		if a.localScope == 1 && !tr.Cloud() {
			out = append(out, tr)
		} else if a.localScope == 2 && tr.Cloud() {
			out = append(out, tr)
		}
	}
	c.source, c.scope, c.tracks = ordered, a.localScope, out
	return out
}

func cloudSourceBadgeAndDesc(source cloudSourceSetting) (badge, desc string) {
	switch source.effectiveType() {
	case localmusic.CloudTypeOneDrive:
		badge = "OneDrive"
		desc = cmp.Or(source.Name, "OneDrive")
		if source.Username != "" {
			desc += " · " + source.Username
		}
		if source.Folder != "" {
			desc += " · " + source.Folder
		}
	case localmusic.CloudTypeGoogleDrive:
		badge = "Google"
		desc = cmp.Or(source.Name, "Google Drive")
		if source.Username != "" {
			desc += " · " + source.Username
		}
		if source.Folder != "" {
			desc += " · " + source.Folder
		}
	case localmusic.CloudTypeBaidu:
		badge = "百度网盘"
		desc = cmp.Or(source.Name, "百度网盘")
		if source.Username != "" {
			desc += " · " + source.Username
		}
		if source.Folder != "" {
			desc += " · " + source.Folder
		}
	case localmusic.CloudTypeQuark:
		badge = "夸克"
		desc = cmp.Or(source.Name, "夸克网盘")
		if source.Folder != "" {
			desc += " · " + source.Folder
		}
	case localmusic.CloudType115:
		badge = "115"
		desc = cmp.Or(source.Name, "115网盘")
		if source.Folder != "" {
			desc += " · " + source.Folder
		}
	default:
		badge = "WebDAV"
		desc = source.URL
		if source.Name != "" {
			desc = source.Name + " · " + source.URL
		}
	}
	if source.FolderPending {
		desc += " · 待选择音乐目录"
	}
	return badge, desc
}

func (a *app) localMusicPage(c *ui.Context) {
	ordered := a.orderedLocalTracks()
	hasLocal, hasCloud := false, false
	for _, tr := range ordered {
		if tr.Cloud() {
			hasCloud = true
		} else {
			hasLocal = true
		}
	}
	if !hasLocal || !hasCloud {
		a.localScope = 0
	}
	scoped := a.filterLocalScope(ordered)
	scopeKey := fmt.Sprintf("local:%d", a.localScope)
	tracks := cachedFilterTracks(&a.localFilterCache, scopeKey, scoped, a.localQuery)
	list := a.trackRows(c, tracks, a.settings.PlaylistTrackCovers, func(c *ui.Context) {
		header := ui.Column(c.Key("local-hero")).PaddingX(12).Children(func() {
			a.heading(c, "本地与云盘音乐", "电脑与云盘中的歌曲")
			ui.Text(c, "支持本机文件夹、OneDrive、Google Drive、115网盘、百度网盘、夸克网盘及 WebDAV（坚果云 / 123云盘 / Alist / NAS），免全量下载即可扫描并流式播放。").
				FontSize(12).TextColor(a.pal.muted).LineHeight(1.4).Margin(0, 0, 14, 0)
			ui.Row(c).Gap(10).Margin(0, 0, 14, 0).Children(func() {
				if ui.PrimaryButton(c.Key("add-folder"), "添加文件夹").Clicked() {
					a.addLocalMusic(true)
				}
				if ui.Button(c.Key("add-files"), "添加歌曲").Clicked() {
					a.addLocalMusic(false)
				}
				cloudBtnLabel := "添加云盘"
				if a.cloudFormOpen {
					cloudBtnLabel = "收起云盘设置"
				}
				if ui.Button(c.Key("toggle-cloud-form"), cloudBtnLabel).Clicked() {
					if a.cloudFormOpen {
						a.closeCloudLoginWindow()
						a.resetCloudForm()
					} else {
						a.cloudFormOpen = true
					}
					a.cloudFormErr, a.cloudFormOK = "", ""
				}
				if ui.Button(c.Key("rescan"), "重新扫描").Clicked() {
					a.scanLocalMusic()
				}
				if hasCloud {
					cacheLabel := "全部缓存到本地"
					if a.cloudSyncing {
						if a.cloudSyncTotal > 0 {
							if a.cloudSyncFailed > 0 {
								cacheLabel = fmt.Sprintf("停止缓存 (%d/%d · 失败 %d)", a.cloudSyncDone, a.cloudSyncTotal, a.cloudSyncFailed)
							} else {
								cacheLabel = fmt.Sprintf("停止缓存 (%d/%d)", a.cloudSyncDone, a.cloudSyncTotal)
							}
						} else {
							cacheLabel = "停止缓存…"
						}
					} else if a.cloudSyncLastFailed > 0 {
						cacheLabel = fmt.Sprintf("重试失败项 (%d)", a.cloudSyncLastFailed)
					}
					if ui.Button(c.Key("cache-all-cloud"), cacheLabel).Clicked() {
						a.toggleCacheAllCloud()
					}
				}
				if len(tracks) > 0 && ui.Button(c.Key("play-local"), "播放全部").Clicked() {
					a.playQueue(tracks, firstPlayable(tracks))
				}
			})

			if a.cloudFormOpen {
				a.cloudSourceForm(c)
			}

			for _, source := range slices.Clone(a.settings.CloudSources) {
				badge, desc := cloudSourceBadgeAndDesc(source)
				ui.Column(c.Key("cloud:"+source.ID)).Gap(6).Margin(0, 0, 6, 0).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, badge).FontSize(11).FontWeight(600).TextColor(a.pal.accent).
							Background(a.pal.accent.Alpha(0.14)).Radius(4).Padding(2, 6)
						ui.Text(c, desc).FontSize(12).TextColor(a.pal.muted).Grow(1).Shrink(1).SingleLine()
						if source.effectiveType() != localmusic.CloudTypeWebDAV {
							btnText := "选择目录"
							if a.cloudBrowseID == source.ID {
								btnText = "收起目录"
							}
							if ui.Button(c.Key("pick-cloud-dir"), btnText).Clicked() {
								if a.cloudBrowseID == source.ID {
									a.cloudBrowseID = ""
								} else {
									a.openCloudFolderBrowser(source, source.Folder)
								}
							}
						}
						if ui.Button(c.Key("remove-cloud"), "移除云盘").Clicked() {
							if a.cloudBrowseID == source.ID {
								a.cloudBrowseID = ""
							}
							if a.cloudSyncing && a.cloudSyncCancel != nil {
								a.cloudSyncGen++
								a.cloudSyncCancel()
								a.cloudSyncCancel = nil
								a.cloudSyncing = false
							}
							// Clean up locally cached audio files for this cloud source.
							if dir, err := mygo.App.Path(mygo.PathCache); err == nil {
								cached := localmusic.LoadCachedCloud(filepath.Join(dir, "cloud-index.json"), []localmusic.CloudSource{{ID: source.ID}})
								for _, tr := range cached {
									localmusic.DeleteCachedCloudAudio(tr.ID)
								}
							}
							a.settings.CloudSources = slices.DeleteFunc(a.settings.CloudSources, func(existing cloudSourceSetting) bool {
								return existing.ID == source.ID && existing.URL == source.URL
							})
							a.settings.save()
							a.scanLocalMusic()
						}
					})
					if a.cloudBrowseID == source.ID {
						a.cloudFolderBrowser(c, source)
					}
				})
			}

			for _, source := range slices.Clone(a.settings.LocalSources) {
				ui.Row(c.Key(source)).Gap(10).AlignItems(ui.Center).Margin(0, 0, 6, 0).Children(func() {
					ui.Text(c, "本机").FontSize(11).FontWeight(600).TextColor(a.pal.muted).
						Background(a.pal.hover).Radius(4).Padding(2, 6)
					ui.Text(c, source).FontSize(12).TextColor(a.pal.muted).Grow(1).Shrink(1).SingleLine()
					if ui.Button(c.Key("remove"), "移除来源").Clicked() {
						a.settings.LocalSources = slices.DeleteFunc(a.settings.LocalSources, func(path string) bool { return path == source })
						a.settings.save()
						a.scanLocalMusic()
					}
				})
			}

			if len(a.settings.LocalSources) > 0 || len(a.settings.CloudSources) > 0 {
				ui.Text(c, "移除来源只会从音乐库中移除，不会删除原文件或云端文件。").FontSize(11).TextColor(a.pal.muted).Margin(0, 0, 10, 0)
			}

			if hasLocal && hasCloud {
				ui.Row(c.Key("local-scope-row")).AlignItems(ui.Center).Gap(10).Margin(0, 0, 10, 0).Children(func() {
					if segmented(c.Key("local-scope"), &a.localScope, "全部", "仅本机", "仅云盘") {
						a.trackList.ScrollTo(0, ui.Start)
					}
				})
			}

			status := fmt.Sprintf("%d 首歌曲", len(tracks))
			if a.localLoading {
				status += " · 正在扫描…"
			}
			if a.cloudUploading {
				status += " · 正在上传到云盘…"
			}
			if a.cloudOffline {
				status += " · 当前显示离线曲库"
			}
			if len(a.localWarnings) > 0 {
				status += fmt.Sprintf(" · %d 个文件或目录无法读取，详情见日志", len(a.localWarnings))
			}
			if !a.localLoading && len(a.localTracks) == 0 {
				status += " · 添加本机文件夹或云盘即可开始"
			}
			ui.Row(c.Key("local-status-row")).Gap(10).AlignItems(ui.Center).Margin(0, 0, 12, 0).Children(func() {
				ui.Text(c, status).FontSize(12).TextColor(a.pal.muted)
				if len(a.settings.HiddenLocalIDs) > 0 {
					ui.Text(c, fmt.Sprintf("· 已隐藏 %d 首", len(a.settings.HiddenLocalIDs))).FontSize(12).TextColor(a.pal.muted)
					if ui.Button(c.Key("restore-hidden"), "恢复已移除").Clicked() {
						a.restoreHiddenLocalTracks()
					}
				}
			})
			if len(a.localWarnings) > 0 && hasLocal {
				ui.Text(c, "OneDrive 本机占位文件可右键选择“始终保留在此设备上”，或直接通过上方「添加云盘 → OneDrive」在线流式播放。").
					FontSize(12).TextColor(a.pal.muted).Margin(0, 0, 12, 0)
			}
		})
		if height := header.LayoutBounds().H; height > 0 && height != a.localHeaderHeight {
			a.localHeaderHeight = height
			c.AnimationFrame()
		}
		ui.Box(c.Key("local-search-space")).Height(playlistToolbarHeight)
		if len(tracks) == 0 && strings.TrimSpace(a.localQuery) != "" {
			ui.Text(c, "本地与云盘音乐中没有匹配的歌曲").FontSize(14).TextColor(a.pal.muted).Padding(30, 12)
		}
	}, nil)
	headerHeight := a.localHeaderHeight
	if headerHeight <= 0 {
		headerHeight = 240
	}
	a.pinTrackToolbar(c, list, "local-search-toolbar", headerHeight, func() {
		ui.Box(c).PaddingX(12).Children(func() { a.localSearch(c, tracks) })
		a.trackColumns(c, false, a.settings.PlaylistTrackCovers)
	})
}

func (a *app) cloudFolderBrowser(c *ui.Context, source cloudSourceSetting) {
	ui.Column(c.Key("cloud-folder-browser")).
		Background(a.pal.sidebar).
		Border(1, a.pal.hairline).
		Radius(8).
		Padding(12, 14).
		Gap(8).
		Children(func() {
			curLabel := "根目录"
			if a.cloudBrowsePath != "" {
				curLabel = "/" + a.cloudBrowsePath
			}
			targetPath := a.cloudBrowsePath
			confirmLabel := "选择当前目录"
			if a.cloudBrowseSelected != "" {
				targetPath = a.cloudBrowseSelected
				name := a.cloudBrowseSelected
				if i := strings.LastIndexByte(name, '/'); i >= 0 {
					name = name[i+1:]
				}
				confirmLabel = "选择「" + name + "」"
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "当前位置："+curLabel).FontSize(12).FontWeight(600).Grow(1).Shrink(1).SingleLine()
				if a.cloudBrowsePath != "" {
					if ui.Button(c.Key("browse-up"), "返回上一级").Disabled(a.cloudBrowseLoading).Clicked() {
						a.openCloudFolderBrowser(source, parentCloudPath(a.cloudBrowsePath))
					}
				}
				if ui.PrimaryButton(c.Key("browse-select"), confirmLabel).Disabled(a.cloudBrowseLoading).Clicked() {
					a.selectCloudFolder(source.ID, targetPath)
				}
				if ui.Button(c.Key("browse-cancel"), "取消").Clicked() {
					a.cloudBrowseID = ""
				}
			})

			if a.cloudBrowseLoading {
				ui.Text(c, "正在读取云盘文件夹…").FontSize(12).TextColor(a.pal.muted)
				return
			}
			if a.cloudBrowseErr != "" {
				ui.Text(c, a.cloudBrowseErr).FontSize(12).TextColor(ui.Hex("#e5484d"))
				return
			}
			if len(a.cloudBrowseDirs) == 0 {
				ui.Text(c, "此目录下没有子文件夹，可直接点击上方「选择当前目录」。").FontSize(12).TextColor(a.pal.muted)
				return
			}
			ui.Text(c, "单击选中文件夹，双击进入下一级目录。").FontSize(11).TextColor(a.pal.muted)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, dir := range a.cloudBrowseDirs {
					selected := a.cloudBrowseSelected == dir.Path
					var btn ui.Element
					if selected {
						btn = ui.PrimaryButton(c.Key("dir:"+dir.Path), "📁 "+dir.Name)
					} else {
						btn = ui.Button(c.Key("dir:"+dir.Path), "📁 "+dir.Name)
					}
					if btn.Clicked() {
						now := time.Now()
						if a.cloudBrowseClickPath == dir.Path && now.Sub(a.cloudBrowseClickAt) < 450*time.Millisecond {
							a.openCloudFolderBrowser(source, dir.Path)
						} else {
							a.cloudBrowseSelected = dir.Path
							a.cloudBrowseClickPath = dir.Path
							a.cloudBrowseClickAt = now
						}
					}
				}
			})
		})
}

func (a *app) cloudSourceForm(c *ui.Context) {
	ui.Column(c.Key("cloud-form-card")).
		Background(a.pal.sidebar).
		Border(1, a.pal.hairline).
		Radius(10).
		Padding(14, 16).
		Gap(10).
		Margin(0, 0, 14, 0).
		Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
				ui.Text(c, "连接音乐云盘").FontSize(13.5).FontWeight(600)
				if segmented(c.Key("cloud-provider-tab"), &a.cloudFormTab, "WebDAV", "OneDrive", "Google Drive", "百度网盘", "夸克网盘", "115网盘") {
					a.closeCloudLoginWindow()
					a.resetCloudForm()
					a.cloudFormOpen = true
				}
			})

			switch a.cloudFormTab {
			case 1:
				defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeOneDrive)
				ui.Text(c, "支持个人 OneDrive 与 Microsoft 365（免费，不绑卡）。申请：Azure 应用注册 → 新注册 → 选「任何组织目录和个人 Microsoft 账户」→ 平台选「公共客户端/本机」填 http://127.0.0.1 → 填入客户端 ID 授权，登录后可随时「修改目录」。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				if defID == "" || a.cloudFormAdvanced {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Client ID").FontSize(12).TextColor(a.pal.muted).Width(56)
						ui.TextInput(c.Key("od-client-id"), &a.cloudFormClientID).Placeholder("填入 Azure 应用注册的应用程序(客户端) ID").Label("OneDrive Client ID").Grow(1)
					})
				}
			case 2:
				defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeGoogleDrive)
				ui.Text(c, "免费使用：Google Cloud Console 启用 Google Drive API → OAuth 同意屏幕添加自己的 Gmail → 创建「桌面应用」OAuth 凭据填入后授权，登录后可随时「修改目录」。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				if defID == "" || a.cloudFormAdvanced {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Client ID").FontSize(12).TextColor(a.pal.muted).Width(56)
						ui.TextInput(c.Key("gd-client-id"), &a.cloudFormClientID).Placeholder("Google 桌面应用 Client ID").Label("Google Client ID").Grow(1)
						ui.Text(c, "Secret").FontSize(12).TextColor(a.pal.muted).Width(46)
						ui.TextInput(c.Key("gd-secret"), &a.cloudFormSecret).Password().Placeholder("Client Secret").Label("Google Client Secret").Grow(1)
					})
				}
			case 3:
				defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeBaidu)
				ui.Text(c, "免费使用：在百度网盘开放平台创建应用（回调地址填 http://127.0.0.1），填入 AppKey 与 SecretKey 授权，登录后可随时「修改目录」。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				if defID == "" || a.cloudFormAdvanced {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "AppKey").FontSize(12).TextColor(a.pal.muted).Width(56)
						ui.TextInput(c.Key("bd-client-id"), &a.cloudFormClientID).Placeholder("百度开放平台 AppKey").Label("百度 AppKey").Grow(1)
						ui.Text(c, "Secret").FontSize(12).TextColor(a.pal.muted).Width(46)
						ui.TextInput(c.Key("bd-secret"), &a.cloudFormSecret).Password().Placeholder("SecretKey").Label("百度 SecretKey").Grow(1)
					})
				}
			case 4:
				ui.Text(c, "点击下方「扫码登录夸克网盘」在弹出窗口扫码即可自动连接；登录成功后可随时点「修改目录」指定音乐文件夹。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				if a.cloudFormAdvanced {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Cookie").FontSize(12).TextColor(a.pal.muted).Width(56)
						ui.TextInput(c.Key("qk-cookie"), &a.cloudFormPass).Password().Placeholder("手动粘贴 pan.quark.cn Cookie").Label("夸克 Cookie").Grow(1)
					})
				}
			case 5:
				ui.Text(c, "点击下方「扫码登录 115 网盘」在弹出窗口扫码即可自动连接；登录成功后可随时点「修改目录」指定音乐文件夹。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				if a.cloudFormAdvanced {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "Cookie").FontSize(12).TextColor(a.pal.muted).Width(56)
						ui.TextInput(c.Key("p115-cookie"), &a.cloudFormPass).Password().Placeholder("手动粘贴 115.com Cookie（含 UID / CID / SEID）").Label("115 Cookie").Grow(1)
					})
				}
			default:
				ui.Text(c, "支持坚果云、123云盘、群晖/飞牛 NAS，或通过 Alist 挂载阿里云盘/夸克/百度网盘；密码使用系统钥匙串加密保存。").
					FontSize(11.5).TextColor(a.pal.muted).LineHeight(1.4)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "快捷预设").FontSize(12).TextColor(a.pal.muted).Width(56)
					if ui.Button(c.Key("preset-jianguoyun"), "坚果云").Clicked() {
						a.cloudFormName = "坚果云"
						a.cloudFormURL = "https://dav.jianguoyun.com/dav/Music"
					}
					if ui.Button(c.Key("preset-123pan"), "123云盘").Clicked() {
						a.cloudFormName = "123云盘"
						a.cloudFormURL = "https://webdav.123pan.cn/webdav/Music"
					}
					if ui.Button(c.Key("preset-alist"), "Alist 本机").Clicked() {
						a.cloudFormName = "Alist 云盘"
						a.cloudFormURL = "http://127.0.0.1:5244/dav/Music"
					}
				})
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "名称").FontSize(12).TextColor(a.pal.muted).Width(56)
					ui.TextInput(c.Key("cloud-name"), &a.cloudFormName).Placeholder("例如：我的音乐云盘 / Alist").Label("云盘名称").Grow(1)
				})
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "地址").FontSize(12).TextColor(a.pal.muted).Width(56)
					ui.TextInput(c.Key("cloud-url"), &a.cloudFormURL).Placeholder("https://dav.jianguoyun.com/dav/Music").Label("WebDAV 地址").Grow(1)
				})
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "账号").FontSize(12).TextColor(a.pal.muted).Width(56)
					ui.TextInput(c.Key("cloud-user"), &a.cloudFormUser).Placeholder("用户名（匿名访问可留空）").Label("WebDAV 账号").Grow(1)
					ui.Text(c, "密码").FontSize(12).TextColor(a.pal.muted).Width(42)
					ui.TextInput(c.Key("cloud-pass"), &a.cloudFormPass).Password().Placeholder("应用密码 / Token").Label("WebDAV 密码").Grow(1)
				})
			}

			if a.cloudFormErr != "" {
				ui.Text(c, a.cloudFormErr).FontSize(12).TextColor(ui.Hex("#e5484d"))
			} else if a.cloudFormOK != "" {
				ui.Text(c, a.cloudFormOK).FontSize(12).TextColor(a.pal.accent)
			}

			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				switch a.cloudFormTab {
				case 1:
					btn := "浏览器授权登录 OneDrive"
					if a.cloudFormAuthing {
						btn = "等待浏览器授权…"
					}
					if ui.PrimaryButton(c.Key("od-auth"), btn).Disabled(a.cloudFormAuthing).Clicked() {
						a.authorizeOAuthForm(localmusic.CloudTypeOneDrive)
					}
					if defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeOneDrive); defID != "" {
						advLabel := "自定义凭据"
						if a.cloudFormAdvanced {
							advLabel = "收起自定义凭据"
						}
						if ui.Button(c.Key("od-adv"), advLabel).Clicked() {
							a.cloudFormAdvanced = !a.cloudFormAdvanced
						}
					}
				case 2:
					btn := "浏览器授权登录 Google Drive"
					if a.cloudFormAuthing {
						btn = "等待浏览器授权…"
					}
					if ui.PrimaryButton(c.Key("gd-auth"), btn).Disabled(a.cloudFormAuthing).Clicked() {
						a.authorizeOAuthForm(localmusic.CloudTypeGoogleDrive)
					}
					if defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeGoogleDrive); defID != "" {
						advLabel := "自定义凭据"
						if a.cloudFormAdvanced {
							advLabel = "收起自定义凭据"
						}
						if ui.Button(c.Key("gd-adv"), advLabel).Clicked() {
							a.cloudFormAdvanced = !a.cloudFormAdvanced
						}
					}
				case 3:
					btn := "浏览器授权登录百度网盘"
					if a.cloudFormAuthing {
						btn = "等待浏览器授权…"
					}
					if ui.PrimaryButton(c.Key("bd-auth"), btn).Disabled(a.cloudFormAuthing).Clicked() {
						a.authorizeOAuthForm(localmusic.CloudTypeBaidu)
					}
					if defID, _ := localmusic.DefaultOAuthCreds(localmusic.CloudTypeBaidu); defID != "" {
						advLabel := "自定义凭据"
						if a.cloudFormAdvanced {
							advLabel = "收起自定义凭据"
						}
						if ui.Button(c.Key("bd-adv"), advLabel).Clicked() {
							a.cloudFormAdvanced = !a.cloudFormAdvanced
						}
					}
				case 4:
					btn := "扫码登录夸克网盘"
					if a.cloudFormAuthing {
						btn = "等待扫码登录…"
					}
					if ui.PrimaryButton(c.Key("qk-web-login"), btn).Disabled(a.cloudFormAuthing).Clicked() {
						a.openCloudWebLogin(localmusic.CloudTypeQuark)
					}
					if a.cloudFormAdvanced {
						testLabel := "测试连接"
						if a.cloudFormTesting {
							testLabel = "正在测试…"
						}
						if ui.Button(c.Key("qk-test"), testLabel).Disabled(a.cloudFormTesting).Clicked() {
							a.testQuarkForm()
						}
						if ui.Button(c.Key("qk-save"), "保存 Cookie").Disabled(a.cloudFormTesting).Clicked() {
							a.saveQuarkForm()
						}
					}
					advLabel := "手动 Cookie"
					if a.cloudFormAdvanced {
						advLabel = "收起手动"
					}
					if ui.Button(c.Key("qk-adv"), advLabel).Clicked() {
						a.cloudFormAdvanced = !a.cloudFormAdvanced
					}
				case 5:
					btn := "扫码登录 115 网盘"
					if a.cloudFormAuthing {
						btn = "等待扫码登录…"
					}
					if ui.PrimaryButton(c.Key("p115-web-login"), btn).Disabled(a.cloudFormAuthing).Clicked() {
						a.openCloudWebLogin(localmusic.CloudType115)
					}
					if a.cloudFormAdvanced {
						testLabel := "测试连接"
						if a.cloudFormTesting {
							testLabel = "正在测试…"
						}
						if ui.Button(c.Key("p115-test"), testLabel).Disabled(a.cloudFormTesting).Clicked() {
							a.test115Form()
						}
						if ui.Button(c.Key("p115-save"), "保存 Cookie").Disabled(a.cloudFormTesting).Clicked() {
							a.save115Form()
						}
					}
					advLabel := "手动 Cookie"
					if a.cloudFormAdvanced {
						advLabel = "收起手动"
					}
					if ui.Button(c.Key("p115-adv"), advLabel).Clicked() {
						a.cloudFormAdvanced = !a.cloudFormAdvanced
					}
				default:
					testLabel := "测试连接"
					if a.cloudFormTesting {
						testLabel = "正在测试…"
					}
					if ui.Button(c.Key("cloud-test"), testLabel).Disabled(a.cloudFormTesting).Clicked() {
						a.testCloudForm()
					}
					if ui.PrimaryButton(c.Key("cloud-save"), "保存并扫描").Disabled(a.cloudFormTesting).Clicked() {
						a.saveCloudForm()
					}
				}
				if ui.Button(c.Key("cloud-cancel"), "取消").Clicked() {
					a.closeCloudLoginWindow()
					a.resetCloudForm()
					a.cloudFormErr, a.cloudFormOK = "", ""
				}
			})
		})
}

func (a *app) localSearch(c *ui.Context, tracks []spotify.Track) {
	query := a.localQuery
	order := a.localSort
	searchChanged, sortChanged := a.trackSearchRow(c, "local-search", "local-search-capsule", "local-filter", "clear-local-search",
		&a.localSearchBox, &query, "筛选本地音乐", "搜索本地与云盘音乐", len(tracks), &order, "local-sort", "本地音乐排序")
	if searchChanged {
		a.localQuery = query
		a.trackList.ScrollTo(0, ui.Start)
	}
	if sortChanged {
		a.localSort = order
		a.trackList.ScrollTo(0, ui.Start)
	}
}

func (a *app) playLocalMatch(match spotify.Track) {
	tracks := a.filterLocalScope(a.orderedLocalTracks())
	for index, track := range tracks {
		if track.ID == match.ID {
			a.playQueue(tracks, index)
			return
		}
	}
}
