package localmusic

import (
	"bytes"
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Endpoints are package variables so tests can redirect them to httptest.Server.
var (
	oneDriveAuthEndpoint  = "https://login.microsoftonline.com/common/oauth2/v2.0/authorize"
	oneDriveTokenEndpoint = "https://login.microsoftonline.com/common/oauth2/v2.0/token"
	oneDriveAPIBase       = "https://graph.microsoft.com/v1.0"

	googleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	googleDriveAPIBase  = "https://www.googleapis.com/drive/v3"
	googleUploadAPIBase = "https://www.googleapis.com/upload/drive/v3"
	googleUserInfoURL   = "https://www.googleapis.com/oauth2/v2/userinfo"

	baiduAuthEndpoint  = "https://openapi.baidu.com/oauth/2.0/authorize"
	baiduTokenEndpoint = "https://openapi.baidu.com/oauth/2.0/token"
	baiduPanAPIBase    = "https://pan.baidu.com/rest/2.0"
	baiduUploadAPIBase = "https://d.pcs.baidu.com/rest/2.0"

	quarkAPIBase = "https://drive-pc.quark.cn/1/clouddrive"

	// Default OAuth credentials can be compiled in via -ldflags -X if a
	// distributor wants zero-config login; left empty in source so users bring
	// their own free Client ID.
	DefaultOneDriveClientID   = ""
	DefaultGoogleClientID     = ""
	DefaultGoogleClientSecret = ""
	DefaultBaiduClientID      = ""
	DefaultBaiduClientSecret  = ""
)

// DefaultOAuthCreds returns the built-in default OAuth client ID and secret
// for provider, if configured at build time.
func DefaultOAuthCreds(provider string) (clientID, clientSecret string) {
	switch provider {
	case CloudTypeOneDrive:
		return DefaultOneDriveClientID, ""
	case CloudTypeGoogleDrive:
		return DefaultGoogleClientID, DefaultGoogleClientSecret
	case CloudTypeBaidu:
		return DefaultBaiduClientID, DefaultBaiduClientSecret
	default:
		return "", ""
	}
}

// onCloudTokenRefresh is registered via SetCloudCallbacks.
var onCloudTokenRefresh func(sourceID, newRefreshToken string)
var tokenRefreshNotifyMu sync.Mutex

func notifyCloudTokenRefresh(sourceID, newRefreshToken string) {
	cloudCallbackMu.RLock()
	cb := onCloudTokenRefresh
	cloudCallbackMu.RUnlock()
	if cb != nil {
		go func() {
			tokenRefreshNotifyMu.Lock()
			defer tokenRefreshNotifyMu.Unlock()
			cb(sourceID, newRefreshToken)
		}()
	}
}

// CurrentOAuthRefreshToken returns the latest in-memory refresh token for
// sourceID so async persistence callbacks never overwrite a newer rotated or
// re-authorized token.
func CurrentOAuthRefreshToken(sourceID string) string {
	oauthRegistry.RLock()
	entry := oauthRegistry.byID[sourceID]
	oauthRegistry.RUnlock()
	if entry == nil {
		return ""
	}
	entry.mu.Lock()
	tok := entry.refreshToken
	entry.mu.Unlock()
	return tok
}

// OAuthResult carries the tokens and account info returned by AuthorizeCloudOAuth.
type OAuthResult struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Email        string
	DisplayName  string
}

type oauthTokenEntry struct {
	mu             sync.Mutex
	source         CloudSource
	knownRefreshes map[string]struct{}
	accessToken    string
	expiresAt      time.Time
	refreshToken   string
}

var oauthRegistry struct {
	sync.RWMutex
	byID   map[string]*oauthTokenEntry
	byType map[string]*oauthTokenEntry // fallback by provider type when resolving legacy HTTPS URLs
}

func oauthVirtualURL(provider, sourceID, itemID string) string {
	escapedID := url.PathEscape(itemID)
	switch {
	case provider == CloudTypeOneDrive && sourceID != "":
		return "onedrive://" + url.PathEscape(sourceID) + "/" + escapedID
	case provider == CloudTypeOneDrive:
		return oneDriveAPIBase + "/me/drive/items/" + escapedID + "/content"
	case provider == CloudTypeGoogleDrive && sourceID != "":
		return "gdrive://" + url.PathEscape(sourceID) + "/" + escapedID
	case provider == CloudTypeGoogleDrive:
		return googleDriveAPIBase + "/files/" + escapedID + "?alt=media"
	default:
		return ""
	}
}

// cachedOAuthURL attaches the source recorded by older cloud indexes to a legacy media URL.
func cachedOAuthURL(raw string, source CloudSource) string {
	kind := source.EffectiveType()
	var prefix, suffix string
	switch kind {
	case CloudTypeOneDrive:
		prefix, suffix = oneDriveAPIBase+"/me/drive/items/", "/content"
	case CloudTypeGoogleDrive:
		prefix, suffix = googleDriveAPIBase+"/files/", "?alt=media"
	default:
		return raw
	}
	item, ok := strings.CutPrefix(raw, prefix)
	if !ok {
		return raw
	}
	item, ok = strings.CutSuffix(item, suffix)
	if !ok || item == "" || strings.ContainsAny(item, "/?#") {
		return raw
	}
	id, err := url.PathUnescape(item)
	if err != nil {
		return raw
	}
	return oauthVirtualURL(kind, source.ID, id)
}

func resolveOneDriveStream(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	return resolveOAuthStream(ctx, cloudURL, "onedrive://", CloudTypeOneDrive, func(itemID string) string {
		return oneDriveAPIBase + "/me/drive/items/" + url.PathEscape(itemID) + "/content"
	})
}

func resolveGoogleDriveStream(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	return resolveOAuthStream(ctx, cloudURL, "gdrive://", CloudTypeGoogleDrive, func(fileID string) string {
		return googleDriveAPIBase + "/files/" + url.PathEscape(fileID) + "?alt=media"
	})
}

func resolveOAuthStream(ctx context.Context, cloudURL, prefix, kind string, buildURL func(string) string) (string, map[string]string, error) {
	rest := strings.TrimPrefix(cloudURL, prefix)
	rawSrcID, rawItemID, ok := strings.Cut(rest, "/")
	if !ok || rawSrcID == "" || rawItemID == "" {
		return "", nil, errors.New("无效的云盘歌曲地址")
	}
	srcID, err := url.PathUnescape(rawSrcID)
	if err != nil {
		srcID = rawSrcID
	}
	itemID, err := url.PathUnescape(rawItemID)
	if err != nil {
		itemID = rawItemID
	}
	oauthRegistry.RLock()
	entry := oauthRegistry.byID[srcID]
	oauthRegistry.RUnlock()
	if entry == nil {
		return "", nil, errors.New("未找到对应的云盘配置")
	}
	entry.mu.Lock()
	sourceKind := entry.source.EffectiveType()
	entry.mu.Unlock()
	if sourceKind != kind {
		return "", nil, errors.New("未找到对应的云盘配置")
	}
	tok, err := entry.validAccessToken(ctx)
	if err != nil || tok == "" {
		if err != nil {
			return "", nil, err
		}
		return "", nil, errors.New("云盘登录已失效，请重新授权")
	}
	return buildURL(itemID), map[string]string{"Authorization": "Bearer " + tok}, nil
}

func registerOAuthSources(sources []CloudSource) {
	oauthRegistry.Lock()
	defer oauthRegistry.Unlock()
	if oauthRegistry.byID == nil {
		oauthRegistry.byID = map[string]*oauthTokenEntry{}
	}
	nextByID := map[string]*oauthTokenEntry{}
	nextByType := map[string]*oauthTokenEntry{}
	for _, s := range sources {
		kind := s.EffectiveType()
		if kind != CloudTypeOneDrive && kind != CloudTypeGoogleDrive && kind != CloudTypeBaidu {
			continue
		}
		if existing, ok := oauthRegistry.byID[s.ID]; ok {
			existing.mu.Lock()
			sameClient := existing.source.EffectiveType() == kind && existing.source.ClientID == s.ClientID && existing.source.ClientSecret == s.ClientSecret
			_, known := existing.knownRefreshes[s.RefreshToken]
			sameCred := existing.refreshToken == s.RefreshToken || (s.RefreshToken != "" && known)
			if sameClient && sameCred {
				if existing.refreshToken != s.RefreshToken {
					// Preserve the newest rotated refresh token already held in memory.
					s.RefreshToken = existing.refreshToken
				}
				existing.source = s
			}
			existing.mu.Unlock()
			if sameClient && sameCred {
				nextByID[s.ID] = existing
				nextByType[kind] = existing
				continue
			}
		}
		known := map[string]struct{}{}
		if s.RefreshToken != "" {
			known[s.RefreshToken] = struct{}{}
		}
		entry := &oauthTokenEntry{
			source:         s,
			knownRefreshes: known,
			refreshToken:   s.RefreshToken,
		}
		nextByID[s.ID] = entry
		nextByType[kind] = entry
	}
	oauthRegistry.byID = nextByID
	oauthRegistry.byType = nextByType
}

func oauthEntryForSource(src CloudSource) *oauthTokenEntry {
	oauthRegistry.RLock()
	entry := oauthRegistry.byID[src.ID]
	oauthRegistry.RUnlock()
	if entry != nil {
		return entry
	}
	return &oauthTokenEntry{source: src, refreshToken: src.RefreshToken}
}

func oauthHeadersForURL(rawURL string) map[string]string {
	var kind string
	switch {
	case strings.HasPrefix(rawURL, oneDriveAPIBase+"/"):
		kind = CloudTypeOneDrive
	case strings.HasPrefix(rawURL, googleDriveAPIBase+"/") || strings.HasPrefix(rawURL, googleUploadAPIBase+"/"):
		kind = CloudTypeGoogleDrive
	default:
		return nil
	}
	oauthRegistry.RLock()
	entry := oauthRegistry.byType[kind]
	oauthRegistry.RUnlock()
	if entry == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tok, err := entry.validAccessToken(ctx)
	if err != nil || tok == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + tok}
}

func (e *oauthTokenEntry) validAccessToken(ctx context.Context) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.accessToken != "" && time.Now().Before(e.expiresAt.Add(-2*time.Minute)) {
		return e.accessToken, nil
	}
	if e.refreshToken == "" {
		return "", errors.New("云盘登录已失效，请重新授权")
	}
	tok, newRefresh, expiresIn, err := refreshOAuthToken(ctx, e.source.EffectiveType(), e.source.ClientID, e.source.ClientSecret, e.refreshToken)
	if err != nil {
		return "", err
	}
	e.accessToken = tok
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	e.expiresAt = time.Now().Add(time.Duration(expiresIn) * time.Second)
	if newRefresh != "" && newRefresh != e.refreshToken {
		if e.knownRefreshes == nil {
			e.knownRefreshes = map[string]struct{}{}
		}
		if e.refreshToken != "" {
			e.knownRefreshes[e.refreshToken] = struct{}{}
		}
		e.knownRefreshes[newRefresh] = struct{}{}
		e.refreshToken = newRefresh
		if e.source.ID != "" {
			notifyCloudTokenRefresh(e.source.ID, newRefresh)
		}
	}
	return e.accessToken, nil
}

func randomBase64URL(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// AuthorizeCloudOAuth runs an RFC 8252 loopback OAuth 2.0 + PKCE flow in the
// user's system browser for OneDrive or Google Drive.
func AuthorizeCloudOAuth(ctx context.Context, provider, clientID, clientSecret string, openBrowser func(string) error) (OAuthResult, error) {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	if clientID == "" {
		return OAuthResult{}, errors.New("请先填写 Client ID")
	}
	if openBrowser == nil {
		return OAuthResult{}, errors.New("无法打开浏览器")
	}

	verifier, err := randomBase64URL(32)
	if err != nil {
		return OAuthResult{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state, err := randomBase64URL(16)
	if err != nil {
		return OAuthResult{}, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return OAuthResult{}, fmt.Errorf("无法启动本地授权回调服务：%w", err)
	}
	defer ln.Close()

	redirectURI := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)

	type callbackResult struct {
		code string
		err  error
	}
	done := make(chan callbackResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("state") != state {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, oauthResultHTML(false, "授权状态校验失败，请返回 Sonora 重试。"))
			return
		}
		if errMsg := q.Get("error"); errMsg != "" {
			desc := cmp.Or(q.Get("error_description"), errMsg)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, oauthResultHTML(false, "授权被取消或失败："+desc))
			select {
			case done <- callbackResult{err: fmt.Errorf("授权失败：%s", desc)}:
			default:
			}
			return
		}
		code := q.Get("code")
		if code == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, oauthResultHTML(false, "未收到授权码，请返回 Sonora 重试。"))
			select {
			case done <- callbackResult{err: errors.New("未收到授权码")}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, oauthResultHTML(true, "授权成功！现在可以关闭此页面并返回 Sonora。"))
		select {
		case done <- callbackResult{code: code}:
		default:
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		shutCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	var authEndpoint, scopes string
	extra := url.Values{}
	switch provider {
	case CloudTypeOneDrive:
		authEndpoint = oneDriveAuthEndpoint
		scopes = "Files.ReadWrite offline_access User.Read"
	case CloudTypeGoogleDrive:
		authEndpoint = googleAuthEndpoint
		scopes = "https://www.googleapis.com/auth/drive.readonly https://www.googleapis.com/auth/drive.file https://www.googleapis.com/auth/userinfo.email"
		extra.Set("access_type", "offline")
		extra.Set("prompt", "consent")
	case CloudTypeBaidu:
		authEndpoint = baiduAuthEndpoint
		scopes = "basic,netdisk"
	default:
		return OAuthResult{}, fmt.Errorf("不支持的云盘类型：%s", provider)
	}

	params := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"scope":                 {scopes},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	for k, v := range extra {
		params[k] = v
	}

	authURL := authEndpoint + "?" + params.Encode()
	if err := openBrowser(authURL); err != nil {
		return OAuthResult{}, fmt.Errorf("无法打开系统浏览器：%w", err)
	}

	var code string
	useConsumers := false
	select {
	case res := <-done:
		if res.err != nil {
			// If the user's Azure app is configured as "Personal accounts only"
			// (Consumer audience), Microsoft rejects /common/ and requires /consumers/.
			if provider == CloudTypeOneDrive && strings.Contains(res.err.Error(), "userAudience") && strings.Contains(authEndpoint, "/common/") {
				useConsumers = true
				consumerAuth := strings.Replace(authEndpoint, "/common/", "/consumers/", 1)
				if err := openBrowser(consumerAuth + "?" + params.Encode()); err != nil {
					return OAuthResult{}, fmt.Errorf("无法打开系统浏览器：%w", err)
				}
				select {
				case res2 := <-done:
					if res2.err != nil {
						return OAuthResult{}, res2.err
					}
					code = res2.code
				case <-ctx.Done():
					return OAuthResult{}, errors.New("等待浏览器授权超时或已取消")
				}
			} else {
				return OAuthResult{}, res.err
			}
		} else {
			code = res.code
		}
	case <-ctx.Done():
		return OAuthResult{}, errors.New("等待浏览器授权超时或已取消")
	}

	tok, refresh, expiresIn, err := exchangeOAuthCode(ctx, provider, clientID, clientSecret, redirectURI, code, verifier, useConsumers)
	if err != nil {
		return OAuthResult{}, err
	}
	if refresh == "" {
		return OAuthResult{}, errors.New("未获取到长期刷新令牌（refresh_token），请确认已勾选离线访问权限后重试")
	}

	email, name := fetchOAuthAccountInfo(ctx, provider, tok)
	return OAuthResult{
		AccessToken:  tok,
		RefreshToken: refresh,
		ExpiresIn:    expiresIn,
		Email:        email,
		DisplayName:  name,
	}, nil
}

func oauthResultHTML(ok bool, msg string) string {
	title := "Sonora 授权成功"
	color := "#10b981"
	if !ok {
		title = "Sonora 授权失败"
		color = "#ef4444"
	}
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%s</title>
<style>body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:#141416;color:#f5f5f7;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.card{background:#1c1c1f;border:1px solid rgba(255,255,255,.08);border-radius:14px;padding:28px 34px;max-width:420px;text-align:center}
h2{margin:0 0 10px;color:%s;font-size:18px}p{margin:0;color:rgba(235,235,245,.75);font-size:14px;line-height:1.5}</style>
</head><body><div class="card"><h2>%s</h2><p>%s</p></div></body></html>`, title, color, title, html.EscapeString(msg))
}

func exchangeOAuthCode(ctx context.Context, provider, clientID, clientSecret, redirectURI, code, verifier string, consumers bool) (access, refresh string, expiresIn int, err error) {
	tokenURL := oneDriveTokenEndpoint
	if provider == CloudTypeOneDrive && consumers {
		tokenURL = strings.Replace(oneDriveTokenEndpoint, "/common/", "/consumers/", 1)
	}
	switch provider {
	case CloudTypeGoogleDrive:
		tokenURL = googleTokenEndpoint
	case CloudTypeBaidu:
		tokenURL = baiduTokenEndpoint
	}
	form := url.Values{
		"client_id":    {clientID},
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
	}
	if provider != CloudTypeBaidu {
		form.Set("code_verifier", verifier)
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	return doTokenRequest(ctx, tokenURL, form)
}

func refreshOAuthToken(ctx context.Context, provider, clientID, clientSecret, refreshToken string) (access, newRefresh string, expiresIn int, err error) {
	tokenURL := oneDriveTokenEndpoint
	switch provider {
	case CloudTypeGoogleDrive:
		tokenURL = googleTokenEndpoint
	case CloudTypeBaidu:
		tokenURL = baiduTokenEndpoint
	}
	form := url.Values{
		"client_id":     {clientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	access, newRefresh, expiresIn, err = doTokenRequest(ctx, tokenURL, form)
	if err != nil && provider == CloudTypeOneDrive && strings.Contains(tokenURL, "/common/") {
		consumerURL := strings.Replace(tokenURL, "/common/", "/consumers/", 1)
		if a2, r2, e2, err2 := doTokenRequest(ctx, consumerURL, form); err2 == nil {
			return a2, r2, e2, nil
		}
	}
	return access, newRefresh, expiresIn, err
}

func doTokenRequest(ctx context.Context, tokenURL string, form url.Values) (string, string, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", "", 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
	if err != nil {
		return "", "", 0, err
	}
	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	_ = json.Unmarshal(body, &parsed)
	if res.StatusCode < 200 || res.StatusCode >= 300 || parsed.AccessToken == "" {
		msg := cmp.Or(parsed.ErrorDesc, parsed.Error, res.Status)
		return "", "", 0, fmt.Errorf("获取令牌失败：%s", msg)
	}
	return parsed.AccessToken, parsed.RefreshToken, parsed.ExpiresIn, nil
}

func fetchOAuthAccountInfo(ctx context.Context, provider, accessToken string) (email, name string) {
	var endpoint string
	switch provider {
	case CloudTypeOneDrive:
		endpoint = oneDriveAPIBase + "/me?$select=displayName,mail,userPrincipalName"
	case CloudTypeGoogleDrive:
		endpoint = googleUserInfoURL
	case CloudTypeBaidu:
		endpoint = baiduPanAPIBase + "/xpan/nas?method=uinfo&access_token=" + url.QueryEscape(accessToken)
	default:
		return "", ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", ""
	}
	if provider != CloudTypeBaidu {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", ""
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", ""
	}
	var info struct {
		DisplayName       string `json:"displayName"`
		Name              string `json:"name"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
		Email             string `json:"email"`
		BaiduName         string `json:"baidu_name"`
		NetdiskName       string `json:"netdisk_name"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&info) == nil {
		email = cmp.Or(info.Email, info.Mail, info.UserPrincipalName, info.BaiduName, info.NetdiskName)
		name = cmp.Or(info.DisplayName, info.Name, info.NetdiskName, info.BaiduName)
	}
	return email, name
}

// ---------- OneDrive (Microsoft Graph API) ----------

type odListResp struct {
	Value    []odItem `json:"value"`
	NextLink string   `json:"@odata.nextLink"`
}

type odItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	ETag         string `json:"eTag"`
	LastModified string `json:"lastModifiedDateTime"`
	Deleted      *struct {
		State string `json:"state"`
	} `json:"deleted"`
	ParentReference *struct {
		ID string `json:"id"`
	} `json:"parentReference"`
	Folder *struct {
		ChildCount int `json:"childCount"`
	} `json:"folder"`
	Audio *struct {
		Title       string `json:"title"`
		Artist      string `json:"artist"`
		AlbumArtist string `json:"albumArtist"`
		Album       string `json:"album"`
		Duration    int64  `json:"duration"`
	} `json:"audio"`
}

func walkOneDriveDelta(ctx context.Context, src CloudSource, folder, authHeader string, seenFiles map[string]bool) ([]davFile, bool, bool) {
	deltaURL := oneDriveAPIBase + "/me/drive/root/delta?$select=id,name,size,eTag,lastModifiedDateTime,folder,audio,deleted,parentReference&$top=500"
	if folder != "" {
		deltaURL = oneDriveAPIBase + "/me/drive/root:/" + escapeOneDrivePath(folder) + ":/delta?$select=id,name,size,eTag,lastModifiedDateTime,folder,audio,deleted,parentReference&$top=500"
	}

	var all []odItem
	nextURL := deltaURL
	for page := 0; nextURL != "" && page < 100; page++ {
		if ctx.Err() != nil {
			return nil, false, false
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, false, false
		}
		req.Header.Set("Authorization", authHeader)
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, false, false
		}
		var body odListResp
		err = json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(&body)
		status := res.StatusCode
		res.Body.Close()
		if status != http.StatusOK || err != nil {
			return nil, false, false
		}
		all = append(all, body.Value...)
		nextURL = body.NextLink
	}
	if ctx.Err() != nil || nextURL != "" {
		return nil, false, false
	}

	lrcByParentStem := map[string]string{}
	lrcByStem := map[string]string{}
	for _, it := range all {
		if it.Deleted != nil || it.Folder != nil || it.Name == "" {
			continue
		}
		lower := strings.ToLower(it.Name)
		if strings.HasSuffix(lower, ".lrc") {
			stem := strings.TrimSuffix(lower, ".lrc")
			lrcURL := oauthVirtualURL(CloudTypeOneDrive, src.ID, it.ID)
			if it.ParentReference != nil && it.ParentReference.ID != "" {
				lrcByParentStem[it.ParentReference.ID+"\x00"+stem] = lrcURL
			} else {
				lrcByStem[stem] = lrcURL
			}
		}
	}

	files := make([]davFile, 0, 128)
	for _, it := range all {
		if it.Deleted != nil || it.Folder != nil || !Supported(it.Name) {
			continue
		}
		contentURL := oauthVirtualURL(CloudTypeOneDrive, src.ID, it.ID)
		if seenFiles[contentURL] {
			continue
		}
		seenFiles[contentURL] = true
		ext := strings.ToLower(path.Ext(it.Name))
		stem := strings.TrimSuffix(strings.ToLower(it.Name), ext)
		var lrcURL string
		if it.ParentReference != nil && it.ParentReference.ID != "" {
			lrcURL = lrcByParentStem[it.ParentReference.ID+"\x00"+stem]
		} else {
			lrcURL = lrcByStem[stem]
		}
		df := davFile{
			URL:          contentURL,
			Name:         it.Name,
			Ext:          ext,
			Size:         it.Size,
			ETag:         it.ETag,
			LastModified: it.LastModified,
			LyricsURL:    lrcURL,
			AuthHeader:   authHeader,
			Source:       src,
		}
		if it.Audio != nil {
			df.MetaTitle = it.Audio.Title
			df.MetaArtist = cmp.Or(it.Audio.Artist, it.Audio.AlbumArtist)
			df.MetaAlbum = it.Audio.Album
			df.MetaDuration = it.Audio.Duration
			df.SkipRange = true
		}
		files = append(files, df)
		if len(files) >= maxCloudFiles {
			return files, true, true
		}
	}
	return files, true, false
}

func walkOneDrive(ctx context.Context, src CloudSource, seenFiles map[string]bool) ([]davFile, []string) {
	entry := oauthEntryForSource(src)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return nil, []string{fmt.Sprintf("OneDrive「%s」：%v", cmp.Or(src.Name, "OneDrive"), err)}
	}
	authHeader := "Bearer " + token

	folder := strings.Trim(strings.TrimSpace(src.Folder), "/")
	if files, ok, truncated := walkOneDriveDelta(ctx, src, folder, authHeader, seenFiles); ok {
		if truncated {
			return files, []string{fmt.Sprintf("OneDrive「%s」曲目数量已达上限，部分文件未扫描", cmp.Or(src.Name, "OneDrive"))}
		}
		return files, nil
	}

	startURL := oneDriveAPIBase + "/me/drive/root/children?$select=id,name,size,eTag,lastModifiedDateTime,folder,audio&$top=200"
	if folder != "" {
		escaped := escapeOneDrivePath(folder)
		startURL = oneDriveAPIBase + "/me/drive/root:/" + escaped + ":/children?$select=id,name,size,eTag,lastModifiedDateTime,folder,audio&$top=200"
	}

	type dirJob struct {
		url   string
		depth int
	}

	var (
		mu          sync.Mutex
		queue       = []dirJob{{url: startURL, depth: 0}}
		active      = 0
		visitedDirs = 0
		files       []davFile
		warnings    []string
	)
	cond := sync.NewCond(&mu)

	// Cancel wakes waiting workers immediately.
	stopWake := context.AfterFunc(ctx, func() {
		mu.Lock()
		cond.Broadcast()
		mu.Unlock()
	})
	defer stopWake()

	workers := 8
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				for len(queue) == 0 && active > 0 && ctx.Err() == nil && visitedDirs < maxCloudDirs && len(files) < maxCloudFiles {
					cond.Wait()
				}
				if len(queue) == 0 || ctx.Err() != nil || visitedDirs >= maxCloudDirs || len(files) >= maxCloudFiles {
					mu.Unlock()
					return
				}
				cur := queue[0]
				queue = queue[1:]
				visitedDirs++
				active++
				mu.Unlock()

				items, err := listOneDriveDir(ctx, cur.url, authHeader)

				mu.Lock()
				active--
				if err != nil {
					if ctx.Err() == nil {
						warnings = append(warnings, fmt.Sprintf("OneDrive「%s」目录读取失败：%v", cmp.Or(src.Name, "OneDrive"), err))
					}
					cond.Broadcast()
					mu.Unlock()
					continue
				}

				dirCover := ""
				lrcByStem := map[string]string{}
				for _, it := range items {
					if it.Folder != nil {
						continue
					}
					lower := strings.ToLower(it.Name)
					contentURL := oauthVirtualURL(CloudTypeOneDrive, src.ID, it.ID)
					if dirCover == "" && (lower == "cover.jpg" || lower == "cover.png" || lower == "folder.jpg" || lower == "folder.png") && it.Size <= 10<<20 {
						dirCover = contentURL
					}
					if strings.HasSuffix(lower, ".lrc") {
						stem := strings.TrimSuffix(lower, ".lrc")
						lrcByStem[stem] = contentURL
					}
				}

				for _, it := range items {
					if it.Folder != nil {
						if cur.depth < maxCloudDepth && !skipCloudFolderName(it.Name) {
							childURL := oneDriveAPIBase + "/me/drive/items/" + url.PathEscape(it.ID) + "/children?$select=id,name,size,eTag,lastModifiedDateTime,folder,audio&$top=200"
							queue = append(queue, dirJob{url: childURL, depth: cur.depth + 1})
						}
						continue
					}
					if !Supported(it.Name) {
						continue
					}
					contentURL := oauthVirtualURL(CloudTypeOneDrive, src.ID, it.ID)
					if seenFiles[contentURL] {
						continue
					}
					seenFiles[contentURL] = true
					ext := strings.ToLower(path.Ext(it.Name))
					stem := strings.TrimSuffix(strings.ToLower(it.Name), ext)
					df := davFile{
						URL:          contentURL,
						Name:         it.Name,
						Ext:          ext,
						Size:         it.Size,
						ETag:         it.ETag,
						LastModified: it.LastModified,
						DirCoverURL:  dirCover,
						LyricsURL:    lrcByStem[stem],
						AuthHeader:   authHeader,
						Source:       src,
					}
					if it.Audio != nil {
						df.MetaTitle = it.Audio.Title
						df.MetaArtist = cmp.Or(it.Audio.Artist, it.Audio.AlbumArtist)
						df.MetaAlbum = it.Audio.Album
						df.MetaDuration = it.Audio.Duration
						df.SkipRange = true
					}
					files = append(files, df)
					if len(files) >= maxCloudFiles {
						break
					}
				}
				cond.Broadcast()
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		warnings = append(warnings, fmt.Sprintf("OneDrive「%s」扫描超时或已取消", cmp.Or(src.Name, "OneDrive")))
	} else if len(queue) > 0 {
		warnings = append(warnings, fmt.Sprintf("OneDrive「%s」目录或曲目数量已达上限，部分文件未扫描", cmp.Or(src.Name, "OneDrive")))
	}
	return files, warnings
}

func skipCloudFolderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "album artwork", ".git", ".ds_store", "__macosx", "cache", "caches", "previous itunes libraries":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func listOneDriveDir(ctx context.Context, startURL, authHeader string) ([]odItem, error) {
	var all []odItem
	nextURL := startURL
	for page := 0; nextURL != "" && page < 50; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", authHeader)
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body odListResp
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
		status := res.StatusCode
		statusText := res.Status
		res.Body.Close()
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %s", statusText)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, body.Value...)
		nextURL = body.NextLink
	}
	return all, nil
}

func escapeOneDrivePath(folder string) string {
	parts := strings.Split(strings.Trim(folder, "/"), "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func uploadOneDrive(ctx context.Context, localPath string, dst CloudSource) (string, error) {
	entry := oauthEntryForSource(dst)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return "", err
	}
	authHeader := "Bearer " + token

	folder := strings.Trim(strings.TrimSpace(dst.Folder), "/")
	itemURL, err := uploadOneDriveFile(ctx, dst.ID, localPath, folder, authHeader)
	if err != nil {
		return "", err
	}
	lrcPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".lrc"
	if info, err := os.Stat(lrcPath); err == nil && info.Mode().IsRegular() && info.Size() <= 2<<20 {
		_, _ = uploadOneDriveFile(ctx, dst.ID, lrcPath, folder, authHeader)
	}
	return itemURL, nil
}

func uploadOneDriveFile(ctx context.Context, sourceID, localPath, folder, authHeader string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	remotePath := url.PathEscape(filepath.Base(localPath))
	if folder != "" {
		remotePath = escapeOneDrivePath(folder) + "/" + remotePath
	}
	uploadClient := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: cloudHTTPClient.CheckRedirect}

	// Simple PUT for files <= 4 MB.
	if info.Size() <= 4<<20 {
		target := oneDriveAPIBase + "/me/drive/root:/" + remotePath + ":/content"
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, f)
		if err != nil {
			return "", err
		}
		req.ContentLength = info.Size()
		req.Header.Set("Authorization", authHeader)
		res, err := uploadClient.Do(req)
		if err != nil {
			return "", err
		}
		defer res.Body.Close()
		var item odItem
		_ = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&item)
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return "", fmt.Errorf("OneDrive 上传失败（HTTP %s）", res.Status)
		}
		if item.ID != "" {
			return oauthVirtualURL(CloudTypeOneDrive, sourceID, item.ID), nil
		}
		return target, nil
	}

	// Resumable upload session for large files (> 4 MB).
	sessionURL := oneDriveAPIBase + "/me/drive/root:/" + remotePath + ":/createUploadSession"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sessionURL, strings.NewReader(`{"item":{"@microsoft.graph.conflictBehavior":"replace"}}`))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("Content-Type", "application/json")
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	var sess struct {
		UploadURL string `json:"uploadUrl"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&sess)
	status := res.StatusCode
	statusText := res.Status
	res.Body.Close()
	if status < 200 || status >= 300 || err != nil || sess.UploadURL == "" {
		return "", fmt.Errorf("OneDrive 创建上传会话失败（HTTP %s）", statusText)
	}

	// Microsoft Graph requires chunk sizes to be exact multiples of 320 KiB.
	const chunkSize = 16 * (320 << 10)
	buf := make([]byte, chunkSize)
	var itemID string
	for offset := int64(0); offset < info.Size(); {
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return "", readErr
		}
		if n == 0 {
			break
		}
		end := offset + int64(n) - 1
		putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, sess.UploadURL, bytes.NewReader(buf[:n]))
		if err != nil {
			return "", err
		}
		putReq.ContentLength = int64(n)
		putReq.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, info.Size()))
		putRes, err := uploadClient.Do(putReq)
		if err != nil {
			return "", err
		}
		var item odItem
		_ = json.NewDecoder(io.LimitReader(putRes.Body, 256<<10)).Decode(&item)
		putStatus := putRes.StatusCode
		putStatusText := putRes.Status
		putRes.Body.Close()
		if putStatus < 200 || putStatus >= 300 {
			return "", fmt.Errorf("OneDrive 分片上传失败（HTTP %s）", putStatusText)
		}
		if item.ID != "" {
			itemID = item.ID
		}
		offset += int64(n)
	}
	if itemID != "" {
		return oauthVirtualURL(CloudTypeOneDrive, sourceID, itemID), nil
	}
	return sess.UploadURL, nil
}

// ---------- Google Drive (API v3) ----------

const gdriveFolderMIME = "application/vnd.google-apps.folder"

type gdListResp struct {
	NextPageToken string   `json:"nextPageToken"`
	Files         []gdFile `json:"files"`
}

type gdFile struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	MimeType     string `json:"mimeType"`
	Size         string `json:"size"`
	MD5Checksum  string `json:"md5Checksum"`
	ModifiedTime string `json:"modifiedTime"`
}

func walkGoogleDrive(ctx context.Context, src CloudSource, seenFiles map[string]bool) ([]davFile, []string) {
	entry := oauthEntryForSource(src)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return nil, []string{fmt.Sprintf("Google Drive「%s」：%v", cmp.Or(src.Name, "Google Drive"), err)}
	}
	authHeader := "Bearer " + token

	rootID, err := resolveGoogleDriveFolder(ctx, src.Folder, authHeader)
	if err != nil {
		return nil, []string{fmt.Sprintf("Google Drive「%s」：%v", cmp.Or(src.Name, "Google Drive"), err)}
	}

	type dirJob struct {
		id    string
		depth int
	}
	queue := []dirJob{{id: rootID, depth: 0}}
	visitedDirs := 0
	var files []davFile
	var warnings []string

	for len(queue) > 0 && visitedDirs < maxCloudDirs && len(files) < maxCloudFiles {
		if ctx.Err() != nil {
			break
		}
		cur := queue[0]
		queue = queue[1:]
		visitedDirs++

		items, err := listGoogleDriveDir(ctx, cur.id, authHeader)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Google Drive「%s」目录读取失败：%v", cmp.Or(src.Name, "Google Drive"), err))
			continue
		}

		dirCover := ""
		lrcByStem := map[string]string{}
		for _, it := range items {
			if it.MimeType == gdriveFolderMIME {
				continue
			}
			lower := strings.ToLower(it.Name)
			size, _ := strconv.ParseInt(it.Size, 10, 64)
			mediaURL := oauthVirtualURL(CloudTypeGoogleDrive, src.ID, it.ID)
			if dirCover == "" && (lower == "cover.jpg" || lower == "cover.png" || lower == "folder.jpg" || lower == "folder.png") && size <= 10<<20 {
				dirCover = mediaURL
			}
			if strings.HasSuffix(lower, ".lrc") {
				stem := strings.TrimSuffix(lower, ".lrc")
				lrcByStem[stem] = mediaURL
			}
		}

		for _, it := range items {
			if it.MimeType == gdriveFolderMIME {
				if cur.depth < maxCloudDepth {
					queue = append(queue, dirJob{id: it.ID, depth: cur.depth + 1})
				}
				continue
			}
			if !Supported(it.Name) {
				continue
			}
			mediaURL := oauthVirtualURL(CloudTypeGoogleDrive, src.ID, it.ID)
			if seenFiles[mediaURL] {
				continue
			}
			seenFiles[mediaURL] = true
			size, _ := strconv.ParseInt(it.Size, 10, 64)
			ext := strings.ToLower(path.Ext(it.Name))
			stem := strings.TrimSuffix(strings.ToLower(it.Name), ext)
			files = append(files, davFile{
				URL:          mediaURL,
				Name:         it.Name,
				Ext:          ext,
				Size:         size,
				ETag:         it.MD5Checksum,
				LastModified: it.ModifiedTime,
				DirCoverURL:  dirCover,
				LyricsURL:    lrcByStem[stem],
				AuthHeader:   authHeader,
				Source:       src,
			})
			if len(files) >= maxCloudFiles {
				break
			}
		}
	}
	if ctx.Err() != nil {
		warnings = append(warnings, fmt.Sprintf("Google Drive「%s」扫描超时或已取消", cmp.Or(src.Name, "Google Drive")))
	} else if len(queue) > 0 || len(files) >= maxCloudFiles {
		warnings = append(warnings, fmt.Sprintf("Google Drive「%s」目录或曲目数量已达上限，部分文件未扫描", cmp.Or(src.Name, "Google Drive")))
	}
	return files, warnings
}

func escapeDriveQuery(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, "'", `\'`)
}

func isLikelyGoogleDriveID(s string) bool {
	if len(s) < 10 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func resolveGoogleDriveFolder(ctx context.Context, folder, authHeader string) (string, error) {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" {
		return "root", nil
	}
	// Walk path segments like "Music/Lossless" from root; if not found and single segment looks like a Drive ID, treat as folder ID.
	segments := strings.Split(folder, "/")
	parentID := "root"
	for _, seg := range segments {
		escaped := escapeDriveQuery(seg)
		q := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = '%s' and trashed = false", escaped, parentID, gdriveFolderMIME)
		u := googleDriveAPIBase + "/files?q=" + url.QueryEscape(q) + "&fields=files(id,name)&pageSize=5"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", authHeader)
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return "", err
		}
		var list gdListResp
		err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&list)
		status := res.StatusCode
		res.Body.Close()
		if status != http.StatusOK {
			return "", fmt.Errorf("Google Drive HTTP %d", status)
		}
		if err != nil {
			return "", err
		}
		if len(list.Files) > 0 {
			parentID = list.Files[0].ID
			continue
		}
		if len(segments) == 1 && isLikelyGoogleDriveID(folder) {
			// Allow passing a raw Google Drive folder ID directly.
			return folder, nil
		}
		return "", fmt.Errorf("找不到目录「%s」", folder)
	}
	return parentID, nil
}

func listGoogleDriveDir(ctx context.Context, folderID, authHeader string) ([]gdFile, error) {
	var all []gdFile
	pageToken := ""
	q := fmt.Sprintf("'%s' in parents and trashed = false", escapeDriveQuery(folderID))
	for page := 0; page < 50; page++ {
		params := url.Values{
			"q":        {q},
			"fields":   {"nextPageToken,files(id,name,mimeType,size,md5Checksum,modifiedTime)"},
			"pageSize": {"200"},
		}
		if pageToken != "" {
			params.Set("pageToken", pageToken)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleDriveAPIBase+"/files?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", authHeader)
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body gdListResp
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
		status := res.StatusCode
		statusText := res.Status
		res.Body.Close()
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %s", statusText)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, body.Files...)
		pageToken = body.NextPageToken
		if pageToken == "" {
			break
		}
	}
	return all, nil
}

func uploadGoogleDrive(ctx context.Context, localPath string, dst CloudSource) (string, error) {
	entry := oauthEntryForSource(dst)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return "", err
	}
	authHeader := "Bearer " + token

	folderID, err := resolveGoogleDriveFolder(ctx, dst.Folder, authHeader)
	if err != nil {
		return "", err
	}
	mediaURL, err := uploadGoogleDriveFile(ctx, dst.ID, localPath, folderID, authHeader)
	if err != nil {
		return "", err
	}
	lrcPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".lrc"
	if info, err := os.Stat(lrcPath); err == nil && info.Mode().IsRegular() && info.Size() <= 2<<20 {
		_, _ = uploadGoogleDriveFile(ctx, dst.ID, lrcPath, folderID, authHeader)
	}
	return mediaURL, nil
}

func uploadGoogleDriveFile(ctx context.Context, sourceID, localPath, folderID, authHeader string) (string, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	meta, _ := json.Marshal(map[string]any{
		"name":    filepath.Base(localPath),
		"parents": []string{folderID},
	})
	initReq, err := http.NewRequestWithContext(ctx, http.MethodPost, googleUploadAPIBase+"/files?uploadType=resumable", bytes.NewReader(meta))
	if err != nil {
		return "", err
	}
	initReq.Header.Set("Authorization", authHeader)
	initReq.Header.Set("Content-Type", "application/json; charset=UTF-8")
	initReq.Header.Set("X-Upload-Content-Length", strconv.FormatInt(info.Size(), 10))
	initRes, err := cloudHTTPClient.Do(initReq)
	if err != nil {
		return "", err
	}
	uploadLoc := initRes.Header.Get("Location")
	initStatus := initRes.StatusCode
	initStatusText := initRes.Status
	initRes.Body.Close()
	if initStatus < 200 || initStatus >= 300 || uploadLoc == "" {
		return "", fmt.Errorf("Google Drive 初始化上传失败（HTTP %s）", initStatusText)
	}

	uploadClient := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: cloudHTTPClient.CheckRedirect}
	putReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadLoc, f)
	if err != nil {
		return "", err
	}
	putReq.ContentLength = info.Size()
	putRes, err := uploadClient.Do(putReq)
	if err != nil {
		return "", err
	}
	defer putRes.Body.Close()
	var created gdFile
	_ = json.NewDecoder(io.LimitReader(putRes.Body, 256<<10)).Decode(&created)
	if putRes.StatusCode < 200 || putRes.StatusCode >= 300 {
		return "", fmt.Errorf("Google Drive 上传失败（HTTP %s）", putRes.Status)
	}
	if created.ID != "" {
		return oauthVirtualURL(CloudTypeGoogleDrive, sourceID, created.ID), nil
	}
	return uploadLoc, nil
}
