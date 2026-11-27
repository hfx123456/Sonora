package fusion

import (
	"encoding/xml"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"vinyl/internal/lyrics"
)

type parsedLine struct {
	at          int64
	end         int64
	hasStart    bool
	hasEnd      bool
	text        string
	translation string
	words       []parsedWord
}

type parsedWord struct {
	text  string
	start int64
	end   int64
}

var (
	reLrcTag       = regexp.MustCompile(`\[(\d+):(\d+)(?:[.:](\d+))?\]`)
	reEnhancedWord = regexp.MustCompile(`<(\d+):(\d+)(?:[.:](\d+))?>`)
	reYrcLine      = regexp.MustCompile(`^\[(\d+),(\d+)\]`)
	reYrcWord      = regexp.MustCompile(`\((\d+),(\d+),\d+\)`)
	reQrcWord      = regexp.MustCompile(`\((\d+),(\d+)\)`)
	reLysLine      = regexp.MustCompile(`^\[(\d+)\]`)
	reLylLine      = regexp.MustCompile(`^\[(\d+),(\d+)\](.*)$`)
)

// convertLyrics parses raw lyrics (auto-detecting TTML, YRC, QRC, LYS, LYL,
// LRC, or plain text), aligns optional separate translation lyrics, pairs
// equal-timestamp bilingual lines, and returns a normalized Result.
func convertLyrics(raw, translation, source string) Result {
	raw = strings.TrimPrefix(raw, "\uFEFF")
	translation = strings.TrimPrefix(translation, "\uFEFF")
	if strings.TrimSpace(raw) == "" {
		return Result{Source: source}
	}

	parsed := parseAuto(raw)
	if len(parsed) == 0 {
		trimmed := strings.TrimSpace(raw)
		if !strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "{") {
			var lines []lyrics.Line
			for _, ln := range strings.Split(raw, "\n") {
				if t := strings.TrimSpace(ln); t != "" {
					lines = append(lines, lyrics.Line{Text: t, Unsynced: true})
				}
			}
			return Result{Source: source, Lines: lines}
		}
		return Result{Source: source}
	}

	var transLines []parsedLine
	if strings.TrimSpace(translation) != "" {
		transLines = parseAuto(translation)
	}

	// Keep non-empty lines and sort stably by start time.
	var valid []parsedLine
	for _, ln := range parsed {
		if strings.TrimSpace(ln.text) == "" {
			continue
		}
		valid = append(valid, ln)
	}
	sort.SliceStable(valid, func(i, j int) bool {
		return valid[i].at < valid[j].at
	})

	result := Result{Source: source, Lines: make([]lyrics.Line, 0, len(valid))}
	for i, ln := range valid {
		start := ln.at
		nextStart := start + 6000
		if i+1 < len(valid) && valid[i+1].hasStart {
			nextStart = valid[i+1].at
		}
		end := ln.end
		if !ln.hasEnd {
			end = min(start+6000, nextStart)
		}
		if end <= start {
			end = start + 1
		}

		chinese := strings.TrimSpace(ln.translation)
		if chinese == "" && ln.hasStart && len(transLines) > 0 {
			bestIdx := -1
			bestDiff := int64(501)
			for ti, tl := range transLines {
				if !tl.hasStart {
					continue
				}
				tt := strings.TrimSpace(tl.text)
				if tt == "" || tt == "//" {
					continue
				}
				d := absInt64(tl.at - start)
				if d < bestDiff {
					bestDiff = d
					bestIdx = ti
				}
			}
			if bestIdx >= 0 {
				nearestAt := transLines[bestIdx].at
				closerExists := false
				for oi, other := range valid {
					if oi == i || !other.hasStart {
						continue
					}
					if absInt64(other.at-nearestAt) < bestDiff {
						closerExists = true
						break
					}
				}
				if !closerExists {
					chinese = strings.TrimSpace(transLines[bestIdx].text)
				}
			}
		}

		var words []lyrics.Word
		if len(ln.words) > 0 {
			words = make([]lyrics.Word, 0, len(ln.words))
			for _, w := range ln.words {
				words = append(words, lyrics.Word{
					Text:  w.text,
					Start: time.Duration(w.start) * time.Millisecond,
					End:   time.Duration(w.end) * time.Millisecond,
				})
			}
		}

		result.Lines = append(result.Lines, lyrics.Line{
			At:          time.Duration(start) * time.Millisecond,
			End:         time.Duration(end) * time.Millisecond,
			Text:        ln.text,
			Translation: chinese,
			Words:       words,
			Timed:       len(words) > 0,
			Unsynced:    !ln.hasStart,
		})
	}

	// Merge equal-timestamp bilingual pairs (e.g. LRC with original + Chinese at same timestamp).
	countByAt := map[time.Duration]int{}
	for _, ln := range result.Lines {
		if !ln.Unsynced {
			countByAt[ln.At]++
		}
	}
	var merged []lyrics.Line
	for i := 0; i < len(result.Lines); i++ {
		cur := result.Lines[i]
		if !cur.Unsynced && countByAt[cur.At] == 2 && i+1 < len(result.Lines) && !result.Lines[i+1].Unsynced && result.Lines[i+1].At == cur.At {
			nxt := result.Lines[i+1]
			if !isChineseText(cur.Text) && isChineseText(nxt.Text) {
				if cur.Translation == "" {
					cur.Translation = nxt.Text
				}
				merged = append(merged, cur)
				i++
				continue
			}
			if isChineseText(cur.Text) && !isChineseText(nxt.Text) {
				if nxt.Translation == "" {
					nxt.Translation = cur.Text
				}
				merged = append(merged, nxt)
				i++
				continue
			}
		}
		merged = append(merged, cur)
	}
	result.Lines = merged

	// Normalize End times for line-synced lyrics after bilingual pairing.
	for i := range result.Lines {
		if result.Lines[i].Unsynced || len(result.Lines[i].Words) > 0 {
			continue
		}
		if i+1 < len(result.Lines) && result.Lines[i+1].At > result.Lines[i].At {
			end := result.Lines[i].End
			if end <= result.Lines[i].At+time.Millisecond {
				end = result.Lines[i].At + 6*time.Second
			}
			if result.Lines[i+1].At < end {
				end = result.Lines[i+1].At
			}
			result.Lines[i].End = end
		}
	}

	return result
}

func parseAuto(raw string) []parsedLine {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "<") && (strings.Contains(trimmed, "<tt") || strings.Contains(trimmed, "<p ")) {
		if lines := parseTTML(trimmed); len(lines) > 0 {
			return lines
		}
	}
	// Detect YRC vs QRC vs LRC vs LYS vs LYL by scanning lines.
	hasYrc, hasQrc, hasLrc, hasLys := false, false, false, false
	for _, line := range strings.Split(raw, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "{") {
			continue
		}
		if reYrcLine.MatchString(s) {
			rest := s[len(reYrcLine.FindString(s)):]
			if reYrcWord.MatchString(rest) {
				hasYrc = true
				break
			}
			if reQrcWord.MatchString(rest) {
				hasQrc = true
				break
			}
		}
		if reLrcTag.MatchString(s) {
			hasLrc = true
		} else if reLysLine.MatchString(s) && reQrcWord.MatchString(s) {
			hasLys = true
		}
	}
	switch {
	case hasYrc:
		if lines := parseYRC(raw); len(lines) > 0 {
			return lines
		}
	case hasQrc:
		if lines := parseQRC(raw); len(lines) > 0 {
			return lines
		}
	case hasLrc:
		if lines := parseLRC(raw); len(lines) > 0 {
			return lines
		}
	case hasLys:
		if lines := parseLYS(raw); len(lines) > 0 {
			return lines
		}
	}
	// Fallback: try LYL ([startMs,endMs]text) or QRC/YRC/LRC.
	if lines := parseLYL(raw); len(lines) > 0 {
		return lines
	}
	if lines := parseLRC(raw); len(lines) > 0 {
		return lines
	}
	return nil
}

func parseYRC(raw string) []parsedLine {
	var out []parsedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		m := reYrcLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, _ := strconv.ParseInt(m[1], 10, 64)
		dur, _ := strconv.ParseInt(m[2], 10, 64)
		rest := line[len(m[0]):]
		matches := reYrcWord.FindAllStringSubmatchIndex(rest, -1)
		if len(matches) == 0 {
			continue
		}
		var words []parsedWord
		var sb strings.Builder
		for i, w := range matches {
			ws, _ := strconv.ParseInt(rest[w[2]:w[3]], 10, 64)
			wd, _ := strconv.ParseInt(rest[w[4]:w[5]], 10, 64)
			textEnd := len(rest)
			if i+1 < len(matches) {
				textEnd = matches[i+1][0]
			}
			wt := rest[w[1]:textEnd]
			if wt == "" {
				continue
			}
			sb.WriteString(wt)
			words = append(words, parsedWord{text: wt, start: ws, end: ws + wd})
		}
		text := strings.TrimSpace(sb.String())
		if text == "" {
			continue
		}
		out = append(out, parsedLine{
			at:       start,
			end:      start + dur,
			hasStart: true,
			hasEnd:   dur > 0,
			text:     text,
			words:    words,
		})
	}
	return out
}

func parseQRC(raw string) []parsedLine {
	var out []parsedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		m := reYrcLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, _ := strconv.ParseInt(m[1], 10, 64)
		dur, _ := strconv.ParseInt(m[2], 10, 64)
		rest := line[len(m[0]):]
		matches := reQrcWord.FindAllStringSubmatchIndex(rest, -1)
		if len(matches) == 0 {
			if text := strings.TrimSpace(rest); text != "" {
				out = append(out, parsedLine{at: start, end: start + dur, hasStart: true, hasEnd: dur > 0, text: text})
			}
			continue
		}
		var words []parsedWord
		var sb strings.Builder
		pos := 0
		for _, w := range matches {
			wt := rest[pos:w[0]]
			pos = w[1]
			ws, _ := strconv.ParseInt(rest[w[2]:w[3]], 10, 64)
			wd, _ := strconv.ParseInt(rest[w[4]:w[5]], 10, 64)
			if wt == "" {
				continue
			}
			sb.WriteString(wt)
			words = append(words, parsedWord{text: wt, start: ws, end: ws + wd})
		}
		text := strings.TrimSpace(sb.String())
		if text == "" {
			continue
		}
		out = append(out, parsedLine{
			at:       start,
			end:      start + dur,
			hasStart: true,
			hasEnd:   dur > 0,
			text:     text,
			words:    words,
		})
	}
	return out
}

func parseLYS(raw string) []parsedLine {
	var out []parsedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		m := reLysLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rest := line[len(m[0]):]
		matches := reQrcWord.FindAllStringSubmatchIndex(rest, -1)
		if len(matches) == 0 {
			continue
		}
		var words []parsedWord
		var sb strings.Builder
		pos := 0
		for _, w := range matches {
			wt := rest[pos:w[0]]
			pos = w[1]
			ws, _ := strconv.ParseInt(rest[w[2]:w[3]], 10, 64)
			wd, _ := strconv.ParseInt(rest[w[4]:w[5]], 10, 64)
			if wt == "" {
				continue
			}
			sb.WriteString(wt)
			words = append(words, parsedWord{text: wt, start: ws, end: ws + wd})
		}
		text := strings.TrimSpace(sb.String())
		if text == "" || len(words) == 0 {
			continue
		}
		out = append(out, parsedLine{
			at:       words[0].start,
			end:      words[len(words)-1].end,
			hasStart: true,
			hasEnd:   true,
			text:     text,
			words:    words,
		})
	}
	return out
}

func parseLYL(raw string) []parsedLine {
	var out []parsedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		m := reLylLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		start, _ := strconv.ParseInt(m[1], 10, 64)
		end, _ := strconv.ParseInt(m[2], 10, 64)
		text := strings.TrimSpace(m[3])
		if text == "" {
			continue
		}
		out = append(out, parsedLine{
			at:       start,
			end:      end,
			hasStart: true,
			hasEnd:   end > start,
			text:     text,
		})
	}
	return out
}

func parseLRC(raw string) []parsedLine {
	var out []parsedLine
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		var times []int64
		pos := 0
		for pos < len(line) {
			for pos < len(line) && (line[pos] == ' ' || line[pos] == '\t') {
				pos++
			}
			loc := reLrcTag.FindStringSubmatchIndex(line[pos:])
			if loc == nil || loc[0] != 0 {
				break
			}
			ms := parseMinSecFrac(line[pos+loc[2]:pos+loc[3]], line[pos+loc[4]:pos+loc[5]], sliceSub(line, pos, loc[6], loc[7]))
			times = append(times, ms)
			pos += loc[1]
		}
		if len(times) == 0 {
			continue
		}
		rest := strings.TrimSpace(line[pos:])
		if rest == "" {
			continue
		}
		// Check enhanced LRC (<mm:ss.xx>word).
		eMatches := reEnhancedWord.FindAllStringSubmatchIndex(rest, -1)
		if len(eMatches) > 0 && len(times) == 1 {
			var words []parsedWord
			var sb strings.Builder
			for i, wm := range eMatches {
				ws := parseMinSecFrac(rest[wm[2]:wm[3]], rest[wm[4]:wm[5]], sliceSub(rest, 0, wm[6], wm[7]))
				textEnd := len(rest)
				if i+1 < len(eMatches) {
					textEnd = eMatches[i+1][0]
				}
				wt := rest[wm[1]:textEnd]
				if wt == "" {
					continue
				}
				sb.WriteString(wt)
				words = append(words, parsedWord{text: wt, start: ws})
			}
			for i := range words {
				if i+1 < len(words) {
					words[i].end = words[i+1].start
				} else {
					words[i].end = words[i].start + 500
				}
			}
			text := strings.TrimSpace(sb.String())
			if text != "" {
				out = append(out, parsedLine{at: times[0], hasStart: true, text: text, words: words})
			}
			continue
		}
		for _, ts := range times {
			out = append(out, parsedLine{at: ts, hasStart: true, text: rest})
		}
	}
	return out
}

func parseTTML(raw string) []parsedLine {
	dec := xml.NewDecoder(strings.NewReader(raw))
	dec.Strict = false

	translations := map[string]string{}
	var out []parsedLine

	var inZhTrans bool
	var curTransKey string
	var curTransText strings.Builder

	var inP bool
	var curP parsedLine
	var curPKey string
	var curPText strings.Builder
	var curInlineTrans strings.Builder

	type ttmlSpanFrame struct {
		start, end   int64
		hasTime      bool
		hadChildSpan bool
		text         strings.Builder
	}
	var spanStack []*ttmlSpanFrame
	var inlineTransDepth int
	var inlineTransKeep bool
	var skipSpanDepth int

	getAttr := func(attrs []xml.Attr, names ...string) string {
		for _, a := range attrs {
			for _, n := range names {
				if strings.EqualFold(a.Name.Local, n) {
					return a.Value
				}
			}
		}
		return ""
	}

	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			local := strings.ToLower(t.Name.Local)
			switch local {
			case "translation":
				lang := strings.ToLower(getAttr(t.Attr, "lang"))
				inZhTrans = strings.HasPrefix(lang, "zh")
			case "text":
				if inZhTrans {
					curTransKey = getAttr(t.Attr, "for")
					curTransText.Reset()
				}
			case "p":
				inP = true
				curP = parsedLine{}
				curPKey = getAttr(t.Attr, "key")
				curPText.Reset()
				curInlineTrans.Reset()
				spanStack = spanStack[:0]
				inlineTransDepth = 0
				inlineTransKeep = false
				skipSpanDepth = 0
				if b := getAttr(t.Attr, "begin"); b != "" {
					if ms, ok := parseTTMLTime(b); ok {
						curP.at, curP.hasStart = ms, true
					}
				}
				if e := getAttr(t.Attr, "end"); e != "" {
					if ms, ok := parseTTMLTime(e); ok {
						curP.end, curP.hasEnd = ms, true
					}
				}
			case "span":
				if inP {
					if inlineTransDepth > 0 {
						inlineTransDepth++
						continue
					}
					if skipSpanDepth > 0 {
						skipSpanDepth++
						continue
					}
					role := strings.ToLower(getAttr(t.Attr, "role"))
					if role == "x-translation" {
						// Inline translation span: capture into translation instead of original text.
						lang := strings.ToLower(getAttr(t.Attr, "lang"))
						inlineTransDepth = 1
						inlineTransKeep = lang == "" || strings.HasPrefix(lang, "zh")
						continue
					}
					if role == "x-roman" {
						skipSpanDepth = 1
						continue
					}
					if n := len(spanStack); n > 0 {
						parent := spanStack[n-1]
						parent.hadChildSpan = true
						if parent.text.Len() > 0 {
							curPText.WriteString(parent.text.String())
							parent.text.Reset()
						}
					}
					frame := &ttmlSpanFrame{}
					if b := getAttr(t.Attr, "begin"); b != "" {
						if ms, ok := parseTTMLTime(b); ok {
							frame.start = ms
							frame.hasTime = true
						}
					}
					if e := getAttr(t.Attr, "end"); e != "" {
						if ms, ok := parseTTMLTime(e); ok {
							frame.end = ms
						}
					}
					spanStack = append(spanStack, frame)
				}
			}
		case xml.CharData:
			s := string(t)
			if inZhTrans && curTransKey != "" {
				curTransText.WriteString(s)
			} else if inlineTransDepth > 0 {
				if inlineTransKeep {
					curInlineTrans.WriteString(s)
				}
			} else if skipSpanDepth > 0 {
				// Ignore romanization spans so they do not pollute original lyrics.
			} else if n := len(spanStack); n > 0 {
				spanStack[n-1].text.WriteString(s)
			} else if inP {
				curPText.WriteString(s)
			}
		case xml.EndElement:
			local := strings.ToLower(t.Name.Local)
			switch local {
			case "translation":
				inZhTrans = false
				curTransKey = ""
			case "text":
				if inZhTrans && curTransKey != "" {
					if val := strings.TrimSpace(curTransText.String()); val != "" {
						translations[curTransKey] = val
					}
					curTransKey = ""
				}
			case "span":
				if inlineTransDepth > 0 {
					inlineTransDepth--
					if inlineTransDepth == 0 {
						inlineTransKeep = false
					}
				} else if skipSpanDepth > 0 {
					skipSpanDepth--
				} else if n := len(spanStack); n > 0 {
					frame := spanStack[n-1]
					spanStack = spanStack[:n-1]
					wt := frame.text.String()
					curPText.WriteString(wt)
					if !frame.hadChildSpan && frame.hasTime && wt != "" {
						end := frame.end
						if end <= frame.start {
							end = frame.start + 1
						}
						curP.words = append(curP.words, parsedWord{text: wt, start: frame.start, end: end})
					}
				}
			case "p":
				if inP {
					for _, frame := range spanStack {
						curPText.WriteString(frame.text.String())
					}
					spanStack = spanStack[:0]
					curP.text = strings.TrimSpace(curPText.String())
					if curPKey != "" {
						if tr, ok := translations[curPKey]; ok {
							curP.translation = tr
						}
					}
					if curP.translation == "" && curInlineTrans.Len() > 0 {
						curP.translation = strings.TrimSpace(curInlineTrans.String())
					}
					if curP.text != "" {
						out = append(out, curP)
					}
					inP = false
					curPKey = ""
					inlineTransDepth = 0
					inlineTransKeep = false
					skipSpanDepth = 0
				}
			}
		}
	}
	return out
}

func parseTTMLTime(s string) (ms int64, ok bool) {
	defer func() {
		if ms < 0 || ms >= math.MaxInt64/int64(time.Millisecond) {
			ms, ok = 0, false
		}
	}()
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "-") {
		return 0, false
	}
	if strings.HasSuffix(s, "ms") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "ms"), 64)
		return int64(math.Round(f)), err == nil
	}
	if strings.HasSuffix(s, "s") {
		f, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		return int64(math.Round(f * 1000)), err == nil
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		f, err := strconv.ParseFloat(parts[0], 64)
		return int64(math.Round(f * 1000)), err == nil
	case 2:
		m, err1 := strconv.ParseInt(parts[0], 10, 64)
		sec, err2 := strconv.ParseFloat(parts[1], 64)
		if err1 != nil || err2 != nil {
			return 0, false
		}
		return m*60000 + int64(math.Round(sec*1000)), true
	case 3:
		h, err1 := strconv.ParseInt(parts[0], 10, 64)
		m, err2 := strconv.ParseInt(parts[1], 10, 64)
		sec, err3 := strconv.ParseFloat(parts[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			return 0, false
		}
		return h*3600000 + m*60000 + int64(math.Round(sec*1000)), true
	}
	return 0, false
}

func parseMinSecFrac(minStr, secStr, fracStr string) int64 {
	m, _ := strconv.ParseInt(minStr, 10, 64)
	s, _ := strconv.ParseInt(secStr, 10, 64)
	var ms int64
	if fracStr != "" {
		if len(fracStr) > 3 {
			fracStr = fracStr[:3]
		}
		for len(fracStr) < 3 {
			fracStr += "0"
		}
		ms, _ = strconv.ParseInt(fracStr, 10, 64)
	}
	return m*60000 + s*1000 + ms
}

func sliceSub(s string, base, start, end int) string {
	if start < 0 || end < 0 {
		return ""
	}
	return s[base+start : base+end]
}

func isChineseText(text string) bool {
	hasHan := false
	for _, r := range text {
		if r >= 0x3040 && r <= 0x30ff {
			return false
		}
		if r >= 0x4e00 && r <= 0x9fff {
			hasHan = true
		}
	}
	return hasHan
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
