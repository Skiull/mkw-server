package core

import "time"

// Client version checks are not included in this build.

func isGameHashPacket(data []byte) bool { return false }

// Caller must hold mu.
func handleGameHash(a *Aid, data []byte, now time.Time) {}
