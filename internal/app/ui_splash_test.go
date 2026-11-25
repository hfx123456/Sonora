package app

import (
	"testing"
	"time"
)

func TestSplashLifecycle(t *testing.T) {
	var s splashState
	now := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)

	if _, active := s.elapsed(now); active {
		t.Fatal("zero splashState should be inactive")
	}

	s.start(now)
	if dt, active := s.elapsed(now); !active || dt != 0 {
		t.Fatalf("at start: got (%v, %v), want (0, true)", dt, active)
	}
	if dt, active := s.elapsed(now.Add(450 * time.Millisecond)); !active || dt < 0.44 || dt > 0.46 {
		t.Fatalf("mid-animation: got (%v, %v), want (~0.45, true)", dt, active)
	}
	if _, active := s.elapsed(now.Add(splashTotalDuration)); active {
		t.Fatal("splash should finish once total duration elapses")
	}
	if !s.done {
		t.Fatal("splash should mark itself done after finishing")
	}
	if _, active := s.elapsed(now.Add(100 * time.Millisecond)); active {
		t.Fatal("completed splash should remain inactive")
	}
}

func TestSplashMorphRect(t *testing.T) {
	r0, rad0 := splashMorphRect(1180, 780, 0)
	if r0.W != 116 || r0.H != 116 || rad0 != 28 {
		t.Fatalf("at expand=0: got rect=%+v radius=%v, want 116x116 r=28", r0, rad0)
	}
	if r0.X != (1180-116)/2 || r0.Y != (780-116)/2 {
		t.Fatalf("at expand=0: expected centered rect, got (%v, %v)", r0.X, r0.Y)
	}

	r1, rad1 := splashMorphRect(1180, 780, 1)
	if r1.X != 0 || r1.Y != 0 || r1.W != 1180 || r1.H != 780 || rad1 != 10 {
		t.Fatalf("at expand=1: got rect=%+v radius=%v, want full 1180x780 r=10", r1, rad1)
	}
}

func TestSplashBarBounce(t *testing.T) {
	if s, a := splashBarBounce(0.10, 0.25); s != 0 || a != 0 {
		t.Fatalf("before start: got (%v, %v), want (0, 0)", s, a)
	}
	s, a := splashBarBounce(0.40, 0.18)
	if s <= 0.5 || a < 0.99 {
		t.Fatalf("during beat: got scale=%v alpha=%v, expected active bounce", s, a)
	}
	sEnd, aEnd := splashBarBounce(1.0, 0.18)
	if sEnd < 0.99 || sEnd > 1.01 || aEnd != 1 {
		t.Fatalf("settled bar: got scale=%v alpha=%v, want (~1, 1)", sEnd, aEnd)
	}
}
