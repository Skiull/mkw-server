package core

import "time"

// Not included in this build.

type shrinkLog struct{}

// Caller must hold mu.
func startShrinkLog(a *Aid, p *Player, now time.Time) {}

// startShock notes a Lightning hit on p. Caller must hold mu.
func startShock(p *Player, now time.Time) {}
