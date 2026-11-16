package lyrics

import (
	"slices"
	"strings"
	"unicode"
)

// CleanTitle removes trailing " - ..." suffixes and bracketed metadata such as
// "(feat. ...)" or "(一天一天)".
func CleanTitle(s string) string {
	if i := strings.Index(s, " - "); i > 0 {
		s = s[:i]
	}
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case strings.ContainsRune("([（【", r):
			depth++
		case strings.ContainsRune(")]）】", r):
			depth = max(depth-1, 0)
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// FoldRaw normalizes text for fuzzy title/artist/album comparison while
// preserving bracketed text.
func FoldRaw(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, SimplifyText(s))
}

// FoldTitle normalizes CleanTitle(s) into lowercased letters and numbers.
func FoldTitle(s string) string {
	return FoldRaw(CleanTitle(s))
}

// ArtistsMatch reports whether any artist in want matches any artist in got,
// ignoring casing, punctuation, traditional/simplified variants, and trailing
// parenthetical aliases like "BIGBANG (빅뱅)".
func ArtistsMatch(want, got []string) bool {
	if len(want) == 0 || len(got) == 0 {
		return false
	}
	variants := func(name string) []string {
		var out []string
		if raw := FoldRaw(name); raw != "" {
			out = append(out, raw)
		}
		if clean := FoldTitle(name); clean != "" && !slices.Contains(out, clean) {
			out = append(out, clean)
		}
		return out
	}
	for _, w := range want {
		wv := variants(w)
		for _, g := range got {
			for _, gv := range variants(g) {
				if slices.Contains(wv, gv) {
					return true
				}
			}
		}
	}
	return false
}

// AlbumMatches reports whether two album titles refer to the same release,
// tolerating edition qualifiers, traditional/simplified differences, and
// series prefixes like "3rd Mini Album : Stand Up".
func AlbumMatches(want, got string) bool {
	variants := func(s string) []string {
		var out []string
		add := func(v string) {
			if f := FoldRaw(v); f != "" && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
		add(s)
		add(CleanTitle(s))
		for _, sep := range []string{":", "："} {
			if _, after, ok := strings.Cut(s, sep); ok {
				add(after)
				add(CleanTitle(after))
			}
		}
		return out
	}
	wv, gv := variants(want), variants(got)
	if len(wv) == 0 || len(gv) == 0 {
		return false
	}
	for _, w := range wv {
		if slices.Contains(gv, w) {
			return true
		}
	}
	return false
}

// CrossScript reports whether a and b use incompatible writing systems (for
// example Romanized Latin "Haru Haru" vs Hangul "하루하루"), making direct
// string comparison impossible.
func CrossScript(a, b string) bool {
	type scripts struct {
		latin, han, kana, hangul bool
	}
	scan := func(s string) (res scripts) {
		for _, r := range s {
			switch {
			case unicode.In(r, unicode.Hangul):
				res.hangul = true
			case unicode.In(r, unicode.Hiragana, unicode.Katakana):
				res.kana = true
			case unicode.Is(unicode.Han, r):
				res.han = true
			case unicode.Is(unicode.Latin, r):
				res.latin = true
			}
		}
		return res
	}
	sa, sb := scan(a), scan(b)
	cjkA := sa.han || sa.kana || sa.hangul
	cjkB := sb.han || sb.kana || sb.hangul
	if cjkA != cjkB {
		return (sa.latin && !cjkA && cjkB) || (sb.latin && !cjkB && cjkA)
	}
	if cjkA && cjkB {
		return (sa.hangul != sb.hangul && !sa.han && !sb.han) || (sa.kana != sb.kana && !sa.han && !sb.han)
	}
	return false
}

var asciiVersionTags = map[string]bool{
	"live": true, "remix": true, "acoustic": true, "unplugged": true,
	"instrumental": true, "inst": true, "karaoke": true, "cover": true,
	"demo": true, "dj": true, "mashup": true, "medley": true,
	"japanese": true, "english": true, "chinese": true, "korean": true,
	"mandarin": true, "cantonese": true, "sped": true, "slowed": true,
}

var versionAllowedBeforeVer = map[string]bool{
	"album": true, "original": true, "explicit": true, "single": true, "clean": true, "studio": true,
}

var cjkVersionTags = []string{
	"伴奏", "纯音乐", "翻唱", "现场", "演唱会", "日语", "日文",
	"英文", "英语", "中文", "国语", "粤语", "韩语", "韩文", "吉他版", "钢琴版",
}

// HasExtraVersionTag reports whether candidate introduces a version, language,
// live, remix, cover, or instrumental qualifier absent from wantTitle and
// wantAlbum.
func HasExtraVersionTag(wantTitle, wantAlbum, candidate string) bool {
	wantSimple := SimplifyText(strings.ToLower(wantTitle + " " + wantAlbum))
	candSimple := SimplifyText(strings.ToLower(candidate))

	wantTokens := asciiTokens(wantSimple)
	candTokens := asciiTokens(candSimple)
	wantSet := make(map[string]bool, len(wantTokens))
	for _, tok := range wantTokens {
		wantSet[tok] = true
	}
	for i, tok := range candTokens {
		if asciiVersionTags[tok] && !wantSet[tok] {
			return true
		}
		if (tok == "ver" || tok == "version") && !wantSet["ver"] && !wantSet["version"] {
			if i == 0 || !versionAllowedBeforeVer[candTokens[i-1]] {
				return true
			}
		}
	}
	for _, tag := range cjkVersionTags {
		if strings.Contains(candSimple, tag) && !strings.Contains(wantSimple, tag) {
			return true
		}
	}
	return false
}

func asciiTokens(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			cur.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

// BilingualParenMatch matches a candidate title that pairs a base title with a
// cross-script translation or alias in brackets (for example "Haru Haru (一天一天)"
// for "Haru Haru", or "春春春 (Haru Haru Haru)" for "Haru Haru Haru"), returning
// the matched flag and the candidate's outer base title.
func BilingualParenMatch(want, candidate string) (matched bool, baseTitle string) {
	wantFold := FoldTitle(want)
	if wantFold == "" || HasExtraVersionTag(want, "", candidate) {
		return false, ""
	}
	outer, parens := splitTitleParens(candidate)
	outerTrim := strings.TrimSpace(outer)
	outerFold := FoldRaw(outerTrim)
	if outerFold == "" || len(parens) == 0 {
		return false, ""
	}
	if outerFold == wantFold {
		for _, p := range parens {
			if CrossScript(outerTrim, p) {
				return true, outerTrim
			}
		}
	}
	for _, p := range parens {
		if FoldRaw(p) == wantFold && CrossScript(outerTrim, p) {
			return true, outerTrim
		}
	}
	return false, ""
}

func splitTitleParens(s string) (outer string, parens []string) {
	var out, cur strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case strings.ContainsRune("([（【", r):
			if depth == 0 {
				cur.Reset()
			} else {
				cur.WriteRune(r)
			}
			depth++
		case strings.ContainsRune(")]）】", r):
			if depth > 0 {
				depth--
				if depth == 0 {
					if t := strings.TrimSpace(cur.String()); t != "" {
						parens = append(parens, t)
					}
					cur.Reset()
				} else {
					cur.WriteRune(r)
				}
			}
		case depth == 0:
			out.WriteRune(r)
		default:
			cur.WriteRune(r)
		}
	}
	return out.String(), parens
}
