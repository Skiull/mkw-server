package core

import "time"

// Client data checks are not included in this build.

func isDataChangesPacket(data []byte) bool { return false }

// Caller must hold mu.
func handleDataChanges(a *Aid, data []byte, now time.Time) {}
