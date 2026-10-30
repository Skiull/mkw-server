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

func TestShockDuration(t *testing.T) {
	for _, c := range []struct {
		pos, players int
		want         time.Duration
	}{
		{1, 12, 12700 * time.Millisecond},
		{12, 12, 2700 * time.Millisecond},
		{1, 10, 11200 * time.Millisecond}, // Same as 3rd of 12.
		{3, 12, 11200 * time.Millisecond},
		{2, 2, 2700 * time.Millisecond}, // Row clamps to the last.
	} {
		if got := shockDuration(c.pos, c.players); got != c.want {
			t.Errorf("position %d of %d: %v, want %v", c.pos, c.players, got, c.want)
		}
	}
}

func TestShrunkWindow(t *testing.T) {
	setupRoom(t, Config{})
	a := addTestAid(t, 0, false)
	a.isRacer = true
	p := a.players[0]
	p.rank = 12
	now := time.Now()
	startShock(p, now)
	if p.shrunk(now.Add(500*time.Millisecond)) || !p.shrunk(now.Add(1500*time.Millisecond)) || p.shrunk(now.Add(3*time.Second)) {
		t.Fatal("shrunk window wrong (12th alone: 2.7 s, with 1 s margins)")
	}
	q := newPlayer(0, false)
	q.rank, q.starAt = 1, now
	startShock(q, now)
	if !q.shockFrom.IsZero() {
		t.Fatal("a Star user was shrunk")
	}
}
