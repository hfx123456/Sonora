package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
)

var (
	fusionCursor    = mygo.Screen.CursorScreenPoint
	fusionDisplays  = mygo.Screen.Displays
	fusionMouseDown = fusionPrimaryButtonDown
)

type fusionHover struct {
	opacity         float64
	last, restoreAt time.Time
	inside          bool
	passing         bool
}

func (hover *fusionHover) step(now time.Time, inside, enabled bool) (float64, bool) {
	dt := now.Sub(hover.last).Seconds()
	if hover.last.IsZero() {
		hover.opacity = 1
		dt = 1.0 / 30
	}
	hover.last = now
	if !enabled {
		hover.inside = false
		hover.restoreAt = time.Time{}
		hover.opacity = 1
		return 1, false
	}
	if inside {
		hover.restoreAt = time.Time{}
	} else if hover.inside {
		hover.restoreAt = now.Add(120 * time.Millisecond)
	}
	hover.inside = inside
	target := float64(1)
	if inside || !hover.restoreAt.IsZero() && now.Before(hover.restoreAt) {
		target = 0
	}
	rate := float64(22)
	if target == 1 {
		rate = 14
	}
	hover.opacity += (target - hover.opacity) * (1 - math.Exp(-min(.05, max(0, dt))*rate))
	if math.Abs(target-hover.opacity) < .01 {
		hover.opacity = target
	}
	return hover.opacity, hover.opacity != target
}

func fusionDisplayForBounds(bounds mygo.Rectangle, displays []mygo.Display) mygo.Display {
	if len(displays) == 0 {
		return mygo.Screen.PrimaryDisplay()
	}
	centerX, centerY := bounds.X+bounds.Width/2, bounds.Y+bounds.Height/2
	best, distance := displays[0], int64(math.MaxInt64)
	for _, display := range displays {
		area := display.Bounds
		dx := int64(centerX - max(area.X, min(area.X+area.Width, centerX)))
		dy := int64(centerY - max(area.Y, min(area.Y+area.Height, centerY)))
		if squared := dx*dx + dy*dy; squared < distance {
			best, distance = display, squared
		}
	}
	return best
}

func fusionPinnedBounds(bounds mygo.Rectangle, displays []mygo.Display) mygo.Rectangle {
	if len(displays) == 0 {
		return bounds
	}
	best := fusionDisplayForBounds(bounds, displays)
	bounds.Width = min(bounds.Width, best.Bounds.Width)
	bounds.X = max(best.Bounds.X, min(best.Bounds.X+best.Bounds.Width-bounds.Width, bounds.X))
	bounds.Y = best.Bounds.Y
	return bounds
}

func fusionAtTopEdge(bounds mygo.Rectangle, displays []mygo.Display) bool {
	if len(displays) == 0 {
		return true
	}
	best := fusionDisplayForBounds(bounds, displays)
	return bounds.Y >= best.Bounds.Y-8 && bounds.Y <= best.Bounds.Y+8
}

func (a *app) fusionFloatingShape() bool {
	if a.settings.FusionFloatingIsland {
		return true
	}
	if a.fusion.win == nil {
		return false
	}
	return !fusionAtTopEdge(a.fusion.win.Bounds(), fusionDisplays())
}

func fusionRestoreBounds(saved [2]int, width, height int, mode string, floating bool, displays []mygo.Display) (mygo.Rectangle, bool) {
	if len(displays) == 0 {
		return mygo.Rectangle{}, false
	}
	candidate := mygo.Rectangle{X: saved[0], Y: saved[1], Width: width, Height: height}
	screen := fusionDisplayForBounds(candidate, displays)
	area := screen.WorkArea
	if mode == "capsule" {
		area = screen.Bounds
	}
	if area.Width > 32 {
		width = min(width, area.Width-32)
	} else if area.Width > 0 {
		width = min(width, area.Width)
	}
	cx := saved[0] + width/2
	cy := saved[1] + max(1, height/2)
	if cx < area.X || cx > area.X+area.Width || cy < area.Y-24 || cy > area.Y+area.Height {
		return mygo.Rectangle{}, false
	}
	y := saved[1]
	if mode == "capsule" && !floating && y >= screen.Bounds.Y-8 && y <= screen.Bounds.Y+8 {
		y = screen.Bounds.Y
	}
	return mygo.Rectangle{X: saved[0], Y: y, Width: width, Height: height}, true
}

func (a *app) onFusionMove() {
	if a.fusion.win == nil || a.fusion.placing || a.settings.FusionMode == "" {
		return
	}
	a.fusion.movedAt = time.Now()
	a.keepFusionPosition()
	a.settings.save()
	a.fusion.win.Invalidate()
}

func (a *app) pinFusionIsland() {
	if a.fusion.win == nil || a.fusion.placing || a.settings.FusionMode != "capsule" || a.settings.FusionFloatingIsland {
		return
	}
	bounds := a.fusion.win.Bounds()
	target := fusionPinnedBounds(bounds, fusionDisplays())
	if bounds != target {
		a.fusion.placing = true
		a.fusion.win.SetBounds(target)
		a.fusion.placing = false
	}
}

func (a *app) centerFusionIsland() {
	a.settings.FusionFloatingIsland = false
	a.settings.FusionLocked = false
	if a.settings.FusionMode != "capsule" {
		a.setFusionMode("capsule")
	} else if !a.pl.Playing() {
		a.fusion.manualShow = true
		a.syncFusionVisibility()
	}
	if a.fusion.win != nil {
		display := fusionDisplayForBounds(a.fusion.win.Bounds(), fusionDisplays())
		width, height := a.fusionWindowSize()
		if display.Bounds.Width > 0 {
			width = min(width, display.Bounds.Width)
		}
		a.fusion.placing = true
		a.fusion.win.SetBounds(mygo.Rectangle{X: display.Bounds.X + (display.Bounds.Width-width)/2, Y: display.Bounds.Y, Width: width, Height: height})
		a.fusion.placing = false
		a.updateFusionHover()
	}
	a.keepFusionPosition()
	a.settings.save()
	a.redraw()
}

func (a *app) moveFusionIslandToNextDisplay() {
	a.settings.FusionLocked = false
	if a.settings.FusionMode != "capsule" {
		a.setFusionMode("capsule")
	}
	if a.fusion.win == nil {
		return
	}
	displays := fusionDisplays()
	if len(displays) < 2 {
		return
	}
	bounds := a.fusion.win.Bounds()
	current := fusionDisplayForBounds(bounds, displays)
	next := displays[0]
	for i, d := range displays {
		if d.Bounds == current.Bounds {
			next = displays[(i+1)%len(displays)]
			break
		}
	}
	width, height := a.fusionWindowSize()
	if next.Bounds.Width > 0 {
		width = min(width, next.Bounds.Width)
	}
	x := next.Bounds.X + (next.Bounds.Width-width)/2
	y := next.Bounds.Y
	if !fusionAtTopEdge(bounds, displays) && current.Bounds.Height > 0 {
		offsetY := bounds.Y - current.Bounds.Y
		y = max(next.Bounds.Y, min(next.Bounds.Y+next.Bounds.Height-height, next.Bounds.Y+offsetY))
	}
	a.fusion.placing = true
	a.fusion.win.SetBounds(mygo.Rectangle{X: x, Y: y, Width: width, Height: height})
	a.fusion.placing = false
	a.keepFusionPosition()
	a.settings.save()
	a.updateFusionHover()
	a.redraw()
}

func fusionCapsuleHitBounds(bounds mygo.Rectangle, capsuleW, capsuleH float32) mygo.Rectangle {
	out := bounds
	if capsuleW > 0 {
		if w := min(bounds.Width, int(math.Ceil(float64(capsuleW)))); w > 0 {
			out.X = bounds.X + (bounds.Width-w)/2
			out.Width = w
		}
	}
	if capsuleH > 0 {
		if h := min(bounds.Height, int(math.Ceil(float64(capsuleH)))); h > 0 {
			out.Height = h
		}
	}
	return out
}

func fusionPointInRect(p mygo.Point, r mygo.Rectangle) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

func fusionUnlockRect(bounds mygo.Rectangle, mode string, capsuleW, capsuleH, scale float32) mygo.Rectangle {
	w := max(64, int(math.Round(float64(76*scale))))
	h := max(24, int(math.Round(float64(28*scale))))
	if mode == "capsule" {
		hit := fusionCapsuleHitBounds(bounds, capsuleW, capsuleH)
		w = min(w, max(48, hit.Width-12))
		h = min(h, max(20, hit.Height-4))
		return mygo.Rectangle{
			X:      hit.X + (hit.Width-w)/2,
			Y:      hit.Y + max(2, (hit.Height-h)/2),
			Width:  w,
			Height: h,
		}
	}
	return mygo.Rectangle{
		X:      bounds.X + (bounds.Width-w)/2,
		Y:      bounds.Y + 4,
		Width:  w,
		Height: h,
	}
}

func (a *app) updateFusionHover() {
	if a.fusion.win == nil || a.quitting {
		return
	}
	bounds := a.fusion.win.Bounds()
	point := fusionCursor()
	if a.settings.FusionLocked {
		hit := bounds
		if a.settings.FusionMode == "capsule" {
			hit = fusionCapsuleHitBounds(bounds, a.fusion.capsuleW, a.fusion.capsuleH)
		}
		inHit := fusionPointInRect(point, hit)
		down := fusionMouseDown()
		if down && !a.fusion.lockDown && inHit {
			a.fusion.lockRevealUntil = time.Now().Add(3 * time.Second)
		}
		a.fusion.lockDown = down
		revealed := inHit || (!a.fusion.lockRevealUntil.IsZero() && time.Now().Before(a.fusion.lockRevealUntil))
		if a.fusion.hover.opacity != 1 {
			a.fusion.hover.opacity = 1
			a.fusion.win.SetOpacity(1)
		}
		unlockRect := fusionUnlockRect(bounds, a.settings.FusionMode, a.fusion.capsuleW, a.fusion.capsuleH, a.fusionScale())
		passing := !revealed || !fusionPointInRect(point, unlockRect)
		if passing != a.fusion.hover.passing {
			a.fusion.hover.passing = passing
			a.fusion.win.SetIgnoreMouseEvents(passing)
		}
		if revealed != a.fusion.lockRevealed {
			a.fusion.lockRevealed = revealed
			a.fusion.win.Invalidate()
		}
		return
	}
	a.fusion.lockRevealed = false
	a.fusion.lockRevealUntil = time.Time{}
	a.fusion.lockDown = false
	if a.settings.FusionMode != "capsule" {
		a.fusion.hover = fusionHover{opacity: 1}
		a.fusion.win.SetOpacity(1)
		a.fusion.win.SetIgnoreMouseEvents(false)
		return
	}
	if !a.fusion.movedAt.IsZero() {
		if time.Since(a.fusion.movedAt) < 400*time.Millisecond {
			if a.fusion.hover.opacity != 1 {
				a.fusion.hover.opacity = 1
				a.fusion.win.SetOpacity(1)
			}
			if a.fusion.hover.passing {
				a.fusion.hover.passing = false
				a.fusion.win.SetIgnoreMouseEvents(false)
			}
			return
		}
		a.fusion.movedAt = time.Time{}
		if !a.settings.FusionFloatingIsland && fusionAtTopEdge(a.fusion.win.Bounds(), fusionDisplays()) {
			a.pinFusionIsland()
			a.keepFusionPosition()
			a.settings.save()
		}
	}
	hit := fusionCapsuleHitBounds(bounds, a.fusion.capsuleW, a.fusion.capsuleH)
	inHit := fusionPointInRect(point, hit)
	inReach := point.X >= hit.X-14 && point.X < hit.X+hit.Width+14 && point.Y >= hit.Y-10 && point.Y < hit.Y+hit.Height+14
	previous := a.fusion.hover.opacity
	opacity, _ := a.fusion.hover.step(time.Now(), inHit, !a.settings.FusionNoAvoid)
	if opacity != previous {
		a.fusion.win.SetOpacity(opacity)
	}
	passing := !inReach || opacity < .99
	if passing != a.fusion.hover.passing {
		a.fusion.hover.passing = passing
		a.fusion.win.SetIgnoreMouseEvents(passing)
	}
	if opacity > 0 && opacity != previous {
		a.fusion.win.Invalidate()
	}
}

func (a *app) startFusionHover() {
	a.fusion.hoverGeneration++
	generation := a.fusion.hoverGeneration
	a.fusion.hover = fusionHover{opacity: 1, passing: a.settings.FusionLocked}
	if a.fusion.win != nil {
		a.fusion.win.SetOpacity(1)
		a.fusion.win.SetIgnoreMouseEvents(a.settings.FusionLocked)
	}
	var poll func()
	poll = func() {
		if a.quitting || generation != a.fusion.hoverGeneration || a.fusion.win == nil || a.settings.FusionMode == "" || a.fusion.pauseHidden {
			return
		}
		if a.settings.FusionMode != "capsule" && !a.settings.FusionLocked {
			return
		}
		a.updateFusionHover()
		delay := 32 * time.Millisecond
		if opacity := a.fusion.hover.opacity; opacity > 0 && opacity < 1 {
			delay = time.Second / 60
		}
		time.AfterFunc(delay, func() { mygo.RunOnMain(poll) })
	}
	if a.settings.FusionMode == "capsule" || a.settings.FusionLocked {
		poll()
	} else {
		a.updateFusionHover()
	}
}
