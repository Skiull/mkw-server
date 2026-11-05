package core

import (
	"testing"
	"time"
)

func TestPlayerInvincible(t *testing.T) {
	p := newPlayer(0, false)
	now := time.Now()
	if p.invincible(now) {
		t.Fatal("invincible with nothing used")
	}
	p.starAt = now.Add(-5 * time.Second)
	if !p.invincible(now) {
		t.Fatal("Star not counted")
	}
	if p.invincible(now.Add(10 * time.Second)) {
		t.Fatal("Star counted after it ended")
	}
	p.starAt = time.Time{}
	p.megaAt = now
	if !p.invincible(now) {
		t.Fatal("Mega not counted")
	}
}
