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
const fusionCaptionDuration = 460 * time.Millisecond

// fusionSpringEval evaluates a unit step response for an underdamped spring
// (normalized over duration), returning a value that smoothly overshoots 1 and settles.
func fusionSpringEval(phase, bounce float32) float32 {
	if phase <= 0 {
		return 0
	}
	if phase >= 1 {
		return 1
	}
	// Undamped natural frequency scaled so the spring settles cleanly by phase == 1.
	t := float64(phase)
	zeta := max(0.2, min(0.95, 1-float64(bounce)))
	wn := 2 * math.Pi * 1.15
	wd := wn * math.Sqrt(1-zeta*zeta)
	env := math.Exp(-zeta * wn * t)
	pos := 1 - env*(math.Cos(wd*t)+(zeta/math.Sqrt(1-zeta*zeta))*math.Sin(wd*t))
	// Smoothly blend the tail to exact 1 over the final 15% so phase==1 is C0-continuous.
	if phase > 0.85 {
		blend := float64((phase - 0.85) / 0.15)
		pos = pos*(1-blend) + 1*blend
	}
	return float32(pos)
}

// fusionIslandPulse returns a brief damped elastic impulse that tapers to 0 by phase == 1.
func (motion *fusionCaptionMotion) pulse(now time.Time, still bool) float32 {
	phase := motion.phase(now, still)
	if still || phase <= 0 || phase >= 1 {
		return 0
	}
	t := float64(phase)
	return float32(math.Exp(-4.6*t)*math.Sin(2.2*math.Pi*t)) * (1 - phase)
}

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

func (a *app) fusionAnimatedCaption(c *ui.Context, line lyrics.Line, translation string, originalSize, translationSize float32, outline bool, fitWidth float32) {
	motion := &a.fusion.caption
	still := a.settings.FusionStatic || c.Preferences().ReduceMotion
	motion.update(fusionTrackKey(a.pl.Current())+a.settings.FusionMode, line, translation, a.fusionPosition(), c.Now())
	phase := motion.phase(c.Now(), still)
	fade := 1 - float32(math.Pow(float64(clamp01(1-phase*1.25)), 3))
	leaveFade := clamp01(1 - phase*1.4)
	springPos := fusionSpringEval(phase, 0.34)
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
					a.fusionSingleLine(c, caption.line, caption.position, originalSize, white, dim, outline, !still, fitWidth)
				}
				if caption.translation != "" {
					translated := fusionTranslationLine(caption.line, caption.translation)
					a.fusionSingleLine(c, translated, caption.position, translationSize, white.Alpha(.86), white.Alpha(.38), outline, !still, fitWidth)
				}
			})
		}
		if phase < 1 && motion.previous.line.Text != "" {
			previous := motion.previous
			previous.line.Words = nil
			previous.line.End = previous.line.At
			draw("leaving", previous, leaveFade, -14*fade, 1-.05*fade)
		}
		draw("entering", motion.current, fade, 14*(1-springPos), .92+.08*springPos)
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
