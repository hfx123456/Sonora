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
	if dt, active := s.elapsed(now.Add(1500 * time.Millisecond)); !active || dt < 1.49 || dt > 1.51 {
		t.Fatalf("mid-animation: got (%v, %v), want (~1.50, true)", dt, active)
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

func TestSplashMainOpacity(t *testing.T) {
	if op := splashMainOpacity(1.0); op != 0 {
		t.Fatalf("before reveal: got %v, want 0", op)
	}
	if op := splashMainOpacity(2.30); op <= 0.1 || op >= 0.9 {
		t.Fatalf("mid reveal: got %v, expected smooth transition between 0 and 1", op)
	}
	if op := splashMainOpacity(2.90); op != 1 {
		t.Fatalf("after reveal: got %v, want 1", op)
	}
}

func TestSplashBarBounce(t *testing.T) {
	if s, a := splashBarBounce(0.20, 0.96); s != 0 || a != 0 {
		t.Fatalf("before start: got (%v, %v), want (0, 0)", s, a)
	}
	s, a := splashBarBounce(1.30, 0.96)
	if s <= 0.5 || a < 0.99 {
		t.Fatalf("during beat: got scale=%v alpha=%v, expected active bounce", s, a)
	}
	sEnd, aEnd := splashBarBounce(2.40, 0.96)
	if sEnd < 0.99 || sEnd > 1.01 || aEnd != 1 {
		t.Fatalf("settled bar: got scale=%v alpha=%v, want (~1, 1)", sEnd, aEnd)
	}
}
