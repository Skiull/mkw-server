package core

import (
	"testing"
	"time"
)

func TestShrinkSpeedLog(t *testing.T) {
	setupRoom(t, Config{})
	a := addTestAid(t, 0, false)
	a.raceStarted = true
	p := a.players[0]
	now := time.Now()
	startShrinkLog(a, p, now)
	p.move.lastSpeed = 80
	trackShrinkSpeed(a, p, now.Add(1500*time.Millisecond))
	if p.shrink.topSpeed[1] != 80 {
		t.Fatalf("second 1 top speed %v, want 80", p.shrink.topSpeed[1])
	}
	trackShrinkSpeed(a, p, now.Add(shrinkLogSeconds*time.Second))
	if !p.shrink.at.IsZero() {
		t.Fatal("log not finished after the window")
	}
}
