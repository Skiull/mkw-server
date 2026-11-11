package core

import (
	"testing"
	"time"
)

func TestRamWithoutHitIsLogged(t *testing.T) {
	setupRoom(t, Config{})
	attacker := addTestAid(t, 0, false)
	victim := addTestAid(t, 1, false)
	now := time.Now()
	a, v := attacker.players[0], victim.players[0]
	a.starAt = now
	a.lastPos, a.lastPosAt = [3]float32{0, 0, 0}, now
	v.lastPos, v.lastPosAt = [3]float32{100, 0, 0}, now
	v.move.lastSpeed = 80

	checkRams(victim, v, now)
	if len(hitExpectations) != 1 {
		t.Fatalf("%d expectations, want 1", len(hitExpectations))
	}
	// No hit flag, no slowdown: expectation runs out (logged) and is dropped.
	resolveHits(now.Add(2 * time.Second))
	if len(hitExpectations) != 0 {
		t.Fatal("expired expectation kept")
	}

	// A hit flag flip resolves it.
	v.rammedAt = time.Time{}
	checkRams(victim, v, now)
	v.hitAt = now.Add(100 * time.Millisecond)
	resolveHits(now.Add(200 * time.Millisecond))
	if len(hitExpectations) != 0 {
		t.Fatal("hit flag didn't resolve the expectation")
	}

	// A spin-out (speed falls) resolves it too.
	v.rammedAt, v.hitAt = time.Time{}, time.Time{}
	checkRams(victim, v, now)
	v.move.lastSpeed = 20
	resolveHits(now.Add(300 * time.Millisecond))
	if len(hitExpectations) != 0 {
		t.Fatal("slowdown didn't resolve the expectation")
	}
}

func TestNoRamWhenBothInvincible(t *testing.T) {
	setupRoom(t, Config{})
	attacker := addTestAid(t, 0, false)
	victim := addTestAid(t, 1, false)
	now := time.Now()
	a, v := attacker.players[0], victim.players[0]
	a.starAt, v.starAt = now, now
	a.lastPosAt, v.lastPosAt = now, now
	checkRams(victim, v, now)
	if len(hitExpectations) != 0 {
		t.Fatal("expected a hit between two Star users")
	}
}

func TestNoHitKick(t *testing.T) {
	setupRoom(t, Config{KickNoHit: true})
	attacker := addTestAid(t, 0, false)
	victim := addTestAid(t, 1, false)
	now := time.Now()
	a, v := attacker.players[0], victim.players[0]
	a.starAt, a.lastPos, a.lastPosAt = now, [3]float32{}, now
	v.lastPos, v.lastPosAt, v.move.lastSpeed = [3]float32{100, 0, 0}, now, 80
	checkRams(victim, v, now)
	resolveHits(now.Add(2 * time.Second))
	if victim.kicked {
		t.Fatal("kicked for a ram (lag decides those)")
	}
	// A Lightning the attacker's packets never told the victim about: nothing to be hit by.
	victim.raceStarted = true
	v.move.lastSpeed = 80
	expectLightning(attacker, now)
	resolveHits(now.Add(5 * time.Second))
	if victim.kicked {
		t.Fatal("kicked for a Lightning their game was never sent")
	}
	// Lightning hits everyone: missing one their game was sent kicks.
	attacker.lightningSentTo = map[byte]time.Time{victim.aid: now}
	expectLightning(attacker, now)
	resolveHits(now.Add(5 * time.Second))
	if !victim.kicked {
		t.Fatal("not kicked for skipping a Lightning")
	}
}
