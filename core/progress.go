package core

import (
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
)

// Not included in this build.

type progressState struct{}

func newProgress() progressState { return progressState{} }

// itemRank is the rank item odds use: the client's own rank. Caller must hold mu.
func itemRank(p *Player, course common.Course) (rank int, capped bool) {
	return int(p.rank), false
}

// Caller must hold mu.
func trackProgress(a *Aid, idx int, rd *records.RaceData, course common.Course, frame uint32, now time.Time) {
}

// Caller must hold mu.
func resetProgress(a *Aid) {}

// Caller must hold mu.
func checkFinish(a *Aid, pkt *RacePacket, now time.Time) {}
