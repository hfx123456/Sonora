package app

import (
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
)

const lyricEffectFrame = time.Second / 30
const lyricLiftTail = 220 * time.Millisecond

// lyricWordRanges matches in the original text, retaining punctuation and
// UTF-16 boundaries even when a source omits spaces or repeats a word.
func lyricWordRanges(text string, words []lyrics.Word) []ui.TextInputRange {
	ranges := make([]ui.TextInputRange, len(words))
	cursor := 0
	for i, word := range words {
		if word.Text == "" {
			continue
		}
		if offset := strings.Index(text[cursor:], word.Text); offset >= 0 {
			start := cursor + offset
			cursor = start + len(word.Text)
			ranges[i] = ui.TextInputRange{
				Start: len(utf16.Encode([]rune(text[:start]))),
				End:   len(utf16.Encode([]rune(text[:cursor]))),
			}
		}
	}
	return ranges
}

// lyricEffectDelay sleeps through gaps instead of repainting a settled line.
func lyricEffectDelay(words []lyrics.Word, pos time.Duration, playing, still bool) time.Duration {
	if !playing {
		return 0
	}
	next := time.Duration(0)
	for _, word := range words {
		if word.End <= word.Start {
			continue
		}
		if !still && pos >= word.Start && pos < word.End+lyricLiftTail {
			return lyricEffectFrame
		}
		at := word.Start
		if still {
			at = word.End
		}
		if at > pos && (next == 0 || at-pos < next) {
			next = at - pos
		}
	}
	return next
}

type lyricInkRect struct {
	ui.Rect
	rtl bool
}

type lyricInkRun struct {
	word  int // -1 is punctuation or spacing without a timestamp
	rects []lyricInkRect
}

type lyricInkLayout struct {
	text      string
	font      ui.Font
	width     float32
	direction ui.Direction
	wordTexts []string
	layout    *ui.TextLayout
	runs      []lyricInkRun
}

func (cache *lyricInkLayout) fit(line lyrics.Line, font ui.Font, width float32, direction ui.Direction) {
	same := cache.layout != nil && cache.text == line.Text && cache.font == font && cache.width == width && cache.direction == direction && len(cache.wordTexts) == len(line.Words)
	if same {
		for i, word := range line.Words {
			if word.Text != cache.wordTexts[i] {
				same = false
				break
			}
		}
	}
	if same {
		return
	}
	*cache = lyricInkLayout{text: line.Text, font: font, width: width, direction: direction}
	cache.layout = ui.ShapeRichTextWith([]ui.Span{{Text: line.Text, LetterSpacing: -.4}}, font, ui.TextShape{Width: width, Direction: direction})
	cache.wordTexts = make([]string, len(line.Words))
	add := func(start, end, word int) {
		if end <= start {
			return
		}
		run := lyricInkRun{word: word}
		for _, rect := range cache.layout.SelectionRects(ui.TextInputRange{Start: start, End: end}) {
			// Hit testing inside the run also handles Latin words embedded in
			// a right-to-left paragraph, rather than reversing the whole line.
			left := cache.layout.IndexAt(ui.Point{X: rect.X + .1, Y: rect.Y + rect.H/2})
			right := cache.layout.IndexAt(ui.Point{X: rect.X + rect.W - .1, Y: rect.Y + rect.H/2})
			run.rects = append(run.rects, lyricInkRect{rect, left > right})
		}
		cache.runs = append(cache.runs, run)
	}
	cursor := 0
	for i, r := range lyricWordRanges(line.Text, line.Words) {
		cache.wordTexts[i] = line.Words[i].Text
		if r.End <= r.Start {
			continue
		}
		add(cursor, r.Start, -1)
		add(r.Start, r.End, i)
		cursor = r.End
	}
	add(cursor, len(utf16.Encode([]rune(line.Text))), -1)
}

// lyricSweep paints a feathered leading edge, with the same geometry for a
// word on one line, a wrapped word, and a right-to-left run.
func lyricSweep(p *ui.Painter, rect ui.Rect, rtl bool, progress, feather float32, draw func(float32)) {
	if progress <= 0 || rect.W <= 0 {
		return
	}
	if progress >= 1 {
		p.Clip(rect, 0, func() { draw(1) })
		return
	}
	feather = min(feather, rect.W*.45)
	reveal := progress * (rect.W + feather)
	solid := max(0, reveal-feather)
	clip := func(from, to, alpha float32) {
		from, to = max(0, from), min(rect.W, to)
		if to <= from {
			return
		}
		r := rect
		r.X += from
		if rtl {
			r.X = rect.X + rect.W - to
		}
		r.W = to - from
		p.Clip(r, 0, func() { draw(alpha) })
	}
	clip(0, solid, 1)
	const steps = 6
	for i := range steps {
		from := solid + feather*float32(i)/steps
		to := solid + feather*float32(i+1)/steps
		clip(from, to, 1-(float32(i)+.5)/steps)
	}
}

// karaokeText keeps the text widget's layout and accessibility, while its
// retained glyph layout paints the sweep without rebuilding the view each tick.
func karaokeText(c *ui.Context, line lyrics.Line, font ui.Font, dim, lit ui.Color, position func() time.Duration, playing func() bool, still bool) ui.Element {
	text := ui.Text(c, line.Text).Font(font.Family).FontSize(font.Size).FontWeight(font.Weight).
		LineHeight(font.LineHeight).LetterSpacing(-.4).TextColor(ui.RGBA(0, 0, 0, 0))
	cache := ui.Local(text, "karaoke-ink", func() lyricInkLayout { return lyricInkLayout{} })
	direction := c.Direction()
	text.Draw(func(p *ui.Painter, bounds ui.Rect) {
		cache.fit(line, font, bounds.W, direction)
		pos := position()
		lastWord := -1
		for _, run := range cache.runs {
			progress, pulse := float32(0), float32(0)
			if run.word >= 0 {
				lastWord = run.word
				word := line.Words[run.word]
				progress, pulse = fusionWordMotion(word, pos)
				if still {
					pulse = 0
					progress = 0
					if pos >= word.End {
						progress = 1
					}
				}
			} else if lastWord >= 0 && pos >= line.Words[lastWord].End {
				progress = 1
			}
			lift := min(2, font.Size*.065) * pulse
			total := float32(0)
			for _, rect := range run.rects {
				total += rect.W
			}
			before := float32(0)
			for _, ink := range run.rects {
				r := ink.Rect
				r.X += bounds.X
				r.Y += bounds.Y - lift
				p.Clip(r, 0, func() {
					p.TextLayout(cache.layout, bounds.X, bounds.Y-lift, dim)
				})
				local := clamp01((progress*total - before) / max(.01, ink.W))
				if pulse > 0 && local > 0 {
					// Paint the faint halo once per word, rather than once per
					// feather strip, keeping the sweep's drawing cost bounded.
					p.Clip(r, 0, func() {
						for _, offset := range [][2]float32{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
							p.TextLayout(cache.layout, bounds.X+offset[0], bounds.Y-lift+offset[1], lit.Alpha(.04*pulse*local))
						}
					})
				}
				lyricSweep(p, r, ink.rtl, local, min(10, font.Size*.3), func(alpha float32) {
					p.TextLayout(cache.layout, bounds.X, bounds.Y-lift, dim.Mix(lit, alpha))
				})
				before += ink.W
			}
		}
		if delay := lyricEffectDelay(line.Words, pos, playing(), still); delay > 0 {
			p.After(delay)
		}
	})
	return text
}
