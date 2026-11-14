package app

import (
	"math"
	"time"

	"github.com/egoist/mygo"
)

var fusionCursor = mygo.Screen.CursorScreenPoint

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

func fusionPinnedBounds(bounds mygo.Rectangle, displays []mygo.Display) mygo.Rectangle {
	if len(displays) == 0 {
		return bounds
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
	bounds.Width = min(bounds.Width, best.Bounds.Width)
	bounds.X = max(best.Bounds.X, min(best.Bounds.X+best.Bounds.Width-bounds.Width, bounds.X))
	bounds.Y = best.Bounds.Y
	return bounds
}

func (a *app) pinFusionIsland() {
	if a.fusion.win == nil || a.fusion.placing || a.settings.FusionMode != "capsule" || a.settings.FusionFloatingIsland {
		return
	}
	bounds := a.fusion.win.Bounds()
	target := fusionPinnedBounds(bounds, mygo.Screen.Displays())
	if bounds != target {
		a.fusion.placing = true
		a.fusion.win.SetBounds(target)
		a.fusion.placing = false
	}
}

func (a *app) centerFusionIsland() {
	a.settings.FusionFloatingIsland = false
	if a.settings.FusionMode != "capsule" {
		a.setFusionMode("capsule")
	} else if !a.pl.Playing() {
		a.fusion.manualShow = true
		a.syncFusionVisibility()
	}
	if a.fusion.win != nil {
		display := mygo.Screen.PrimaryDisplay()
		width, height := a.fusionWindowSize()
		width = min(width, display.Bounds.Width)
		a.fusion.win.SetBounds(mygo.Rectangle{X: display.Bounds.X + (display.Bounds.Width-width)/2, Y: display.Bounds.Y, Width: width, Height: height})
	}
	a.keepFusionPosition()
	a.settings.save()
	a.redraw()
}

func (a *app) updateFusionHover() {
	if a.fusion.win == nil || a.quitting {
		return
	}
	if a.settings.FusionMode != "capsule" {
		a.fusion.hover = fusionHover{opacity: 1}
		a.fusion.win.SetOpacity(1)
		a.fusion.win.SetIgnoreMouseEvents(a.settings.FusionLocked)
		return
	}
	bounds := a.fusion.win.Bounds()
	point := fusionCursor()
	inside := point.X >= bounds.X && point.X < bounds.X+bounds.Width && point.Y >= bounds.Y && point.Y < bounds.Y+bounds.Height
	previous := a.fusion.hover.opacity
	opacity, _ := a.fusion.hover.step(time.Now(), inside, !a.settings.FusionNoAvoid)
	if opacity != previous {
		a.fusion.win.SetOpacity(opacity)
	}
	passing := a.settings.FusionLocked || opacity < .99
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
	a.fusion.hover = fusionHover{opacity: 1}
	var poll func()
	poll = func() {
		if a.quitting || generation != a.fusion.hoverGeneration || a.fusion.win == nil || a.settings.FusionMode != "capsule" || a.fusion.pauseHidden {
			return
		}
		a.updateFusionHover()
		delay := 80 * time.Millisecond
		if opacity := a.fusion.hover.opacity; opacity > 0 && opacity < 1 {
			delay = time.Second / 60
		}
		time.AfterFunc(delay, func() { mygo.RunOnMain(poll) })
	}
	if a.settings.FusionMode == "capsule" {
		poll()
	} else {
		a.updateFusionHover()
	}
}
