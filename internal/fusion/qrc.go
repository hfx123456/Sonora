package fusion

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/hex"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var qqKey = []byte("!@#)(*$%123ZXC!@!@#)(NHL")

func fetchDirectLyrics(ctx context.Context, proxy string, cand alternateTitle) (raw, translation string, ok bool) {
	if cand.ID == "" {
		return "", "", false
	}
	client := httpClientForProxy(proxy)
	switch cand.Source {
	case "QQ 音乐 · Lyricify":
		return fetchQQMusicLyricsByID(ctx, client, cand.ID)
	case "网易云 · Lyricify":
		return fetchNetEaseLyricsByID(ctx, client, cand.ID)
	}
	return "", "", false
}

func fetchQQMusicLyricsByID(ctx context.Context, client *http.Client, songID string) (raw, translation string, ok bool) {
	form := url.Values{
		"version":     {"15"},
		"miniversion": {"82"},
		"lrctype":     {"4"},
		"musicid":     {songID},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://c.y.qq.com/qqmusic/fcgi-bin/lyric_download.fcg", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://c.y.qq.com/")

	res, err := client.Do(req)
	if err != nil {
		return "", "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", "", false
	}
	cleaned := strings.ReplaceAll(strings.ReplaceAll(string(body), "<!--", ""), "-->", "")
	origEnc, tsEnc := extractQQXmlFields(cleaned)
	orig := decodeQQField(origEnc)
	ts := decodeQQField(tsEnc)
	if strings.TrimSpace(orig) == "" {
		return "", "", false
	}
	return orig, ts, true
}

func fetchNetEaseLyricsByID(ctx context.Context, client *http.Client, songID string) (raw, translation string, ok bool) {
	endpoint := "https://music.163.com/api/song/lyric?id=" + url.QueryEscape(songID) + "&lv=1&tv=-1&yv=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", false
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://music.163.com/")

	res, err := client.Do(req)
	if err != nil {
		return "", "", false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", "", false
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if err != nil {
		return "", "", false
	}
	var parsed struct {
		Code      int  `json:"code"`
		PureMusic bool `json:"pureMusic"`
		Lrc       struct {
			Lyric string `json:"lyric"`
		} `json:"lrc"`
		Tlyric struct {
			Lyric string `json:"lyric"`
		} `json:"tlyric"`
		Yrc struct {
			Lyric string `json:"lyric"`
		} `json:"yrc"`
		Ytlrc struct {
			Lyric string `json:"lyric"`
		} `json:"ytlrc"`
	}
	if json.Unmarshal(body, &parsed) != nil || parsed.Code != http.StatusOK || parsed.PureMusic {
		return "", "", false
	}
	if yrc := strings.TrimSpace(parsed.Yrc.Lyric); yrc != "" {
		tr := strings.TrimSpace(parsed.Ytlrc.Lyric)
		if tr == "" {
			tr = strings.TrimSpace(parsed.Tlyric.Lyric)
		}
		return yrc, tr, true
	}
	lrc := strings.TrimSpace(parsed.Lrc.Lyric)
	if lrc == "" {
		return "", "", false
	}
	return lrc, strings.TrimSpace(parsed.Tlyric.Lyric), true
}

var (
	qqContentRe   = regexp.MustCompile(`(?is)<content\b[^>]*>(.*?)</content>`)
	qqContentTsRe = regexp.MustCompile(`(?is)<contentts\b[^>]*>(.*?)</contentts>`)
	qqLyricAttrRe = regexp.MustCompile(`(?is)<Lyric_1\b[^>]*?\bLyricContent="([\s\S]*?)"(?:\s*/?>|\s+[A-Za-z_][\w:.-]*\s*=)`)
)

func extractQQXmlFields(xmlText string) (orig, ts string) {
	extract := func(re *regexp.Regexp) string {
		m := re.FindStringSubmatch(xmlText)
		if len(m) < 2 {
			return ""
		}
		v := strings.TrimSpace(m[1])
		if strings.HasPrefix(v, "<![CDATA[") && strings.HasSuffix(v, "]]>") {
			v = strings.TrimSpace(v[len("<![CDATA[") : len(v)-len("]]>")])
		}
		return v
	}
	return extract(qqContentRe), extract(qqContentTsRe)
}

func decodeQQField(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	text, err := decryptQRC(field)
	if err != nil {
		if strings.Contains(field, "[") && strings.Contains(field, "]") {
			return field
		}
		return ""
	}
	if strings.Contains(text, "<?xml") || strings.Contains(text, "<Lyric_1") {
		if content := extractLyricContentAttr(text); content != "" {
			return content
		}
	}
	return text
}

func extractLyricContentAttr(xmlText string) string {
	m := qqLyricAttrRe.FindStringSubmatch(xmlText)
	if len(m) < 2 {
		return ""
	}
	return html.UnescapeString(m[1])
}

func decryptQRC(hexText string) (string, error) {
	encrypted, err := hex.DecodeString(strings.TrimSpace(hexText))
	if err != nil || len(encrypted) == 0 || len(encrypted)%8 != 0 {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return "", err
	}
	var schedule [3][16][6]byte
	qqTripleDESKeySetup(qqKey, &schedule, qqDESDecrypt)

	decrypted := make([]byte, len(encrypted))
	for i := 0; i < len(encrypted); i += 8 {
		qqTripleDESCrypt(encrypted[i:i+8], decrypted[i:i+8], &schedule)
	}
	zr, err := zlib.NewReader(bytes.NewReader(decrypted))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 4<<20))
	if err != nil {
		return "", err
	}
	out = bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	return string(out), nil
}

// QQ Music uses a custom DES variant (from Lyricify.Lyrics.Helper DESHelper.cs, Apache-2.0)
// with non-standard key compression indexing and S-box entries.
const (
	qqDESDecrypt uint32 = 0
	qqDESEncrypt uint32 = 1
)

func qqBitnum(a []byte, b, c int) uint32 {
	return uint32((a[b/32*4+3-(b%32)/8]>>uint(7-(b%8)))&0x01) << uint(c)
}

func qqBitnumIntR(a uint32, b, c int) byte {
	return byte(((a >> uint(31-b)) & 0x00000001) << uint(c))
}

func qqBitnumIntL(a uint32, b, c int) uint32 {
	return ((a << uint(b)) & 0x80000000) >> uint(c)
}

func qqSboxBit(a byte) uint32 {
	return uint32((a & 0x20) | ((a & 0x1f) >> 1) | ((a & 0x01) << 4))
}

var (
	qqSbox1 = [64]byte{14, 4, 13, 1, 2, 15, 11, 8, 3, 10, 6, 12, 5, 9, 0, 7, 0, 15, 7, 4, 14, 2, 13, 1, 10, 6, 12, 11, 9, 5, 3, 8, 4, 1, 14, 8, 13, 6, 2, 11, 15, 12, 9, 7, 3, 10, 5, 0, 15, 12, 8, 2, 4, 9, 1, 7, 5, 11, 3, 14, 10, 0, 6, 13}
	qqSbox2 = [64]byte{15, 1, 8, 14, 6, 11, 3, 4, 9, 7, 2, 13, 12, 0, 5, 10, 3, 13, 4, 7, 15, 2, 8, 15, 12, 0, 1, 10, 6, 9, 11, 5, 0, 14, 7, 11, 10, 4, 13, 1, 5, 8, 12, 6, 9, 3, 2, 15, 13, 8, 10, 1, 3, 15, 4, 2, 11, 6, 7, 12, 0, 5, 14, 9}
	qqSbox3 = [64]byte{10, 0, 9, 14, 6, 3, 15, 5, 1, 13, 12, 7, 11, 4, 2, 8, 13, 7, 0, 9, 3, 4, 6, 10, 2, 8, 5, 14, 12, 11, 15, 1, 13, 6, 4, 9, 8, 15, 3, 0, 11, 1, 2, 12, 5, 10, 14, 7, 1, 10, 13, 0, 6, 9, 8, 7, 4, 15, 14, 3, 11, 5, 2, 12}
	qqSbox4 = [64]byte{7, 13, 14, 3, 0, 6, 9, 10, 1, 2, 8, 5, 11, 12, 4, 15, 13, 8, 11, 5, 6, 15, 0, 3, 4, 7, 2, 12, 1, 10, 14, 9, 10, 6, 9, 0, 12, 11, 7, 13, 15, 1, 3, 14, 5, 2, 8, 4, 3, 15, 0, 6, 10, 10, 13, 8, 9, 4, 5, 11, 12, 7, 2, 14}
	qqSbox5 = [64]byte{2, 12, 4, 1, 7, 10, 11, 6, 8, 5, 3, 15, 13, 0, 14, 9, 14, 11, 2, 12, 4, 7, 13, 1, 5, 0, 15, 10, 3, 9, 8, 6, 4, 2, 1, 11, 10, 13, 7, 8, 15, 9, 12, 5, 6, 3, 0, 14, 11, 8, 12, 7, 1, 14, 2, 13, 6, 15, 0, 9, 10, 4, 5, 3}
	qqSbox6 = [64]byte{12, 1, 10, 15, 9, 2, 6, 8, 0, 13, 3, 4, 14, 7, 5, 11, 10, 15, 4, 2, 7, 12, 9, 5, 6, 1, 13, 14, 0, 11, 3, 8, 9, 14, 15, 5, 2, 8, 12, 3, 7, 0, 4, 10, 1, 13, 11, 6, 4, 3, 2, 12, 9, 5, 15, 10, 11, 14, 1, 7, 6, 0, 8, 13}
	qqSbox7 = [64]byte{4, 11, 2, 14, 15, 0, 8, 13, 3, 12, 9, 7, 5, 10, 6, 1, 13, 0, 11, 7, 4, 9, 1, 10, 14, 3, 5, 12, 2, 15, 8, 6, 1, 4, 11, 13, 12, 3, 7, 14, 10, 15, 6, 8, 0, 5, 9, 2, 6, 11, 13, 8, 1, 4, 10, 7, 9, 5, 0, 15, 14, 2, 3, 12}
	qqSbox8 = [64]byte{13, 2, 8, 4, 6, 15, 11, 1, 10, 9, 3, 14, 5, 0, 12, 7, 1, 15, 13, 8, 10, 3, 7, 4, 12, 5, 6, 11, 0, 14, 9, 2, 7, 11, 4, 1, 9, 12, 14, 2, 0, 6, 10, 13, 15, 3, 5, 8, 2, 1, 14, 7, 4, 10, 8, 13, 15, 12, 9, 0, 3, 5, 6, 11}
)

func qqKeySchedule(key []byte, schedule *[16][6]byte, mode uint32) {
	keyRndShift := [16]uint32{1, 1, 2, 2, 2, 2, 2, 2, 1, 2, 2, 2, 2, 2, 2, 1}
	keyPermC := [28]int{56, 48, 40, 32, 24, 16, 8, 0, 57, 49, 41, 33, 25, 17, 9, 1, 58, 50, 42, 34, 26, 18, 10, 2, 59, 51, 43, 35}
	keyPermD := [28]int{62, 54, 46, 38, 30, 22, 14, 6, 61, 53, 45, 37, 29, 21, 13, 5, 60, 52, 44, 36, 28, 20, 12, 4, 27, 19, 11, 3}
	keyCompression := [48]int{13, 16, 10, 23, 0, 4, 2, 27, 14, 5, 20, 9, 22, 18, 11, 3, 25, 7, 15, 6, 26, 19, 12, 1, 40, 51, 30, 36, 46, 54, 29, 39, 50, 44, 32, 47, 43, 48, 38, 55, 33, 52, 45, 41, 49, 35, 28, 31}

	var c, d uint32
	for i, j := 0, 31; i < 28; i, j = i+1, j-1 {
		c |= qqBitnum(key, keyPermC[i], j)
		d |= qqBitnum(key, keyPermD[i], j)
	}
	for i := 0; i < 16; i++ {
		shift := keyRndShift[i]
		c = ((c << shift) | (c >> (28 - shift))) & 0xfffffff0
		d = ((d << shift) | (d >> (28 - shift))) & 0xfffffff0

		toGen := i
		if mode == qqDESDecrypt {
			toGen = 15 - i
		}
		for j := 0; j < 6; j++ {
			schedule[toGen][j] = 0
		}
		j := 0
		for ; j < 24; j++ {
			schedule[toGen][j/8] |= qqBitnumIntR(c, keyCompression[j], 7-(j%8))
		}
		for ; j < 48; j++ {
			schedule[toGen][j/8] |= qqBitnumIntR(d, keyCompression[j]-27, 7-(j%8))
		}
	}
}

func qqIP(state *[2]uint32, input []byte) {
	state[0] = qqBitnum(input, 57, 31) | qqBitnum(input, 49, 30) | qqBitnum(input, 41, 29) | qqBitnum(input, 33, 28) |
		qqBitnum(input, 25, 27) | qqBitnum(input, 17, 26) | qqBitnum(input, 9, 25) | qqBitnum(input, 1, 24) |
		qqBitnum(input, 59, 23) | qqBitnum(input, 51, 22) | qqBitnum(input, 43, 21) | qqBitnum(input, 35, 20) |
		qqBitnum(input, 27, 19) | qqBitnum(input, 19, 18) | qqBitnum(input, 11, 17) | qqBitnum(input, 3, 16) |
		qqBitnum(input, 61, 15) | qqBitnum(input, 53, 14) | qqBitnum(input, 45, 13) | qqBitnum(input, 37, 12) |
		qqBitnum(input, 29, 11) | qqBitnum(input, 21, 10) | qqBitnum(input, 13, 9) | qqBitnum(input, 5, 8) |
		qqBitnum(input, 63, 7) | qqBitnum(input, 55, 6) | qqBitnum(input, 47, 5) | qqBitnum(input, 39, 4) |
		qqBitnum(input, 31, 3) | qqBitnum(input, 23, 2) | qqBitnum(input, 15, 1) | qqBitnum(input, 7, 0)

	state[1] = qqBitnum(input, 56, 31) | qqBitnum(input, 48, 30) | qqBitnum(input, 40, 29) | qqBitnum(input, 32, 28) |
		qqBitnum(input, 24, 27) | qqBitnum(input, 16, 26) | qqBitnum(input, 8, 25) | qqBitnum(input, 0, 24) |
		qqBitnum(input, 58, 23) | qqBitnum(input, 50, 22) | qqBitnum(input, 42, 21) | qqBitnum(input, 34, 20) |
		qqBitnum(input, 26, 19) | qqBitnum(input, 18, 18) | qqBitnum(input, 10, 17) | qqBitnum(input, 2, 16) |
		qqBitnum(input, 60, 15) | qqBitnum(input, 52, 14) | qqBitnum(input, 44, 13) | qqBitnum(input, 36, 12) |
		qqBitnum(input, 28, 11) | qqBitnum(input, 20, 10) | qqBitnum(input, 12, 9) | qqBitnum(input, 4, 8) |
		qqBitnum(input, 62, 7) | qqBitnum(input, 54, 6) | qqBitnum(input, 46, 5) | qqBitnum(input, 38, 4) |
		qqBitnum(input, 30, 3) | qqBitnum(input, 22, 2) | qqBitnum(input, 14, 1) | qqBitnum(input, 6, 0)
}

func qqInvIP(state *[2]uint32, out []byte) {
	out[3] = qqBitnumIntR(state[1], 7, 7) | qqBitnumIntR(state[0], 7, 6) | qqBitnumIntR(state[1], 15, 5) | qqBitnumIntR(state[0], 15, 4) | qqBitnumIntR(state[1], 23, 3) | qqBitnumIntR(state[0], 23, 2) | qqBitnumIntR(state[1], 31, 1) | qqBitnumIntR(state[0], 31, 0)
	out[2] = qqBitnumIntR(state[1], 6, 7) | qqBitnumIntR(state[0], 6, 6) | qqBitnumIntR(state[1], 14, 5) | qqBitnumIntR(state[0], 14, 4) | qqBitnumIntR(state[1], 22, 3) | qqBitnumIntR(state[0], 22, 2) | qqBitnumIntR(state[1], 30, 1) | qqBitnumIntR(state[0], 30, 0)
	out[1] = qqBitnumIntR(state[1], 5, 7) | qqBitnumIntR(state[0], 5, 6) | qqBitnumIntR(state[1], 13, 5) | qqBitnumIntR(state[0], 13, 4) | qqBitnumIntR(state[1], 21, 3) | qqBitnumIntR(state[0], 21, 2) | qqBitnumIntR(state[1], 29, 1) | qqBitnumIntR(state[0], 29, 0)
	out[0] = qqBitnumIntR(state[1], 4, 7) | qqBitnumIntR(state[0], 4, 6) | qqBitnumIntR(state[1], 12, 5) | qqBitnumIntR(state[0], 12, 4) | qqBitnumIntR(state[1], 20, 3) | qqBitnumIntR(state[0], 20, 2) | qqBitnumIntR(state[1], 28, 1) | qqBitnumIntR(state[0], 28, 0)
	out[7] = qqBitnumIntR(state[1], 3, 7) | qqBitnumIntR(state[0], 3, 6) | qqBitnumIntR(state[1], 11, 5) | qqBitnumIntR(state[0], 11, 4) | qqBitnumIntR(state[1], 19, 3) | qqBitnumIntR(state[0], 19, 2) | qqBitnumIntR(state[1], 27, 1) | qqBitnumIntR(state[0], 27, 0)
	out[6] = qqBitnumIntR(state[1], 2, 7) | qqBitnumIntR(state[0], 2, 6) | qqBitnumIntR(state[1], 10, 5) | qqBitnumIntR(state[0], 10, 4) | qqBitnumIntR(state[1], 18, 3) | qqBitnumIntR(state[0], 18, 2) | qqBitnumIntR(state[1], 26, 1) | qqBitnumIntR(state[0], 26, 0)
	out[5] = qqBitnumIntR(state[1], 1, 7) | qqBitnumIntR(state[0], 1, 6) | qqBitnumIntR(state[1], 9, 5) | qqBitnumIntR(state[0], 9, 4) | qqBitnumIntR(state[1], 17, 3) | qqBitnumIntR(state[0], 17, 2) | qqBitnumIntR(state[1], 25, 1) | qqBitnumIntR(state[0], 25, 0)
	out[4] = qqBitnumIntR(state[1], 0, 7) | qqBitnumIntR(state[0], 0, 6) | qqBitnumIntR(state[1], 8, 5) | qqBitnumIntR(state[0], 8, 4) | qqBitnumIntR(state[1], 16, 3) | qqBitnumIntR(state[0], 16, 2) | qqBitnumIntR(state[1], 24, 1) | qqBitnumIntR(state[0], 24, 0)
}

func qqDESF(state uint32, key [6]byte) uint32 {
	t1 := qqBitnumIntL(state, 31, 0) | ((state & 0xf0000000) >> 1) | qqBitnumIntL(state, 4, 5) |
		qqBitnumIntL(state, 3, 6) | ((state & 0x0f000000) >> 3) | qqBitnumIntL(state, 8, 11) |
		qqBitnumIntL(state, 7, 12) | ((state & 0x00f00000) >> 5) | qqBitnumIntL(state, 12, 17) |
		qqBitnumIntL(state, 11, 18) | ((state & 0x000f0000) >> 7) | qqBitnumIntL(state, 16, 23)

	t2 := qqBitnumIntL(state, 15, 0) | ((state & 0x0000f000) << 15) | qqBitnumIntL(state, 20, 5) |
		qqBitnumIntL(state, 19, 6) | ((state & 0x00000f00) << 13) | qqBitnumIntL(state, 24, 11) |
		qqBitnumIntL(state, 23, 12) | ((state & 0x000000f0) << 11) | qqBitnumIntL(state, 28, 17) |
		qqBitnumIntL(state, 27, 18) | ((state & 0x0000000f) << 9) | qqBitnumIntL(state, 0, 23)

	lrg := [6]byte{
		byte((t1 >> 24) & 0xff) ^ key[0],
		byte((t1 >> 16) & 0xff) ^ key[1],
		byte((t1 >> 8) & 0xff) ^ key[2],
		byte((t2 >> 24) & 0xff) ^ key[3],
		byte((t2 >> 16) & 0xff) ^ key[4],
		byte((t2 >> 8) & 0xff) ^ key[5],
	}

	s := (uint32(qqSbox1[qqSboxBit(lrg[0]>>2)]) << 28) |
		(uint32(qqSbox2[qqSboxBit(((lrg[0]&0x03)<<4)|(lrg[1]>>4))]) << 24) |
		(uint32(qqSbox3[qqSboxBit(((lrg[1]&0x0f)<<2)|(lrg[2]>>6))]) << 20) |
		(uint32(qqSbox4[qqSboxBit(lrg[2]&0x3f)]) << 16) |
		(uint32(qqSbox5[qqSboxBit(lrg[3]>>2)]) << 12) |
		(uint32(qqSbox6[qqSboxBit(((lrg[3]&0x03)<<4)|(lrg[4]>>4))]) << 8) |
		(uint32(qqSbox7[qqSboxBit(((lrg[4]&0x0f)<<2)|(lrg[5]>>6))]) << 4) |
		uint32(qqSbox8[qqSboxBit(lrg[5]&0x3f)])

	return qqBitnumIntL(s, 15, 0) | qqBitnumIntL(s, 6, 1) | qqBitnumIntL(s, 19, 2) |
		qqBitnumIntL(s, 20, 3) | qqBitnumIntL(s, 28, 4) | qqBitnumIntL(s, 11, 5) |
		qqBitnumIntL(s, 27, 6) | qqBitnumIntL(s, 16, 7) | qqBitnumIntL(s, 0, 8) |
		qqBitnumIntL(s, 14, 9) | qqBitnumIntL(s, 22, 10) | qqBitnumIntL(s, 25, 11) |
		qqBitnumIntL(s, 4, 12) | qqBitnumIntL(s, 17, 13) | qqBitnumIntL(s, 30, 14) |
		qqBitnumIntL(s, 9, 15) | qqBitnumIntL(s, 1, 16) | qqBitnumIntL(s, 7, 17) |
		qqBitnumIntL(s, 23, 18) | qqBitnumIntL(s, 13, 19) | qqBitnumIntL(s, 31, 20) |
		qqBitnumIntL(s, 26, 21) | qqBitnumIntL(s, 2, 22) | qqBitnumIntL(s, 8, 23) |
		qqBitnumIntL(s, 18, 24) | qqBitnumIntL(s, 12, 25) | qqBitnumIntL(s, 29, 26) |
		qqBitnumIntL(s, 5, 27) | qqBitnumIntL(s, 21, 28) | qqBitnumIntL(s, 10, 29) |
		qqBitnumIntL(s, 3, 30) | qqBitnumIntL(s, 24, 31)
}

func qqDESCrypt(in, out []byte, key *[16][6]byte) {
	var state [2]uint32
	qqIP(&state, in)
	for idx := 0; idx < 15; idx++ {
		t := state[1]
		state[1] = qqDESF(state[1], key[idx]) ^ state[0]
		state[0] = t
	}
	state[0] = qqDESF(state[1], key[15]) ^ state[0]
	qqInvIP(&state, out)
}

func qqTripleDESKeySetup(key []byte, schedule *[3][16][6]byte, mode uint32) {
	if mode == qqDESEncrypt {
		qqKeySchedule(key[0:], &schedule[0], mode)
		qqKeySchedule(key[8:], &schedule[1], qqDESDecrypt)
		qqKeySchedule(key[16:], &schedule[2], mode)
		return
	}
	qqKeySchedule(key[0:], &schedule[2], mode)
	qqKeySchedule(key[8:], &schedule[1], qqDESEncrypt)
	qqKeySchedule(key[16:], &schedule[0], mode)
}

func qqTripleDESCrypt(in, out []byte, schedule *[3][16][6]byte) {
	qqDESCrypt(in, out, &schedule[0])
	qqDESCrypt(out, out, &schedule[1])
	qqDESCrypt(out, out, &schedule[2])
}
