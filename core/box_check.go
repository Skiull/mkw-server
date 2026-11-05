package core

import (
	"time"

	"mkw-server/items"
)

// Not included in this build.

type timedPos struct{}

// Caller must hold mu.
func recordPosition(p *Player, pos [3]float32, now time.Time) {}

// checkItemBox finds the box a request is for (nil if unknown) and, when the request
// should be refused, why. Caller must hold mu.
func checkItemBox(a *Aid, p *Player, now time.Time) (*items.Box, string) {
	return nil, ""
}
