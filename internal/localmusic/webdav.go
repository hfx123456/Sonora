package localmusic

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dhowden/tag"

	"vinyl/internal/spotify"
)

const (
	CloudTypeWebDAV      = "webdav"
	CloudTypeOneDrive    = "onedrive"
	CloudTypeGoogleDrive = "googledrive"
	CloudTypeBaidu       = "baidu"
	CloudTypeQuark       = "quark"
	CloudType115         = "115"
)

// CloudSource describes a cloud music folder (WebDAV, OneDrive, Google Drive,
// Baidu Netdisk, or Quark Drive).
type CloudSource struct {
	ID           string `json:"id"`
	Type         string `json:"type,omitempty"` // "", "webdav", "onedrive", "googledrive", "baidu", "quark"
	Name         string `json:"name"`
	URL          string `json:"url,omitempty"`
	Folder       string `json:"folder,omitempty"`
	Username     string `json:"username,omitempty"`
	Password     string `json:"-"` // WebDAV password or Quark Cookie
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"-"`
	RefreshToken string `json:"-"`
}

func (s CloudSource) EffectiveType() string {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case CloudTypeOneDrive:
		return CloudTypeOneDrive
	case CloudTypeGoogleDrive:
		return CloudTypeGoogleDrive
	case CloudTypeBaidu:
		return CloudTypeBaidu
	case CloudTypeQuark:
		return CloudTypeQuark
	case CloudType115:
		return CloudType115
	default:
		return CloudTypeWebDAV
	}
}

func (s CloudSource) authHeader() string {
	if s.Username == "" && s.Password == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(s.Username+":"+s.Password))
}

// cloudAuthEntry keeps the normalized URL scope and Basic auth header in memory.
type cloudAuthEntry struct {
	scheme     string
	host       string
	pathPrefix string
	auth       string
}

var cloudAuth struct {
	sync.RWMutex
	entries []cloudAuthEntry
}

func normalizeWebDAVPath(p string) string {
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	clean := path.Clean(p)
	if !strings.HasPrefix(clean, "/") {
		clean = "/" + clean
	}
	return clean
}

func withinWebDAVScope(targetPath, rootPath string) bool {
	target := normalizeWebDAVPath(targetPath)
	root := normalizeWebDAVPath(rootPath)
	if root == "/" {
		return true
	}
	return target == root || strings.HasPrefix(target, root+"/")
}

type webdavScopeCtxKey struct{}

func withWebDAVScope(ctx context.Context, scope *url.URL) context.Context {
	if scope == nil {
		return ctx
	}
	return context.WithValue(ctx, webdavScopeCtxKey{}, scope)
}

func isVirtualCloudURI(raw string) bool {
	return strings.HasPrefix(raw, "baidu://") ||
		strings.HasPrefix(raw, "quark://") ||
		strings.HasPrefix(raw, "p115://") ||
		strings.HasPrefix(raw, "onedrive://") ||
		strings.HasPrefix(raw, "gdrive://")
}

var cloudHTTPClient = &http.Client{
	Timeout: 20 * time.Second,
	Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		tr := spotify.Client.Transport
		if tr == nil {
			tr = http.DefaultTransport
		}
		return tr.RoundTrip(req)
	}),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 8 {
			return errors.New("too many redirects")
		}
		if len(via) > 0 && via[0].URL != nil && req.URL != nil {
			orig := via[0].URL
			sameScheme := strings.EqualFold(req.URL.Scheme, orig.Scheme)
			sameHost := strings.EqualFold(req.URL.Host, orig.Host)
			if !sameScheme || !sameHost {
				req.Header.Del("Authorization")
				req.Header.Del("Cookie")
			}
			if scope, ok := req.Context().Value(webdavScopeCtxKey{}).(*url.URL); ok && scope != nil {
				inScope := strings.EqualFold(req.URL.Scheme, scope.Scheme) &&
					strings.EqualFold(req.URL.Host, scope.Host) &&
					withinWebDAVScope(req.URL.Path, scope.Path)
				if !inScope {
					req.Header.Del("Authorization")
					req.Header.Del("Cookie")
					if req.Method == "PROPFIND" || via[0].Method == "PROPFIND" {
						return errors.New("refused out-of-scope WebDAV redirect")
					}
				}
			}
		}
		return nil
	},
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// SetCloudSources registers credentials for active cloud sources in memory so
// the player and lyrics loader can authenticate requests without storing
// plaintext secrets inside Track structs or session files.
func SetCloudSources(sources []CloudSource) {
	entries := make([]cloudAuthEntry, 0, len(sources))
	for _, s := range sources {
		if s.EffectiveType() != CloudTypeWebDAV {
			continue
		}
		norm, err := NormalizeCloudURL(s.URL)
		if err != nil {
			continue
		}
		u, err := url.Parse(norm)
		if err != nil {
			continue
		}
		entries = append(entries, cloudAuthEntry{
			scheme:     strings.ToLower(u.Scheme),
			host:       strings.ToLower(u.Host),
			pathPrefix: normalizeWebDAVPath(u.Path),
			auth:       s.authHeader(),
		})
	}
	// Longest mount path first so nested mounts match their most specific credential.
	slices.SortFunc(entries, func(a, b cloudAuthEntry) int {
		return cmp.Compare(len(b.pathPrefix), len(a.pathPrefix))
	})
	cloudAuth.Lock()
	cloudAuth.entries = entries
	cloudAuth.Unlock()
	registerOAuthSources(sources)
	registerDomesticSources(sources)
}

// ResolveCloudMedia turns a track's CloudURL (which may be a direct HTTPS URL
// or a virtual baidu:// / quark:// / p115:// / onedrive:// / gdrive:// URI)
// into a streamable HTTP Range URL plus any required request headers.
func ResolveCloudMedia(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	if strings.HasPrefix(cloudURL, "baidu://") {
		return resolveBaiduStream(ctx, cloudURL)
	}
	if strings.HasPrefix(cloudURL, "quark://") {
		return resolveQuarkStream(ctx, cloudURL)
	}
	if strings.HasPrefix(cloudURL, "p115://") {
		return resolve115Stream(ctx, cloudURL)
	}
	if strings.HasPrefix(cloudURL, "onedrive://") {
		return resolveOneDriveStream(ctx, cloudURL)
	}
	if strings.HasPrefix(cloudURL, "gdrive://") {
		return resolveGoogleDriveStream(ctx, cloudURL)
	}
	return cloudURL, CloudHeadersForURL(cloudURL), nil
}

// CloudHeadersForURL returns authentication headers for a registered cloud URL.
func CloudHeadersForURL(rawURL string) map[string]string {
	if h := oauthHeadersForURL(rawURL); len(h) > 0 {
		return h
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	cloudAuth.RLock()
	defer cloudAuth.RUnlock()
	for _, e := range cloudAuth.entries {
		if strings.EqualFold(u.Scheme, e.scheme) && strings.EqualFold(u.Host, e.host) && withinWebDAVScope(u.Path, e.pathPrefix) {
			if e.auth == "" {
				return nil
			}
			return map[string]string{"Authorization": e.auth}
		}
	}
	return nil
}

// NormalizeCloudURL cleans and validates a user-entered WebDAV URL.
func NormalizeCloudURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请输入 WebDAV 地址")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("WebDAV 地址格式无效，需以 http:// 或 https:// 开头")
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}

const propfindBody = `<?xml version="1.0" encoding="utf-8" ?><D:propfind xmlns:D="DAV:"><D:prop><D:resourcetype/><D:getcontentlength/><D:getetag/><D:getlastmodified/></D:prop></D:propfind>`

type davMultistatus struct {
	Responses []davResponse `xml:"response"`
}

type davResponse struct {
	Href      string        `xml:"href"`
	Propstats []davPropstat `xml:"propstat"`
}

type davPropstat struct {
	Prop   davProp `xml:"prop"`
	Status string  `xml:"status"`
}

type davProp struct {
	ResourceType  davResourceType `xml:"resourcetype"`
	ContentLength string          `xml:"getcontentlength"`
	ETag          string          `xml:"getetag"`
	LastModified  string          `xml:"getlastmodified"`
}

type davResourceType struct {
	Collection *struct{} `xml:"collection"`
}

// TestCloudSource checks whether a WebDAV endpoint is reachable and credentials work.
func TestCloudSource(ctx context.Context, src CloudSource) error {
	base, err := NormalizeCloudURL(src.URL)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", base, strings.NewReader(propfindBody))
	if err != nil {
		return err
	}
	req.Header.Set("Depth", "0")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if auth := src.authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("无法连接到 WebDAV 服务器：%v", err)
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	switch res.StatusCode {
	case http.StatusMultiStatus, http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("账号或密码错误（401/403）")
	case http.StatusNotFound:
		return errors.New("找不到该云盘目录（404）")
	case http.StatusMethodNotAllowed:
		return errors.New("该地址不支持 WebDAV 协议（405）")
	default:
		return fmt.Errorf("服务器返回异常状态：%s", res.Status)
	}
}

type davFile struct {
	URL          string
	DownloadURL  string // optional temporary download URL when URL is virtual (baidu://, quark://)
	Name         string
	Ext          string
	Size         int64
	ETag         string
	LastModified string
	DirCoverURL  string
	LyricsURL    string
	AuthHeader   string
	Headers      map[string]string
	MetaTitle    string
	MetaArtist   string
	MetaAlbum    string
	MetaDuration int64 // milliseconds, when provider already supplies audio metadata (e.g. OneDrive)
	SkipRange    bool
	Source       CloudSource
}

type cloudIndexCache struct {
	Entries map[string]cloudIndexEntry `json:"entries"`
}

type cloudIndexEntry struct {
	SourceID     string        `json:"sourceId,omitempty"`
	ETag         string        `json:"etag,omitempty"`
	LastModified string        `json:"lastModified,omitempty"`
	Size         int64         `json:"size,omitempty"`
	Track        spotify.Track `json:"track"`
}

func loadCloudIndex(cachePath string) *cloudIndexCache {
	c := &cloudIndexCache{Entries: map[string]cloudIndexEntry{}}
	if cachePath == "" {
		return c
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return c
	}
	var loaded cloudIndexCache
	if json.Unmarshal(data, &loaded) == nil && loaded.Entries != nil {
		return &loaded
	}
	return c
}

func saveCloudIndex(cachePath string, c *cloudIndexCache) {
	if cachePath == "" || c == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(c)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), "cloud-index-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmpName)
		return
	}
	if os.Rename(tmpName, cachePath) != nil {
		os.Remove(tmpName)
	}
}

// LoadCachedCloud returns tracks previously saved in cloud-index.json that
// belong to the currently active cloud sources, so the UI can render them
// immediately on startup without showing ghost tracks from removed drives.
func LoadCachedCloud(cachePath string, active []CloudSource) []spotify.Track {
	if len(active) == 0 {
		return nil
	}
	cloudIndexMu.Lock()
	idx := loadCloudIndex(cachePath)
	cloudIndexMu.Unlock()
	if len(idx.Entries) == 0 {
		return nil
	}
	activeSources := make(map[string]CloudSource, len(active))
	for _, s := range active {
		if s.ID != "" {
			activeSources[s.ID] = s
		}
	}
	tracks := make([]spotify.Track, 0, len(idx.Entries))
	for _, e := range idx.Entries {
		if e.Track.ID == "" || e.Track.CloudURL == "" {
			continue
		}
		source, ok := activeSources[e.SourceID]
		if !ok {
			continue
		}
		track := e.Track
		track.CloudURL = cachedOAuthURL(track.CloudURL, source)
		track.CloudLyricsURL = cachedOAuthURL(track.CloudLyricsURL, source)
		tracks = append(tracks, track)
	}
	slices.SortFunc(tracks, func(a, b spotify.Track) int {
		if a.Unplayable != b.Unplayable {
			if a.Unplayable {
				return 1
			}
			return -1
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.CloudURL, b.CloudURL))
	})
	return tracks
}

// CloudFolder represents one selectable subdirectory in a cloud drive.
type CloudFolder struct {
	Name string
	Path string
}

// ListCloudFolders lists immediate child directories under folderPath for the
// given CloudSource so the user can visually browse and pick a music folder.
func ListCloudFolders(ctx context.Context, src CloudSource, folderPath string) ([]CloudFolder, error) {
	folderPath = strings.Trim(strings.TrimSpace(folderPath), "/")
	joinPath := func(name string) string {
		if folderPath == "" {
			return name
		}
		return folderPath + "/" + name
	}

	var folders []CloudFolder
	switch src.EffectiveType() {
	case CloudTypeOneDrive:
		entry := oauthEntryForSource(src)
		token, err := entry.validAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		u := oneDriveAPIBase + "/me/drive/root/children?$select=id,name,folder&$top=200"
		if folderPath != "" {
			u = oneDriveAPIBase + "/me/drive/root:/" + escapeOneDrivePath(folderPath) + ":/children?$select=id,name,folder&$top=200"
		}
		items, err := listOneDriveDir(ctx, u, "Bearer "+token)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Folder != nil {
				folders = append(folders, CloudFolder{Name: it.Name, Path: "/" + joinPath(it.Name)})
			}
		}

	case CloudTypeGoogleDrive:
		entry := oauthEntryForSource(src)
		token, err := entry.validAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		authHeader := "Bearer " + token
		dirID, err := resolveGoogleDriveFolder(ctx, folderPath, authHeader)
		if err != nil {
			return nil, err
		}
		items, err := listGoogleDriveDir(ctx, dirID, authHeader)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.MimeType == gdriveFolderMIME {
				folders = append(folders, CloudFolder{Name: it.Name, Path: joinPath(it.Name)})
			}
		}

	case CloudTypeBaidu:
		entry := oauthEntryForSource(src)
		token, err := entry.validAccessToken(ctx)
		if err != nil {
			return nil, err
		}
		dir := "/"
		if folderPath != "" {
			dir = "/" + folderPath
		}
		items, err := listBaiduDir(ctx, dir, token)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.IsDir == 1 {
				folders = append(folders, CloudFolder{Name: it.ServerFilename, Path: "/" + joinPath(it.ServerFilename)})
			}
		}

	case CloudTypeQuark:
		cookie := strings.TrimSpace(src.Password)
		if cookie == "" {
			return nil, errors.New("夸克网盘 Cookie 为空")
		}
		dirFID, err := resolveQuarkFolder(ctx, folderPath, cookie)
		if err != nil {
			return nil, err
		}
		items, err := listQuarkDir(ctx, dirFID, cookie, 5)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Dir {
				folders = append(folders, CloudFolder{Name: it.FileName, Path: joinPath(it.FileName)})
			}
		}

	case CloudType115:
		cookie := strings.TrimSpace(src.Password)
		if cookie == "" {
			return nil, errors.New("115 网盘 Cookie 为空")
		}
		dirCID, err := resolve115Folder(ctx, folderPath, cookie)
		if err != nil {
			return nil, err
		}
		items, err := list115Dir(ctx, dirCID, cookie, 3)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.isDir() {
				folders = append(folders, CloudFolder{Name: it.N, Path: joinPath(it.N)})
			}
		}

	default:
		return nil, errors.New("该云盘类型不支持目录浏览")
	}

	slices.SortFunc(folders, func(a, b CloudFolder) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return folders, nil
}

var (
	cloudAudioCacheMu  sync.RWMutex
	cloudAudioCacheDir string
	cloudCaching       sync.Map // trackID -> bool
)

// SetCloudCacheDir sets the local directory where played cloud songs are cached
// for instant subsequent playback and seeking.
func SetCloudCacheDir(dir string) {
	cloudAudioCacheMu.Lock()
	cloudAudioCacheDir = dir
	cloudAudioCacheMu.Unlock()
}

func cloudAudioPath(trackID string) string {
	cloudAudioCacheMu.RLock()
	dir := cloudAudioCacheDir
	cloudAudioCacheMu.RUnlock()
	if dir == "" || trackID == "" {
		return ""
	}
	safe := strings.NewReplacer(":", "_", "/", "_", "\\", "_").Replace(trackID)
	return filepath.Join(dir, safe+".audio")
}

// DeleteCachedCloudAudio removes a track's locally cached cloud audio file
// from Sonora's cache directory (never touching the remote cloud file).
func DeleteCachedCloudAudio(trackID string) {
	if p := cloudAudioPath(trackID); p != "" {
		_ = os.Remove(p)
	}
}

// CachedCloudAudio returns the local cached file path for a cloud track if it
// has already been downloaded, or "" if not yet cached.
func CachedCloudAudio(trackID string) string {
	p := cloudAudioPath(trackID)
	if p == "" {
		return ""
	}
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return ""
	}
	return p
}

var (
	cloudCallbackMu      sync.RWMutex
	onCloudTrackEnriched func(track spotify.Track)
)

// SetCloudCallbacks registers callback hooks invoked asynchronously by cloud
// OAuth refresh and local caching workers.
func SetCloudCallbacks(onTokenRefresh func(sourceID, newRefreshToken string), onTrackEnriched func(track spotify.Track)) {
	cloudCallbackMu.Lock()
	onCloudTokenRefresh = onTokenRefresh
	onCloudTrackEnriched = onTrackEnriched
	cloudCallbackMu.Unlock()
}

func notifyCloudTrackEnriched(track spotify.Track) {
	cloudCallbackMu.RLock()
	cb := onCloudTrackEnriched
	cloudCallbackMu.RUnlock()
	if cb != nil {
		cb(track)
	}
}

func enrichCloudTrackFromFile(track spotify.Track, localFile, coverDir, cachePath string) spotify.Track {
	parsed, err := Read(localFile, coverDir)
	if err != nil {
		return track
	}
	if parsed.Name != "" && parsed.Name != strings.TrimSuffix(filepath.Base(localFile), filepath.Ext(localFile)) {
		track.Name = parsed.Name
	}
	if len(parsed.Artists) > 0 {
		track.Artists = parsed.Artists
	}
	if parsed.Album.Name != "" {
		track.Album.Name = parsed.Album.Name
	}
	if parsed.Album.PicURL != "" {
		track.Album.PicURL = parsed.Album.PicURL
	}
	if parsed.Duration > 0 {
		track.Duration = parsed.Duration
	}
	if parsed.LocalLyrics != "" && track.LocalLyrics == "" {
		track.LocalLyrics = parsed.LocalLyrics
	}
	if cachePath != "" && track.CloudURL != "" {
		cloudIndexMu.Lock()
		idx := loadCloudIndex(cachePath)
		if entry, ok := idx.Entries[track.CloudURL]; ok {
			entry.Track = track
			idx.Entries[track.CloudURL] = entry
			saveCloudIndex(cachePath, idx)
		}
		cloudIndexMu.Unlock()
	}
	return track
}

var maxCloudCacheBytes int64 = 256 << 20

func downloadToCloudCache(ctx context.Context, trackID, streamURL string, headers map[string]string) (string, error) {
	dst := cloudAudioPath(trackID)
	if dst == "" || streamURL == "" {
		return "", errors.New("invalid cloud cache target")
	}
	if existing := CachedCloudAudio(trackID); existing != "" {
		return existing, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	dlClient := &http.Client{
		Timeout:       5 * time.Minute,
		Transport:     cloudHTTPClient.Transport,
		CheckRedirect: cloudHTTPClient.CheckRedirect,
	}
	res, err := dlClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", res.Status)
	}
	if res.ContentLength > maxCloudCacheBytes {
		return "", fmt.Errorf("file exceeds %d MiB cache limit", maxCloudCacheBytes>>20)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "cloud-*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	// Read one byte past the cap so streams exceeding maxCloudCacheBytes are
	// rejected cleanly instead of saving a truncated file as complete.
	n, copyErr := io.Copy(tmp, io.LimitReader(res.Body, maxCloudCacheBytes+1))
	closeErr := tmp.Close()
	if copyErr != nil || closeErr != nil || n == 0 {
		os.Remove(tmpName)
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		return "", errors.New("empty download")
	}
	if n > maxCloudCacheBytes {
		os.Remove(tmpName)
		return "", fmt.Errorf("file exceeds %d MiB cache limit", maxCloudCacheBytes>>20)
	}
	if res.ContentLength > 0 && n != res.ContentLength {
		os.Remove(tmpName)
		return "", fmt.Errorf("incomplete download: got %d of %d bytes", n, res.ContentLength)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		if existing := CachedCloudAudio(trackID); existing != "" {
			return existing, nil
		}
		return "", err
	}
	return dst, nil
}

// CacheCloudAudioAsync downloads a cloud track in the background to the local
// cache directory so future plays and seeks happen instantaneously from disk,
// and enriches its cover art / tags from the downloaded file.
func CacheCloudAudioAsync(track spotify.Track, streamURL string, headers map[string]string) {
	if track.ID == "" || streamURL == "" || CachedCloudAudio(track.ID) != "" {
		return
	}
	if _, loaded := cloudCaching.LoadOrStore(track.ID, true); loaded {
		return
	}
	hdrs := make(map[string]string, len(headers))
	for k, v := range headers {
		hdrs[k] = v
	}
	go func() {
		defer cloudCaching.Delete(track.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		dst, err := downloadToCloudCache(ctx, track.ID, streamURL, hdrs)
		if err != nil {
			return
		}
		cloudAudioCacheMu.RLock()
		dir := cloudAudioCacheDir
		cloudAudioCacheMu.RUnlock()
		baseDir := filepath.Dir(dir)
		enriched := enrichCloudTrackFromFile(track, dst, filepath.Join(baseDir, "local-covers"), filepath.Join(baseDir, "cloud-index.json"))
		notifyCloudTrackEnriched(enriched)
	}()
}

// CloudCacheStats summarizes one batch cloud caching run.
type CloudCacheStats struct {
	Total     int
	Succeeded int
	Failed    int
}

// CacheAllCloudTracks downloads all uncached cloud tracks to local disk in
// parallel (3 workers), extracting full embedded cover art & tags from each
// downloaded file so future playback and metadata are 100% local and instant.
func CacheAllCloudTracks(ctx context.Context, tracks []spotify.Track, coverDir, cachePath string, onProgress func(done, succeeded, failed, total int, enriched *spotify.Track)) CloudCacheStats {
	var pending []spotify.Track
	for _, tr := range tracks {
		if tr.Cloud() && tr.CloudURL != "" && (CachedCloudAudio(tr.ID) == "" || tr.Album.PicURL == "") {
			pending = append(pending, tr)
		}
	}
	total := len(pending)
	if total == 0 {
		if onProgress != nil {
			onProgress(0, 0, 0, 0, nil)
		}
		return CloudCacheStats{}
	}
	jobs := make(chan spotify.Track, total)
	for _, tr := range pending {
		jobs <- tr
	}
	close(jobs)

	var mu sync.Mutex
	doneCount := 0
	succeededCount := 0
	failedCount := 0
	workers := min(3, total)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for tr := range jobs {
				if ctx.Err() != nil {
					return
				}
				dst := CachedCloudAudio(tr.ID)
				if dst == "" {
					streamURL, hdrs, err := ResolveCloudMedia(ctx, tr.CloudURL)
					if err == nil && streamURL != "" {
						dst, _ = downloadToCloudCache(ctx, tr.ID, streamURL, hdrs)
					}
				}
				var enriched *spotify.Track
				ok := dst != ""
				if ok {
					t2 := enrichCloudTrackFromFile(tr, dst, coverDir, cachePath)
					enriched = &t2
				}
				mu.Lock()
				doneCount++
				if ok {
					succeededCount++
				} else {
					failedCount++
				}
				curDone, curOK, curFail := doneCount, succeededCount, failedCount
				mu.Unlock()
				if onProgress != nil {
					onProgress(curDone, curOK, curFail, total, enriched)
				}
			}
		}()
	}
	wg.Wait()
	return CloudCacheStats{
		Total:     total,
		Succeeded: succeededCount,
		Failed:    failedCount,
	}
}

// ScanCloud recursively scans WebDAV, OneDrive, Google Drive, Baidu, Quark,
// and 115 cloud sources, reusing cached metadata for unchanged files. When a
// source fails to refresh (for example while offline), its previously indexed
// tracks are preserved instead of wiping the offline library. When a source
// succeeds cleanly (including an empty directory), deleted tracks are pruned
// from cloud-index.json so they do not reappear on next launch.
func ScanCloud(ctx context.Context, sources []CloudSource, coverDir, cachePath string) ([]spotify.Track, []string) {
	if len(sources) == 0 {
		return nil, nil
	}
	SetCloudSources(sources)
	cloudIndexMu.Lock()
	index := loadCloudIndex(cachePath)
	cloudIndexMu.Unlock()

	var files []davFile
	var warnings []string
	seenURLs := map[string]bool{}
	succeededSources := map[string]bool{}
	failedSources := map[string]CloudSource{}
	partialSources := map[string]CloudSource{}
	updatedEntries := map[string]cloudIndexEntry{}

	for _, src := range sources {
		var found []davFile
		var warns []string
		switch src.EffectiveType() {
		case CloudTypeOneDrive:
			found, warns = walkOneDrive(ctx, src, seenURLs)
		case CloudTypeGoogleDrive:
			found, warns = walkGoogleDrive(ctx, src, seenURLs)
		case CloudTypeBaidu:
			found, warns = walkBaidu(ctx, src, seenURLs)
		case CloudTypeQuark:
			found, warns = walkQuark(ctx, src, seenURLs)
		case CloudType115:
			found, warns = walk115(ctx, src, seenURLs)
		default:
			base, err := NormalizeCloudURL(src.URL)
			if err != nil {
				warns = []string{fmt.Sprintf("云盘「%s」：%v", cmp.Or(src.Name, src.URL), err)}
			} else {
				found, warns = walkWebDAV(ctx, src, base, seenURLs)
			}
		}
		files = append(files, found...)
		warnings = append(warnings, warns...)
		if src.ID != "" {
			if len(warns) == 0 && ctx.Err() == nil {
				succeededSources[src.ID] = true
			} else if len(found) == 0 {
				failedSources[src.ID] = src
			} else {
				partialSources[src.ID] = src
			}
		}
	}

	tracks := make([]spotify.Track, len(files))
	okFlags := make([]bool, len(files))

	var indexMu sync.Mutex
	var warnMu sync.Mutex
	indexDirty := false

	if len(files) > 0 {
		// For large uncached cloud libraries (> 50 files), skip per-file HTTP Range
		// probing so thousands of songs load in a few seconds instead of minutes.
		uncachedCount := 0
		for _, f := range files {
			if cached, hit := index.Entries[f.URL]; !hit || !sameDAVVersion(cached, f) {
				uncachedCount++
			}
		}
		fastBatch := uncachedCount > 50

		workers := min(4, len(files))
		jobs := make(chan int, len(files))
		for i := range files {
			jobs <- i
		}
		close(jobs)

		var wg sync.WaitGroup
		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := range jobs {
					if ctx.Err() != nil {
						return
					}
					f := files[i]
					indexMu.Lock()
					cached, hit := index.Entries[f.URL]
					indexMu.Unlock()

					if hit && sameDAVVersion(cached, f) && coverStillExists(cached.Track.Album.PicURL) {
						t := cached.Track
						t.CloudSource = cmp.Or(f.Source.Name, f.Source.URL)
						if f.LyricsURL != "" {
							t.CloudLyricsURL = f.LyricsURL
						}
						tracks[i] = t
						okFlags[i] = true
						continue
					}

					if fastBatch {
						f.SkipRange = true
					}
					t, err := readCloudTrack(ctx, f, coverDir)
					if err != nil {
						warnMu.Lock()
						warnings = append(warnings, fmt.Sprintf("云盘 %s：%v", f.Name, err))
						warnMu.Unlock()
						if t.ID != "" {
							tracks[i] = t
							okFlags[i] = true
						}
						continue
					}
					tracks[i] = t
					okFlags[i] = true

					entry := cloudIndexEntry{
						SourceID:     f.Source.ID,
						ETag:         f.ETag,
						LastModified: f.LastModified,
						Size:         f.Size,
						Track:        t,
					}
					indexMu.Lock()
					index.Entries[f.URL] = entry
					updatedEntries[f.URL] = entry
					indexDirty = true
					indexMu.Unlock()
				}
			}()
		}
		wg.Wait()
	}

	result := make([]spotify.Track, 0, len(files))
	for i, ok := range okFlags {
		if ok {
			result = append(result, tracks[i])
			continue
		}
		// If metadata probing stopped early due to timeout/cancel, fall back to
		// any existing cached track so the scanned source does not lose items.
		if cached, hit := index.Entries[files[i].URL]; hit && cached.Track.ID != "" {
			t := cached.Track
			t.CloudURL = cachedOAuthURL(t.CloudURL, files[i].Source)
			t.CloudLyricsURL = cachedOAuthURL(t.CloudLyricsURL, files[i].Source)
			t.CloudSource = cmp.Or(files[i].Source.Name, files[i].Source.URL)
			result = append(result, t)
		}
	}

	// Preserve offline cached tracks for sources whose refresh failed (or timed
	// out partway through) so users keep access to their downloaded library.
	if (len(failedSources) > 0 || len(partialSources) > 0) && len(index.Entries) > 0 {
		restoredBySource := map[string]bool{}
		for _, e := range index.Entries {
			if e.Track.ID == "" || e.Track.CloudURL == "" {
				continue
			}
			src, failed := failedSources[e.SourceID]
			if !failed {
				src, failed = partialSources[e.SourceID]
			}
			if !failed {
				continue
			}
			t := e.Track
			t.CloudURL = cachedOAuthURL(t.CloudURL, src)
			t.CloudLyricsURL = cachedOAuthURL(t.CloudLyricsURL, src)
			if seenURLs[t.CloudURL] {
				continue
			}
			t.CloudSource = cmp.Or(src.Name, src.URL)
			result = append(result, t)
			if _, fullFail := failedSources[e.SourceID]; fullFail {
				restoredBySource[e.SourceID] = true
			}
		}
		for _, src := range sources {
			if restoredBySource[src.ID] {
				warnings = append(warnings, fmt.Sprintf("云盘「%s」刷新失败，当前显示离线曲库", cmp.Or(src.Name, src.URL)))
			}
		}
	}

	// Prune cache entries from removed cloud sources, and remove entries deleted
	// from sources that succeeded completely (never on cancellation/timeout).
	activeIDs := make(map[string]bool, len(sources))
	for _, s := range sources {
		if s.ID != "" {
			activeIDs[s.ID] = true
		}
	}
	shouldPrune := ctx.Err() == nil
	needSave := indexDirty
	if !needSave {
		for k, e := range index.Entries {
			if e.SourceID == "" || !activeIDs[e.SourceID] || (shouldPrune && succeededSources[e.SourceID] && !seenURLs[k]) {
				needSave = true
				break
			}
		}
	}
	if needSave {
		cloudIndexMu.Lock()
		merged := loadCloudIndex(cachePath)
		for k, e := range updatedEntries {
			// Never let a fast-batch unprobed scan overwrite richer metadata
			// written concurrently by an audio cache download.
			if prev, ok := merged.Entries[k]; ok && prev.SourceID == e.SourceID && prev.Size == e.Size && prev.Track.Duration > 0 && e.Track.Duration == 0 {
				e.Track.Duration = prev.Track.Duration
				if len(e.Track.Artists) == 0 && len(prev.Track.Artists) > 0 {
					e.Track.Artists = prev.Track.Artists
				}
				if e.Track.Album.Name == "" && prev.Track.Album.Name != "" {
					e.Track.Album.Name = prev.Track.Album.Name
				}
				if e.Track.Album.PicURL == "" && prev.Track.Album.PicURL != "" {
					e.Track.Album.PicURL = prev.Track.Album.PicURL
				}
			}
			merged.Entries[k] = e
		}
		for k, e := range merged.Entries {
			if e.SourceID == "" || !activeIDs[e.SourceID] {
				delete(merged.Entries, k)
				continue
			}
			if shouldPrune && succeededSources[e.SourceID] && !seenURLs[k] {
				delete(merged.Entries, k)
			}
		}
		saveCloudIndex(cachePath, merged)
		cloudIndexMu.Unlock()
	}
	slices.SortFunc(result, func(a, b spotify.Track) int {
		if a.Unplayable != b.Unplayable {
			if a.Unplayable {
				return 1
			}
			return -1
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(a.CloudURL, b.CloudURL))
	})
	return result, warnings
}

func sameDAVVersion(cached cloudIndexEntry, f davFile) bool {
	if cached.Track.ID == "" {
		return false
	}
	if f.ETag != "" && cached.ETag != "" {
		return cached.ETag == f.ETag && cached.Size == f.Size
	}
	return cached.Size == f.Size && cached.LastModified == f.LastModified
}

func coverStillExists(picURL string) bool {
	if picURL == "" || !strings.HasPrefix(picURL, "file://") {
		return true
	}
	u, err := url.Parse(picURL)
	if err != nil {
		return false
	}
	p := u.Path
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	_, err = os.Stat(filepath.FromSlash(p))
	return err == nil
}

const (
	maxCloudDepth = 12
	maxCloudDirs  = 2000
	maxCloudFiles = 5000
)

func walkWebDAV(ctx context.Context, src CloudSource, rootURL string, seenFiles map[string]bool) ([]davFile, []string) {
	scope, _ := url.Parse(ensureTrailingSlash(rootURL))
	type dirItem struct {
		url   string
		depth int
	}
	queue := []dirItem{{url: ensureTrailingSlash(rootURL), depth: 0}}
	visitedDirs := map[string]bool{}
	var files []davFile
	var warnings []string

	for len(queue) > 0 && len(visitedDirs) < maxCloudDirs && len(files) < maxCloudFiles {
		if ctx.Err() != nil {
			break
		}
		cur := queue[0]
		queue = queue[1:]

		normCur := normalizeDirKey(cur.url)
		if visitedDirs[normCur] {
			continue
		}
		visitedDirs[normCur] = true

		entries, err := propfindDir(ctx, src, cur.url, scope)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("云盘「%s」目录读取失败：%v", cmp.Or(src.Name, src.URL), err))
			continue
		}

		// First pass: look for a folder cover image in this directory.
		dirCover := ""
		for _, entry := range entries {
			if entry.isDir {
				continue
			}
			lower := strings.ToLower(entry.name)
			if lower == "cover.jpg" || lower == "cover.png" || lower == "folder.jpg" || lower == "folder.png" {
				if entry.size <= 10<<20 {
					dirCover = entry.url
					break
				}
			}
		}

		// Second pass: enqueue subdirectories and collect supported audio files.
		for _, entry := range entries {
			if normalizeDirKey(entry.url) == normCur {
				continue
			}
			if entry.isDir {
				if cur.depth < maxCloudDepth {
					queue = append(queue, dirItem{url: ensureTrailingSlash(entry.url), depth: cur.depth + 1})
				}
				continue
			}
			if !Supported(entry.name) || seenFiles[entry.url] {
				continue
			}
			seenFiles[entry.url] = true
			files = append(files, davFile{
				URL:          entry.url,
				Name:         entry.name,
				Ext:          strings.ToLower(path.Ext(entry.name)),
				Size:         entry.size,
				ETag:         entry.etag,
				LastModified: entry.lastModified,
				DirCoverURL:  dirCover,
				Source:       src,
			})
			if len(files) >= maxCloudFiles {
				break
			}
		}
	}
	if ctx.Err() != nil {
		warnings = append(warnings, fmt.Sprintf("云盘「%s」扫描超时或已取消", cmp.Or(src.Name, src.URL)))
	} else if len(queue) > 0 || len(files) >= maxCloudFiles {
		warnings = append(warnings, fmt.Sprintf("云盘「%s」目录或曲目数量已达上限，部分文件未扫描", cmp.Or(src.Name, src.URL)))
	}
	return files, warnings
}

type davEntry struct {
	url          string
	name         string
	isDir        bool
	size         int64
	etag         string
	lastModified string
}

func propfindDir(ctx context.Context, src CloudSource, dirURL string, scope *url.URL) ([]davEntry, error) {
	req, err := http.NewRequestWithContext(withWebDAVScope(ctx, scope), "PROPFIND", dirURL, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if auth := src.authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus && res.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(res.Body, 16<<10))
		return nil, fmt.Errorf("HTTP %s", res.Status)
	}
	var ms davMultistatus
	if err := xml.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&ms); err != nil {
		return nil, err
	}
	base, err := url.Parse(dirURL)
	if err != nil {
		return nil, err
	}
	out := make([]davEntry, 0, len(ms.Responses))
	for _, r := range ms.Responses {
		href := strings.TrimSpace(r.Href)
		if href == "" {
			continue
		}
		ref, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := base.ResolveReference(ref)
		resolved.Fragment = ""
		if scope != nil {
			if !strings.EqualFold(resolved.Scheme, scope.Scheme) || !strings.EqualFold(resolved.Host, scope.Host) {
				continue
			}
			if !withinWebDAVScope(resolved.Path, scope.Path) {
				continue
			}
		}

		var prop davProp
		foundOK := false
		for _, ps := range r.Propstats {
			if strings.Contains(ps.Status, " 200 ") || ps.Status == "" {
				prop = ps.Prop
				foundOK = true
				break
			}
		}
		if !foundOK && len(r.Propstats) > 0 {
			prop = r.Propstats[0].Prop
		}

		isDir := prop.ResourceType.Collection != nil || strings.HasSuffix(resolved.Path, "/")
		size, _ := strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64)
		cleanPath := strings.TrimSuffix(resolved.Path, "/")
		name := path.Base(cleanPath)
		out = append(out, davEntry{
			url:          resolved.String(),
			name:         name,
			isDir:        isDir,
			size:         size,
			etag:         strings.TrimSpace(prop.ETag),
			lastModified: strings.TrimSpace(prop.LastModified),
		})
	}
	return out, nil
}

func ensureTrailingSlash(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u.String()
}

func normalizeDirKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return strings.TrimRight(raw, "/")
	}
	p := strings.TrimRight(u.Path, "/")
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + p
}

func readCloudTrack(ctx context.Context, f davFile, coverDir string) (spotify.Track, error) {
	sum := sha256.Sum256([]byte(f.URL))
	baseName := strings.TrimSuffix(f.Name, path.Ext(f.Name))
	track := spotify.Track{
		ID:             "cloud:" + hex.EncodeToString(sum[:16]),
		CloudURL:       f.URL,
		CloudLyricsURL: f.LyricsURL,
		CloudSource:    cmp.Or(f.Source.Name, f.Source.URL),
		Name:           cmp.Or(strings.TrimSpace(f.MetaTitle), baseName),
		Duration:       f.MetaDuration,
	}
	if artist := strings.TrimSpace(f.MetaArtist); artist != "" {
		track.Artists = []spotify.Artist{{Name: artist}}
	}
	if album := strings.TrimSpace(f.MetaAlbum); album != "" {
		track.Album.Name = album
	}

	auth := f.AuthHeader
	if auth == "" {
		auth = f.Source.authHeader()
	}
	if auth == "" {
		if h := CloudHeadersForURL(f.URL); h != nil {
			auth = h["Authorization"]
		}
	}

	if f.Size > 0 && !f.SkipRange {
		dlURL := cmp.Or(f.DownloadURL, f.URL)
		hdrs := f.Headers
		if dlURL == f.URL && isVirtualCloudURI(dlURL) {
			if resolved, rh, err := ResolveCloudMedia(ctx, dlURL); err == nil && resolved != "" {
				dlURL, hdrs = resolved, rh
			}
		}
		reader := newWebDAVRangeReader(ctx, dlURL, auth, f.Size)
		reader.headers = hdrs
		if d := duration(reader, f.Size, f.Ext).Milliseconds(); d > 0 {
			track.Duration = d
		}
		_, _ = reader.Seek(0, io.SeekStart)
		if metadata, err := tag.ReadFrom(reader); err == nil {
			track.Name = cmp.Or(strings.TrimSpace(metadata.Title()), track.Name)
			if artist := cmp.Or(strings.TrimSpace(metadata.Artist()), strings.TrimSpace(metadata.AlbumArtist())); artist != "" {
				track.Artists = []spotify.Artist{{Name: artist}}
			}
			track.Album.Name = cmp.Or(strings.TrimSpace(metadata.Album()), track.Album.Name)
			track.LocalLyrics = metadata.Lyrics()
			if picture := metadata.Picture(); picture != nil && len(picture.Data) <= 10<<20 && coverDir != "" {
				hash := sha256.Sum256(picture.Data)
				cover := filepath.Join(coverDir, hex.EncodeToString(hash[:16])+".img")
				if writeCoverAtomic(coverDir, cover, picture.Data) == nil {
					track.Album.PicURL = spotify.FileURL(cover)
				}
			}
		}
	}

	if len(track.Artists) == 0 {
		if artist, title, ok := strings.Cut(track.Name, " - "); ok {
			track.Artists = []spotify.Artist{{Name: strings.TrimSpace(artist)}}
			track.Name = strings.TrimSpace(title)
		}
	}

	if track.Album.PicURL == "" && f.DirCoverURL != "" && coverDir != "" {
		if coverPath, err := downloadCloudCover(ctx, f.DirCoverURL, auth, coverDir); err == nil && coverPath != "" {
			track.Album.PicURL = spotify.FileURL(coverPath)
		}
	}

	return track, nil
}

func downloadCloudCover(ctx context.Context, coverURL, auth, coverDir string) (string, error) {
	hash := sha256.Sum256([]byte(coverURL))
	cover := filepath.Join(coverDir, "dav-"+hex.EncodeToString(hash[:16])+".img")
	if _, err := os.Stat(cover); err == nil {
		return cover, nil
	}
	targetURL := coverURL
	var hdrs map[string]string
	if isVirtualCloudURI(coverURL) {
		var err error
		targetURL, hdrs, err = ResolveCloudMedia(ctx, coverURL)
		if err != nil || targetURL == "" {
			return "", cmp.Or(err, errors.New("invalid cloud cover url"))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return "", err
	}
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	if auth != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", auth)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 10<<20+1))
	if err != nil || len(data) == 0 || len(data) > 10<<20 {
		return "", err
	}
	if err := writeCoverAtomic(coverDir, cover, data); err != nil {
		return "", err
	}
	return cover, nil
}

func cloudLyrics(audioURL, lyricsURL string) string {
	lrcURL := strings.TrimSpace(lyricsURL)
	if lrcURL == "" {
		if isVirtualCloudURI(audioURL) {
			return ""
		}
		u, err := url.Parse(audioURL)
		if err != nil {
			return ""
		}
		ext := path.Ext(u.Path)
		if ext == "" {
			return ""
		}
		u.Path = strings.TrimSuffix(u.Path, ext) + ".lrc"
		u.RawPath = ""
		lrcURL = u.String()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	targetURL, headers, err := ResolveCloudMedia(ctx, lrcURL)
	if err != nil || targetURL == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return ""
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(data) == 0 || len(data) > 2<<20 {
		return ""
	}
	return decodeLyricsBytes(data)
}

// UploadToCloud uploads a local audio file (and its companion .lrc if present)
// to a WebDAV, OneDrive, Google Drive, or Baidu Netdisk CloudSource.
func UploadToCloud(ctx context.Context, localPath string, dst CloudSource) (string, error) {
	switch dst.EffectiveType() {
	case CloudTypeOneDrive:
		return uploadOneDrive(ctx, localPath, dst)
	case CloudTypeGoogleDrive:
		return uploadGoogleDrive(ctx, localPath, dst)
	case CloudTypeBaidu:
		return uploadBaidu(ctx, localPath, dst)
	case CloudTypeQuark:
		return "", errors.New("夸克网盘暂不支持客户端直传，请先在夸克网盘网页/客户端上传后点击重新扫描")
	case CloudType115:
		return "", errors.New("115网盘暂不支持客户端直传，请先在 115 网页/客户端上传后点击重新扫描")
	}
	base, err := NormalizeCloudURL(dst.URL)
	if err != nil {
		return "", err
	}
	targetURL, err := joinCloudFileURL(base, filepath.Base(localPath))
	if err != nil {
		return "", err
	}
	if err := putCloudFile(ctx, localPath, targetURL, dst.authHeader()); err != nil {
		return "", err
	}
	// Best-effort upload of companion .lrc lyrics file.
	lrcPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".lrc"
	if info, err := os.Stat(lrcPath); err == nil && info.Mode().IsRegular() && info.Size() <= 2<<20 {
		if lrcURL, err := joinCloudFileURL(base, filepath.Base(lrcPath)); err == nil {
			_ = putCloudFile(ctx, lrcPath, lrcURL, dst.authHeader())
		}
	}
	return targetURL, nil
}

var cloudIndexMu sync.Mutex

func joinCloudFileURL(baseURL, filename string) (string, error) {
	u, err := url.Parse(ensureTrailingSlash(baseURL))
	if err != nil {
		return "", err
	}
	ref := &url.URL{Path: filename}
	return u.ResolveReference(ref).String(), nil
}

func putCloudFile(ctx context.Context, localPath, targetURL, auth string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, f)
	if err != nil {
		return err
	}
	req.ContentLength = info.Size()
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	// Use a client without short timeout for large FLAC uploads.
	uploadClient := &http.Client{
		Timeout:       10 * time.Minute,
		CheckRedirect: cloudHTTPClient.CheckRedirect,
	}
	res, err := uploadClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 16<<10))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("上传失败（HTTP %s）", res.Status)
	}
	return nil
}

// webdavRangeReader implements io.Reader, io.ReaderAt, and io.Seeker over HTTP
// Range requests with a small chunk cache so metadata/duration parsing never
// downloads entire audio files.
type webdavRangeReader struct {
	ctx       context.Context
	url       string
	auth      string
	headers   map[string]string
	size      int64
	pos       int64
	chunks    map[int64][]byte
	totalRead int64
}

const (
	davChunkSize   int64 = 64 << 10
	davInitialRead int64 = 128 << 10
	davMaxBudget   int64 = 2 << 20
)

func newWebDAVRangeReader(ctx context.Context, rawURL, auth string, size int64) *webdavRangeReader {
	return &webdavRangeReader{
		ctx:    ctx,
		url:    rawURL,
		auth:   auth,
		size:   size,
		chunks: map[int64][]byte{},
	}
}

func (r *webdavRangeReader) Read(p []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	n, err := r.ReadAt(p, r.pos)
	r.pos += int64(n)
	return n, err
}

func (r *webdavRangeReader) Seek(offset int64, whence int) (int64, error) {
	var next int64
	switch whence {
	case io.SeekStart:
		next = offset
	case io.SeekCurrent:
		next = r.pos + offset
	case io.SeekEnd:
		next = r.size + offset
	default:
		return 0, errors.New("invalid whence")
	}
	if next < 0 {
		return 0, errors.New("negative position")
	}
	r.pos = next
	return next, nil
}

func (r *webdavRangeReader) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, errors.New("negative offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	want := len(p)
	if int64(want) > r.size-off {
		want = int(r.size - off)
	}
	copied := 0
	for copied < want {
		cur := off + int64(copied)
		chunkIdx := cur / davChunkSize
		chunk, ok := r.chunks[chunkIdx]
		if !ok {
			if err := r.fetchChunk(chunkIdx); err != nil {
				if copied > 0 {
					return copied, nil
				}
				return 0, err
			}
			chunk = r.chunks[chunkIdx]
		}
		inChunk := int(cur % davChunkSize)
		if inChunk >= len(chunk) {
			break
		}
		n := copy(p[copied:want], chunk[inChunk:])
		copied += n
	}
	if copied < len(p) {
		return copied, io.EOF
	}
	return copied, nil
}

func (r *webdavRangeReader) fetchChunk(chunkIdx int64) error {
	if r.totalRead >= davMaxBudget {
		return io.EOF
	}
	start := chunkIdx * davChunkSize
	span := davChunkSize
	if chunkIdx == 0 && len(r.chunks) == 0 {
		span = davInitialRead
	}
	end := min(r.size-1, start+span-1)
	if end < start {
		return io.EOF
	}
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	if r.auth != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", r.auth)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusPartialContent {
		want := end - start + 1
		data, err := io.ReadAll(io.LimitReader(res.Body, want+1))
		if err != nil {
			return err
		}
		if int64(len(data)) > want {
			return fmt.Errorf("range response exceeds requested %d bytes", want)
		}
		r.storeChunks(start, data)
		return nil
	}
	if res.StatusCode == http.StatusOK && start == 0 {
		data, err := io.ReadAll(io.LimitReader(res.Body, davInitialRead))
		if err != nil {
			return err
		}
		r.storeChunks(0, data)
		return nil
	}
	return fmt.Errorf("range HTTP %s", res.Status)
}

func (r *webdavRangeReader) storeChunks(start int64, data []byte) {
	r.totalRead += int64(len(data))
	for off := int64(0); off < int64(len(data)); off += davChunkSize {
		end := min(int64(len(data)), off+davChunkSize)
		idx := (start + off) / davChunkSize
		r.chunks[idx] = data[off:end]
	}
}
