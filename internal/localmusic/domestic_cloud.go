package localmusic

import (
	"bytes"
	"cmp"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
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

const (
	baiduUA      = "pan.baidu.com"
	quarkUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) quark-cloud-drive/2.5.20 Chrome/100.0.4896.160 Electron/18.3.5.4-b478491100 Safari/537.36 Channel/pckk_other_ch"
	quarkReferer = "https://pan.quark.cn/"
	p115UA       = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36 115Browser/27.0.5.7"
)

var p115APIBase = "https://webapi.115.com"

var domesticRegistry struct {
	sync.RWMutex
	quarkByID map[string]CloudSource
	p115ByID  map[string]CloudSource
}

func registerDomesticSources(sources []CloudSource) {
	nextQuark := map[string]CloudSource{}
	next115 := map[string]CloudSource{}
	for _, s := range sources {
		switch s.EffectiveType() {
		case CloudTypeQuark:
			nextQuark[s.ID] = s
		case CloudType115:
			next115[s.ID] = s
		}
	}
	domesticRegistry.Lock()
	domesticRegistry.quarkByID = nextQuark
	domesticRegistry.p115ByID = next115
	domesticRegistry.Unlock()
}

// ---------- 百度网盘 (Baidu Netdisk xpan API) ----------

type baiduListResp struct {
	Errno int             `json:"errno"`
	List  []baiduFileItem `json:"list"`
}

type baiduFileItem struct {
	FsID           int64  `json:"fs_id"`
	Path           string `json:"path"`
	ServerFilename string `json:"server_filename"`
	Size           int64  `json:"size"`
	IsDir          int    `json:"isdir"`
	MD5            string `json:"md5"`
	ServerMTime    int64  `json:"server_mtime"`
}

type baiduMetasResp struct {
	Errno int `json:"errno"`
	List  []struct {
		FsID  int64  `json:"fs_id"`
		DLink string `json:"dlink"`
	} `json:"list"`
}

func walkBaidu(ctx context.Context, src CloudSource, seenFiles map[string]bool) ([]davFile, []string) {
	entry := oauthEntryForSource(src)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return nil, []string{fmt.Sprintf("百度网盘「%s」：%v", cmp.Or(src.Name, "百度网盘"), err)}
	}

	rootDir := strings.TrimSpace(src.Folder)
	if rootDir == "" {
		rootDir = "/"
	}
	if !strings.HasPrefix(rootDir, "/") {
		rootDir = "/" + rootDir
	}

	type dirJob struct {
		dir   string
		depth int
	}
	queue := []dirJob{{dir: rootDir, depth: 0}}
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

		items, err := listBaiduDir(ctx, cur.dir, token)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("百度网盘「%s」目录读取失败：%v", cmp.Or(src.Name, "百度网盘"), err))
			continue
		}

		lrcByStem := map[string]string{}
		for _, it := range items {
			if it.IsDir == 1 {
				continue
			}
			lower := strings.ToLower(it.ServerFilename)
			if strings.HasSuffix(lower, ".lrc") {
				stem := strings.TrimSuffix(lower, ".lrc")
				lrcByStem[stem] = fmt.Sprintf("baidu://%s/%d", src.ID, it.FsID)
			}
		}

		for _, it := range items {
			if it.IsDir == 1 {
				if cur.depth < maxCloudDepth {
					queue = append(queue, dirJob{dir: it.Path, depth: cur.depth + 1})
				}
				continue
			}
			if !Supported(it.ServerFilename) {
				continue
			}
			virtualURL := fmt.Sprintf("baidu://%s/%d", src.ID, it.FsID)
			if seenFiles[virtualURL] {
				continue
			}
			seenFiles[virtualURL] = true
			ext := strings.ToLower(path.Ext(it.ServerFilename))
			stem := strings.TrimSuffix(strings.ToLower(it.ServerFilename), ext)
			files = append(files, davFile{
				URL:          virtualURL,
				Name:         it.ServerFilename,
				Ext:          ext,
				Size:         it.Size,
				ETag:         it.MD5,
				LastModified: strconv.FormatInt(it.ServerMTime, 10),
				LyricsURL:    lrcByStem[stem],
				Headers:      map[string]string{"User-Agent": baiduUA},
				Source:       src,
			})
			if len(files) >= maxCloudFiles {
				break
			}
		}
	}
	return files, warnings
}

func listBaiduDir(ctx context.Context, dir, token string) ([]baiduFileItem, error) {
	var all []baiduFileItem
	const pageSize = 500
	for start := 0; start < 5000; start += pageSize {
		params := url.Values{
			"method":       {"list"},
			"dir":          {dir},
			"start":        {strconv.Itoa(start)},
			"limit":        {strconv.Itoa(pageSize)},
			"access_token": {token},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baiduPanAPIBase+"/xpan/file?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body baiduListResp
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
		if body.Errno != 0 {
			return nil, fmt.Errorf("百度网盘错误码 %d", body.Errno)
		}
		all = append(all, body.List...)
		if len(body.List) < pageSize {
			break
		}
	}
	return all, nil
}

func resolveBaiduStream(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	rest := strings.TrimPrefix(cloudURL, "baidu://")
	sourceID, fsIDStr, ok := strings.Cut(rest, "/")
	if !ok || sourceID == "" || fsIDStr == "" {
		return "", nil, errors.New("无效的百度网盘歌曲地址")
	}
	oauthRegistry.RLock()
	entry := oauthRegistry.byID[sourceID]
	if entry == nil {
		entry = oauthRegistry.byType[CloudTypeBaidu]
	}
	oauthRegistry.RUnlock()
	if entry == nil {
		return "", nil, errors.New("未找到对应的百度网盘配置")
	}
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return "", nil, err
	}

	params := url.Values{
		"method":       {"filemetas"},
		"fsids":        {"[" + fsIDStr + "]"},
		"dlink":        {"1"},
		"access_token": {token},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baiduPanAPIBase+"/xpan/multimedia?"+params.Encode(), nil)
	if err != nil {
		return "", nil, err
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	var body baiduMetasResp
	err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&body)
	status := res.StatusCode
	statusText := res.Status
	res.Body.Close()
	if status != http.StatusOK || err != nil {
		return "", nil, fmt.Errorf("获取百度网盘播放地址失败（HTTP %s）", statusText)
	}
	if body.Errno != 0 || len(body.List) == 0 || body.List[0].DLink == "" {
		return "", nil, fmt.Errorf("百度网盘未返回下载地址（errno=%d）", body.Errno)
	}
	sep := "?"
	if strings.Contains(body.List[0].DLink, "?") {
		sep = "&"
	}
	streamURL := body.List[0].DLink + sep + "access_token=" + url.QueryEscape(token)
	return streamURL, map[string]string{"User-Agent": baiduUA}, nil
}

func uploadBaidu(ctx context.Context, localPath string, dst CloudSource) (string, error) {
	entry := oauthEntryForSource(dst)
	token, err := entry.validAccessToken(ctx)
	if err != nil {
		return "", err
	}
	folder := strings.TrimSpace(dst.Folder)
	if folder == "" {
		folder = "/apps/Sonora"
	}
	if !strings.HasPrefix(folder, "/") {
		folder = "/" + folder
	}
	folder = strings.TrimRight(folder, "/")

	fsID, err := uploadBaiduFile(ctx, localPath, folder+"/"+filepath.Base(localPath), token)
	if err != nil {
		return "", err
	}
	lrcPath := strings.TrimSuffix(localPath, filepath.Ext(localPath)) + ".lrc"
	if info, err := os.Stat(lrcPath); err == nil && info.Mode().IsRegular() && info.Size() <= 2<<20 {
		_, _ = uploadBaiduFile(ctx, lrcPath, folder+"/"+filepath.Base(lrcPath), token)
	}
	return fmt.Sprintf("baidu://%s/%d", dst.ID, fsID), nil
}

func uploadBaiduFile(ctx context.Context, localPath, remotePath, token string) (int64, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}

	const blockSize = 4 << 20
	var blockMD5s []string
	buf := make([]byte, blockSize)
	for {
		n, readErr := io.ReadFull(f, buf)
		if n > 0 {
			sum := md5.Sum(buf[:n])
			blockMD5s = append(blockMD5s, hex.EncodeToString(sum[:]))
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if len(blockMD5s) == 0 {
		sum := md5.Sum(nil)
		blockMD5s = []string{hex.EncodeToString(sum[:])}
	}
	blockListJSON, _ := json.Marshal(blockMD5s)

	// Step 1: precreate
	preForm := url.Values{
		"path":       {remotePath},
		"size":       {strconv.FormatInt(info.Size(), 10)},
		"isdir":      {"0"},
		"autoinit":   {"1"},
		"rtype":      {"3"},
		"block_list": {string(blockListJSON)},
	}
	preURL := baiduPanAPIBase + "/xpan/file?method=precreate&access_token=" + url.QueryEscape(token)
	preReq, err := http.NewRequestWithContext(ctx, http.MethodPost, preURL, strings.NewReader(preForm.Encode()))
	if err != nil {
		return 0, err
	}
	preReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	preRes, err := cloudHTTPClient.Do(preReq)
	if err != nil {
		return 0, err
	}
	var preResp struct {
		Errno    int    `json:"errno"`
		UploadID string `json:"uploadid"`
	}
	_ = json.NewDecoder(io.LimitReader(preRes.Body, 256<<10)).Decode(&preResp)
	preRes.Body.Close()
	if preResp.Errno != 0 || preResp.UploadID == "" {
		return 0, fmt.Errorf("百度网盘预上传失败（errno=%d）", preResp.Errno)
	}

	// Step 2: upload chunks via superfile2
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	uploadClient := &http.Client{Timeout: 10 * time.Minute}
	for partSeq := 0; partSeq < len(blockMD5s); partSeq++ {
		n, readErr := io.ReadFull(f, buf)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return 0, readErr
		}
		var mpBuf bytes.Buffer
		mw := multipart.NewWriter(&mpBuf)
		part, err := mw.CreateFormFile("file", filepath.Base(localPath))
		if err != nil {
			return 0, err
		}
		if _, err := part.Write(buf[:n]); err != nil {
			return 0, err
		}
		_ = mw.Close()

		upParams := url.Values{
			"method":       {"upload"},
			"type":         {"tmpfile"},
			"access_token": {token},
			"path":         {remotePath},
			"uploadid":     {preResp.UploadID},
			"partseq":      {strconv.Itoa(partSeq)},
		}
		upReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baiduUploadAPIBase+"/pcs/superfile2?"+upParams.Encode(), &mpBuf)
		if err != nil {
			return 0, err
		}
		upReq.Header.Set("Content-Type", mw.FormDataContentType())
		upRes, err := uploadClient.Do(upReq)
		if err != nil {
			return 0, err
		}
		status := upRes.StatusCode
		statusText := upRes.Status
		io.Copy(io.Discard, io.LimitReader(upRes.Body, 64<<10))
		upRes.Body.Close()
		if status < 200 || status >= 300 {
			return 0, fmt.Errorf("百度网盘分片上传失败（HTTP %s）", statusText)
		}
	}

	// Step 3: create
	createForm := url.Values{
		"path":       {remotePath},
		"size":       {strconv.FormatInt(info.Size(), 10)},
		"isdir":      {"0"},
		"rtype":      {"3"},
		"uploadid":   {preResp.UploadID},
		"block_list": {string(blockListJSON)},
	}
	createURL := baiduPanAPIBase + "/xpan/file?method=create&access_token=" + url.QueryEscape(token)
	createReq, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, strings.NewReader(createForm.Encode()))
	if err != nil {
		return 0, err
	}
	createReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createRes, err := cloudHTTPClient.Do(createReq)
	if err != nil {
		return 0, err
	}
	var createResp struct {
		Errno int   `json:"errno"`
		FsID  int64 `json:"fs_id"`
	}
	_ = json.NewDecoder(io.LimitReader(createRes.Body, 256<<10)).Decode(&createResp)
	createRes.Body.Close()
	if createResp.Errno != 0 {
		return 0, fmt.Errorf("百度网盘合并文件失败（errno=%d）", createResp.Errno)
	}
	return createResp.FsID, nil
}

// ---------- 夸克网盘 (Quark Drive Web API) ----------

type quarkSortResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		List []quarkItem `json:"list"`
	} `json:"data"`
	Metadata struct {
		Total int `json:"_total"`
	} `json:"metadata"`
}

type quarkItem struct {
	FID       string `json:"fid"`
	FileName  string `json:"file_name"`
	Size      int64  `json:"size"`
	Dir       bool   `json:"dir"`
	MD5       string `json:"md5"`
	UpdatedAt int64  `json:"updated_at"`
}

type quarkDownloadResp struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    []struct {
		FID         string `json:"fid"`
		DownloadURL string `json:"download_url"`
	} `json:"data"`
}

func quarkHeaders(cookie string) map[string]string {
	return map[string]string{
		"Cookie":     strings.TrimSpace(cookie),
		"Referer":    quarkReferer,
		"User-Agent": quarkUA,
	}
}

// TestQuarkSource verifies that a user-supplied Quark Drive Cookie is valid.
func TestQuarkSource(ctx context.Context, cookie string) error {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return errors.New("请粘贴夸克网盘 Cookie")
	}
	_, err := listQuarkDir(ctx, "0", cookie, 1)
	return err
}

func walkQuark(ctx context.Context, src CloudSource, seenFiles map[string]bool) ([]davFile, []string) {
	cookie := strings.TrimSpace(src.Password)
	if cookie == "" {
		return nil, []string{fmt.Sprintf("夸克网盘「%s」：Cookie 为空", cmp.Or(src.Name, "夸克网盘"))}
	}
	rootFID, err := resolveQuarkFolder(ctx, src.Folder, cookie)
	if err != nil {
		return nil, []string{fmt.Sprintf("夸克网盘「%s」：%v", cmp.Or(src.Name, "夸克网盘"), err)}
	}

	type dirJob struct {
		fid   string
		depth int
	}
	queue := []dirJob{{fid: rootFID, depth: 0}}
	visitedDirs := 0
	var files []davFile
	var warnings []string
	hdrs := quarkHeaders(cookie)

	for len(queue) > 0 && visitedDirs < maxCloudDirs && len(files) < maxCloudFiles {
		if ctx.Err() != nil {
			break
		}
		cur := queue[0]
		queue = queue[1:]
		visitedDirs++

		items, err := listQuarkDir(ctx, cur.fid, cookie, 50)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("夸克网盘「%s」目录读取失败：%v", cmp.Or(src.Name, "夸克网盘"), err))
			continue
		}

		lrcByStem := map[string]string{}
		for _, it := range items {
			if it.Dir {
				continue
			}
			lower := strings.ToLower(it.FileName)
			if strings.HasSuffix(lower, ".lrc") {
				stem := strings.TrimSuffix(lower, ".lrc")
				lrcByStem[stem] = fmt.Sprintf("quark://%s/%s", src.ID, it.FID)
			}
		}

		for _, it := range items {
			if it.Dir {
				if cur.depth < maxCloudDepth {
					queue = append(queue, dirJob{fid: it.FID, depth: cur.depth + 1})
				}
				continue
			}
			if !Supported(it.FileName) {
				continue
			}
			virtualURL := fmt.Sprintf("quark://%s/%s", src.ID, it.FID)
			if seenFiles[virtualURL] {
				continue
			}
			seenFiles[virtualURL] = true
			ext := strings.ToLower(path.Ext(it.FileName))
			stem := strings.TrimSuffix(strings.ToLower(it.FileName), ext)
			files = append(files, davFile{
				URL:          virtualURL,
				Name:         it.FileName,
				Ext:          ext,
				Size:         it.Size,
				ETag:         it.MD5,
				LastModified: strconv.FormatInt(it.UpdatedAt, 10),
				LyricsURL:    lrcByStem[stem],
				Headers:      hdrs,
				Source:       src,
			})
			if len(files) >= maxCloudFiles {
				break
			}
		}
	}
	return files, warnings
}

func resolveQuarkFolder(ctx context.Context, folder, cookie string) (string, error) {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" {
		return "0", nil
	}
	segments := strings.Split(folder, "/")
	curFID := "0"
	for _, seg := range segments {
		items, err := listQuarkDir(ctx, curFID, cookie, 20)
		if err != nil {
			return "", err
		}
		found := ""
		for _, it := range items {
			if it.Dir && strings.EqualFold(it.FileName, seg) {
				found = it.FID
				break
			}
		}
		if found == "" {
			if len(segments) == 1 && len(seg) >= 16 {
				// Allow passing a raw Quark folder FID directly.
				return seg, nil
			}
			return "", fmt.Errorf("找不到目录「%s」", folder)
		}
		curFID = found
	}
	return curFID, nil
}

func listQuarkDir(ctx context.Context, pdirFID, cookie string, maxPages int) ([]quarkItem, error) {
	var all []quarkItem
	const pageSize = 100
	for page := 1; page <= maxPages; page++ {
		params := url.Values{
			"pr":           {"ucpro"},
			"fr":           {"pc"},
			"pdir_fid":     {pdirFID},
			"_page":        {strconv.Itoa(page)},
			"_size":        {strconv.Itoa(pageSize)},
			"_fetch_total": {"1"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, quarkAPIBase+"/file/sort?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		for k, v := range quarkHeaders(cookie) {
			req.Header.Set(k, v)
		}
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body quarkSortResp
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
		status := res.StatusCode
		statusText := res.Status
		res.Body.Close()
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, errors.New("夸克网盘 Cookie 无效或已过期")
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %s", statusText)
		}
		if err != nil {
			return nil, err
		}
		if body.Code != 0 {
			return nil, fmt.Errorf("夸克网盘返回错误：%s", cmp.Or(body.Message, strconv.Itoa(body.Code)))
		}
		all = append(all, body.Data.List...)
		if len(body.Data.List) < pageSize || (body.Metadata.Total > 0 && len(all) >= body.Metadata.Total) {
			break
		}
	}
	return all, nil
}

func resolveQuarkStream(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	rest := strings.TrimPrefix(cloudURL, "quark://")
	sourceID, fid, ok := strings.Cut(rest, "/")
	if !ok || sourceID == "" || fid == "" {
		return "", nil, errors.New("无效的夸克网盘歌曲地址")
	}
	domesticRegistry.RLock()
	src, found := domesticRegistry.quarkByID[sourceID]
	if !found {
		for _, v := range domesticRegistry.quarkByID {
			src, found = v, true
			break
		}
	}
	domesticRegistry.RUnlock()
	if !found || strings.TrimSpace(src.Password) == "" {
		return "", nil, errors.New("未找到对应的夸克网盘配置")
	}

	payload, _ := json.Marshal(map[string]any{"fids": []string{fid}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, quarkAPIBase+"/file/download?pr=ucpro&fr=pc", bytes.NewReader(payload))
	if err != nil {
		return "", nil, err
	}
	for k, v := range quarkHeaders(src.Password) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	// Merge any Set-Cookie tokens (notably __puus) returned by /file/download,
	// which Quark's CDN requires alongside the user's session cookie.
	mergedCookie := mergeCookieHeader(src.Password, res.Cookies())
	var body quarkDownloadResp
	err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&body)
	status := res.StatusCode
	statusText := res.Status
	res.Body.Close()
	if status != http.StatusOK || err != nil {
		return "", nil, fmt.Errorf("获取夸克网盘播放地址失败（HTTP %s）", statusText)
	}
	if body.Code != 0 || len(body.Data) == 0 || body.Data[0].DownloadURL == "" {
		return "", nil, fmt.Errorf("夸克网盘未返回下载地址：%s", cmp.Or(body.Message, strconv.Itoa(body.Code)))
	}
	return body.Data[0].DownloadURL, quarkHeaders(mergedCookie), nil
}

func mergeCookieHeader(base string, extra []*http.Cookie) string {
	if len(extra) == 0 {
		return strings.TrimSpace(base)
	}
	kv := map[string]string{}
	order := make([]string, 0, 16)
	for _, part := range strings.Split(base, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, exists := kv[k]; !exists {
			order = append(order, k)
		}
		kv[k] = strings.TrimSpace(v)
	}
	for _, c := range extra {
		if c.Name == "" {
			continue
		}
		if _, exists := kv[c.Name]; !exists {
			order = append(order, c.Name)
		}
		kv[c.Name] = c.Value
	}
	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+kv[k])
	}
	return strings.Join(out, "; ")
}

// ---------- 115网盘 (115 Web API) ----------

type p115ListResp struct {
	State bool       `json:"state"`
	Error string     `json:"error"`
	Count int        `json:"count"`
	Data  []p115Item `json:"data"`
}

type p115Item struct {
	FID string      `json:"fid"`
	CID json.Number `json:"cid"`
	N   string      `json:"n"`
	S   int64       `json:"s"`
	PC  string      `json:"pc"`
	SHA string      `json:"sha"`
	TE  string      `json:"te"`
}

func (it p115Item) isDir() bool { return it.FID == "" }

type p115DownloadResp struct {
	State   bool   `json:"state"`
	Msg     string `json:"msg"`
	FileURL string `json:"file_url"`
}

func p115Headers(cookie string) map[string]string {
	return map[string]string{
		"Cookie":     strings.TrimSpace(cookie),
		"User-Agent": p115UA,
		"Referer":    "https://115.com/",
	}
}

// Test115Source verifies that a user-supplied 115 Cookie is valid.
func Test115Source(ctx context.Context, cookie string) error {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" {
		return errors.New("请粘贴 115 网盘 Cookie")
	}
	_, err := list115Dir(ctx, "0", cookie, 1)
	return err
}

func walk115(ctx context.Context, src CloudSource, seenFiles map[string]bool) ([]davFile, []string) {
	cookie := strings.TrimSpace(src.Password)
	if cookie == "" {
		return nil, []string{fmt.Sprintf("115网盘「%s」：Cookie 为空", cmp.Or(src.Name, "115网盘"))}
	}
	rootCID, err := resolve115Folder(ctx, src.Folder, cookie)
	if err != nil {
		return nil, []string{fmt.Sprintf("115网盘「%s」：%v", cmp.Or(src.Name, "115网盘"), err)}
	}

	type dirJob struct {
		cid   string
		depth int
	}
	queue := []dirJob{{cid: rootCID, depth: 0}}
	visitedDirs := 0
	var files []davFile
	var warnings []string
	hdrs := p115Headers(cookie)

	for len(queue) > 0 && visitedDirs < maxCloudDirs && len(files) < maxCloudFiles {
		if ctx.Err() != nil {
			break
		}
		cur := queue[0]
		queue = queue[1:]
		visitedDirs++

		items, err := list115Dir(ctx, cur.cid, cookie, 20)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("115网盘「%s」目录读取失败：%v", cmp.Or(src.Name, "115网盘"), err))
			continue
		}

		lrcByStem := map[string]string{}
		for _, it := range items {
			if it.isDir() || it.PC == "" {
				continue
			}
			lower := strings.ToLower(it.N)
			if strings.HasSuffix(lower, ".lrc") {
				stem := strings.TrimSuffix(lower, ".lrc")
				lrcByStem[stem] = fmt.Sprintf("p115://%s/%s", src.ID, it.PC)
			}
		}

		for _, it := range items {
			if it.isDir() {
				if cur.depth < maxCloudDepth && it.CID.String() != "" {
					queue = append(queue, dirJob{cid: it.CID.String(), depth: cur.depth + 1})
				}
				continue
			}
			if !Supported(it.N) || it.PC == "" {
				continue
			}
			virtualURL := fmt.Sprintf("p115://%s/%s", src.ID, it.PC)
			if seenFiles[virtualURL] {
				continue
			}
			seenFiles[virtualURL] = true
			ext := strings.ToLower(path.Ext(it.N))
			stem := strings.TrimSuffix(strings.ToLower(it.N), ext)
			files = append(files, davFile{
				URL:          virtualURL,
				Name:         it.N,
				Ext:          ext,
				Size:         it.S,
				ETag:         it.SHA,
				LastModified: it.TE,
				LyricsURL:    lrcByStem[stem],
				Headers:      hdrs,
				Source:       src,
			})
			if len(files) >= maxCloudFiles {
				break
			}
		}
	}
	return files, warnings
}

func resolve115Folder(ctx context.Context, folder, cookie string) (string, error) {
	folder = strings.Trim(strings.TrimSpace(folder), "/")
	if folder == "" {
		return "0", nil
	}
	segments := strings.Split(folder, "/")
	curCID := "0"
	for _, seg := range segments {
		items, err := list115Dir(ctx, curCID, cookie, 10)
		if err != nil {
			return "", err
		}
		found := ""
		for _, it := range items {
			if it.isDir() && strings.EqualFold(it.N, seg) {
				found = it.CID.String()
				break
			}
		}
		if found == "" {
			if len(segments) == 1 {
				if _, err := strconv.ParseInt(seg, 10, 64); err == nil {
					// Allow passing a raw numeric 115 CID directly.
					return seg, nil
				}
			}
			return "", fmt.Errorf("找不到目录「%s」", folder)
		}
		curCID = found
	}
	return curCID, nil
}

func list115Dir(ctx context.Context, cid, cookie string, maxPages int) ([]p115Item, error) {
	var all []p115Item
	const pageSize = 1000
	for page := 0; page < maxPages; page++ {
		params := url.Values{
			"aid":              {"1"},
			"cid":              {cid},
			"o":                {"file_name"},
			"asc":              {"1"},
			"offset":           {strconv.Itoa(page * pageSize)},
			"show_dir":         {"1"},
			"limit":            {strconv.Itoa(pageSize)},
			"snap":             {"0"},
			"record_open_time": {"1"},
			"format":           {"json"},
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p115APIBase+"/files?"+params.Encode(), nil)
		if err != nil {
			return nil, err
		}
		for k, v := range p115Headers(cookie) {
			req.Header.Set(k, v)
		}
		res, err := cloudHTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body p115ListResp
		err = json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(&body)
		status := res.StatusCode
		statusText := res.Status
		res.Body.Close()
		if status == http.StatusUnauthorized || status == http.StatusForbidden {
			return nil, errors.New("115 网盘 Cookie 无效或已过期")
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("HTTP %s", statusText)
		}
		if err != nil {
			return nil, err
		}
		if !body.State {
			return nil, fmt.Errorf("115 网盘返回错误：%s", cmp.Or(body.Error, "Cookie 无效或未登录"))
		}
		all = append(all, body.Data...)
		if len(body.Data) < pageSize || (body.Count > 0 && len(all) >= body.Count) {
			break
		}
	}
	return all, nil
}

func resolve115Stream(ctx context.Context, cloudURL string) (string, map[string]string, error) {
	rest := strings.TrimPrefix(cloudURL, "p115://")
	sourceID, pickcode, ok := strings.Cut(rest, "/")
	if !ok || sourceID == "" || pickcode == "" {
		return "", nil, errors.New("无效的 115 网盘歌曲地址")
	}
	domesticRegistry.RLock()
	src, found := domesticRegistry.p115ByID[sourceID]
	if !found {
		for _, v := range domesticRegistry.p115ByID {
			src, found = v, true
			break
		}
	}
	domesticRegistry.RUnlock()
	if !found || strings.TrimSpace(src.Password) == "" {
		return "", nil, errors.New("未找到对应的 115 网盘配置")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p115APIBase+"/files/download?pickcode="+url.QueryEscape(pickcode), nil)
	if err != nil {
		return "", nil, err
	}
	for k, v := range p115Headers(src.Password) {
		req.Header.Set(k, v)
	}
	res, err := cloudHTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	var body p115DownloadResp
	err = json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&body)
	status := res.StatusCode
	statusText := res.Status
	res.Body.Close()
	if status != http.StatusOK || err != nil {
		return "", nil, fmt.Errorf("获取 115 网盘播放地址失败（HTTP %s）", statusText)
	}
	if !body.State || body.FileURL == "" {
		return "", nil, fmt.Errorf("115 网盘未返回下载地址：%s", cmp.Or(body.Msg, "请检查 Cookie"))
	}
	return body.FileURL, p115Headers(src.Password), nil
}
