package app

import (
	"testing"
	"time"
)

func TestSplashLifecycle(t *testing.T) {
	var s splashState
	now := time.Date(2026, 11, 25, 15, 0, 0, 0, time.UTC)

	// Zero value is inactive so unit tests and screenshot scenes aren't covered.
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

func TestSplashBarBounce(t *testing.T) {
	if s, a := splashBarBounce(0.10, 0.25); s != 0 || a != 0 {
		t.Fatalf("before start: got (%v, %v), want (0, 0)", s, a)
	}
	s, a := splashBarBounce(0.45, 0.25)
	if s <= 0.5 || a < 0.99 {
		t.Fatalf("during beat: got scale=%v alpha=%v, expected active bounce", s, a)
	}
	sEnd, aEnd := splashBarBounce(1.0, 0.25)
	if sEnd < 0.99 || sEnd > 1.01 || aEnd != 1 {
		t.Fatalf("settled bar: got scale=%v alpha=%v, want (~1, 1)", sEnd, aEnd)
	}
}
