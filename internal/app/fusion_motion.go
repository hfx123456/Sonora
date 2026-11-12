package app

import (
	"math"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"

	"vinyl/internal/lyrics"
)

const fusionMotionFrame = time.Second / 60
const fusionIntroFrame = time.Second / 30
const fusionCaptionDuration = 360 * time.Millisecond

func fusionWordOffsets(text string, words []lyrics.Word) []int {
	offsets := make([]int, len(words))
	cursor := 0
	for index, word := range words {
		if offset := strings.Index(text[cursor:], word.Text); offset >= 0 {
			cursor += offset + len(word.Text)
		}
		offsets[index] = len(utf16.Encode([]rune(text[:cursor])))
	}
	return offsets
}

func prepareFusionLines(lines []lyrics.Line) []lyrics.Line {
	for index := range lines {
		line := &lines[index]
		if line.Unsynced || lyrics.IsCredit(line.Text) {
			continue
		}
		if line.End <= line.At {
			line.End = line.At + 6*time.Second
			if index+1 < len(lines) && lines[index+1].At > line.At {
				line.End = lines[index+1].At
			}
		}
		if len(line.Words) == 0 {
			line.Words = lyrics.SpreadWords(line.Text, line.At, line.End)
			line.Timed = false
		}
	}
	return lines
}

func fusionFrameDelay(lines []lyrics.Line, position time.Duration, playing, still bool) time.Duration {
	if !playing || len(lines) == 0 || lines[0].Unsynced {
		return 0
	}
	index := lyrics.Index(lines, position)
	if index >= 0 && !still && !lyrics.IsCredit(lines[index].Text) && position < lines[index].End+250*time.Millisecond {
		return fusionMotionFrame
	}
	next := time.Duration(0)
	if index+1 < len(lines) {
		next = lines[index+1].At - position
	}
	if index >= 0 {
		end := lines[index].End + 2*time.Second - position
		if end > 0 && (next == 0 || end < next) {
			next = end
		}
	}
	if next > 0 {
		return next + 10*time.Millisecond
	}
	return 0
}

// fusionFloatingDelay is fusionFrameDelay for the floating island, whose
// emblem dances whenever playing. Through the intro's credits and the gaps
// between lines no lyric line asks for frames, so without this the emblem
// froze and jumped; it keeps a slow beat going instead.
func fusionFloatingDelay(lines []lyrics.Line, position time.Duration, playing, still bool) time.Duration {
	delay := fusionFrameDelay(lines, position, playing, still)
	if playing && !still && (delay == 0 || delay > fusionIntroFrame) {
		delay = fusionIntroFrame
	}
	return delay
}

func fusionWordMotion(word lyrics.Word, position time.Duration) (progress, pulse float32) {
	duration := max(time.Millisecond, word.End-word.Start)
	progress = clamp01(float32(float64(position-word.Start) / float64(duration)))
	attack := clamp01(float32((position - word.Start).Seconds() / .16))
	release := clamp01(float32((word.End + 220*time.Millisecond - position).Seconds() / .22))
	pulse = attack * attack * (3 - 2*attack) * release * release * (3 - 2*release)
	return progress, pulse
}

func fusionTranslationLine(original lyrics.Line, text string) lyrics.Line {
	line := lyrics.Line{Text: text, At: original.At, End: original.End, Unsynced: original.Unsynced}
	if len(original.Words) > 0 {
		line.At = original.Words[0].Start
		line.End = original.Words[len(original.Words)-1].End
	}
	if !line.Unsynced && !lyrics.IsCredit(original.Text) && line.End > line.At {
		line.Words = lyrics.SpreadWords(text, line.At, line.End)
	}
	return line
}

type fusionCaption struct {
	line        lyrics.Line
	translation string
	position    time.Duration
}

type fusionCaptionMotion struct {
	scope             string
	current, previous fusionCaption
	changed           time.Time
	ready             bool
}

func (motion *fusionCaptionMotion) update(scope string, line lyrics.Line, translation string, position time.Duration, now time.Time) {
	next := fusionCaption{line, translation, position}
	if !motion.ready || motion.scope != scope {
		*motion = fusionCaptionMotion{scope: scope, current: next, ready: true}
		return
	}
	if line.At != motion.current.line.At || line.Text != motion.current.line.Text || translation != motion.current.translation {
		motion.previous = motion.current
		motion.current = next
		motion.changed = now
	} else {
		motion.current = next
	}
}

func (motion *fusionCaptionMotion) phase(now time.Time, still bool) float32 {
	if still || motion.changed.IsZero() {
		return 1
	}
	return clamp01(float32(now.Sub(motion.changed)) / float32(fusionCaptionDuration))
}

func (a *app) fusionAnimatedCaption(c *ui.Context, line lyrics.Line, translation string, originalSize, translationSize float32, outline bool) {
	motion := &a.fusion.caption
	still := a.settings.FusionStatic || c.Preferences().ReduceMotion
	motion.update(fusionTrackKey(a.pl.Current())+a.settings.FusionMode, line, translation, a.fusionPosition(), c.Now())
	phase := motion.phase(c.Now(), still)
	eased := 1 - float32(math.Pow(float64(1-phase), 3))
	height := originalSize * 1.5
	if translation != "" {
		height += translationSize*1.4 + 2*a.fusionScale()
	}
	ui.Box(c.Key("caption-motion")).Height(height).AlignSelf(ui.Stretch).Clip().Children(func() {
		draw := func(key string, caption fusionCaption, opacity, offset, scale float32) {
			ui.Column(c.Key(key)).Absolute().Fill().Justify(ui.Center).Gap(2*a.fusionScale()).Opacity(opacity).Translate(0, offset).TransformOrigin(.5, .5).Scale(scale, scale).Children(func() {
				dim := white.Alpha(.38)
				if outline {
					dim = white.Alpha(.55)
				}
				if caption.line.Text == fusionWaitingNotes && caption.translation == "" {
					a.fusionWaitingAnimation(c, originalSize, caption.position, still)
				} else {
					a.fusionSingleLine(c, caption.line, caption.position, originalSize, white, dim, outline, !still)
				}
				if caption.translation != "" {
					translated := fusionTranslationLine(caption.line, caption.translation)
					a.fusionSingleLine(c, translated, caption.position, translationSize, white.Alpha(.86), white.Alpha(.38), outline, !still)
				}
			})
		}
		if phase < 1 && motion.previous.line.Text != "" {
			previous := motion.previous
			previous.line.Words = nil
			previous.line.End = previous.line.At
			draw("leaving", previous, 1-eased, -14*eased, 1-.025*eased)
		}
		draw("entering", motion.current, eased, 14*(1-eased), .975+.025*eased)
	})
	if phase < 1 {
		c.After(fusionMotionFrame)
	}
}

// fusionWaitingAnimation replaces the loading and intro messages with gently moving notes.
func (a *app) fusionWaitingAnimation(c *ui.Context, size float32, position time.Duration, still bool) {
	ui.Row(c).Height(size * 1.5).AlignSelf(ui.Stretch).Justify(ui.Center).AlignItems(ui.Center).Gap(size * .38).Children(func() {
		for index := range 3 {
			offset := float32(0)
			if !still {
				t := float64(c.Now().UnixNano()) / 1e9
				offset = float32(math.Sin(t*4-float64(index)*.9)) * size * .12
			}
			ui.Icon(c.Key(index), iconNote).Size(size*.62, size*.62).TextColor(white.Alpha(.9)).Translate(0, offset)
		}
	})
	if !still && a.pl.Playing() {
		c.After(fusionMotionFrame)
	}
}
