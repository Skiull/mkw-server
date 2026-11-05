package core

import "time"

// Not included in this build.

// Caller must hold mu.
func expectLightning(user *Aid, now time.Time) {}

// Caller must hold mu.
func expectBlueShell(user *Aid, now time.Time) {}

// Caller must hold mu.
func resetHitChecks() {}
