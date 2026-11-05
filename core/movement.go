package core

import "time"

// Not included in this build.

type moveState struct{}

// Caller must hold mu.
func trackMovement(a *Aid, pkt *RacePacket, now time.Time) {}

// Caller must hold mu.
func resetMovement(a *Aid) {}
