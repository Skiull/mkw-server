package core

import (
	"fmt"
	"math"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
)

// Turning without losing speed (forum code "Don't lose Speed when turning in a Wheelie",
// 045746B8 480000E4 USA): the game takes speed off every frame a kart steers without
// drifting (KartMove::calcVehicleSpeed: speed *= turningSpeed + (1 - turningSpeed) *
// (1 - |turn| * speed ratio)), except while boosting. The code skips that, so a held hard
// turn keeps straight-line top speed.
//
// The check: a hard turn (stick at or next to full lock) held for turnHoldFrames without
// drifting, on plain road (no boost, zipper, offroad, air, steep descent, Star, Mega,
// Bullet Bill, shock or Thunder Cloud), that still runs at turnKeptRatio of the player's
// own straight-line top speed this race. Speed is the game's own value from RACEDATA.
// Log only: legit hard turns without drifting are rare in the recordings so far, so the
// threshold isn't tuned yet.

const (
	turnHardStick  = 6   // |stick X - 7| >= this: hard turn (7 is full lock).
	turnStraight   = 1   // |stick X - 7| <= this: driving straight.
	turnHoldFrames = 60  // Frames the hard turn must be held before speed is judged.
	turnKeptRatio  = 0.98
	turnMinTop     = 70  // u/f: no judging before a real straight-line top speed is seen.
	turnMaxDescent = 1.5 // u/f downward: downhill adds speed.
	turnItemWindow = 12 * time.Second
)

// raceDataSpeed is the kart's speed as the game keeps it (RACEDATA stream bits 135-153).
func raceDataSpeed(rd *records.RaceData) float32 {
	return decodeRDFloat(records.GetBits(rd.Raw(), rdSpeedBit, 19), 15, 1001)
}

// checkTurnSpeed sets m.turnNote when a hard non-drift turn keeps full speed. Called once
// per RaceData record with a position, after the boost bookkeeping. Caller must hold mu.
func checkTurnSpeed(p *Player, m *moveState, frame uint32, rd *records.RaceData, pos [3]float32, mega bool, now time.Time) {
	free := frame >= m.boostUntil && frame >= m.bulletUntil && frame >= m.exemptUntil && !mega && !m.shrunk &&
		!within(p.starAt, turnItemWindow, now) && !within(p.megaAt, turnItemWindow, now) && p.held != common.ThunderCloud &&
		!rd.Respawning()
	if free && m.floors != nil {
		ty, ok := m.floors.At(pos)
		if !ok || ty != items.FloorRoad && ty != items.FloorRoad2 && ty != items.FloorSlippery && ty != items.FloorSlippery2 && ty != items.FloorItemRoad {
			free = false // Air, offroad, boost floors, moving road...
		}
	}
	if n := len(m.samples); free && n >= 2 {
		prev := m.samples[n-2]
		if df := frame - prev.frame; df > 0 && (prev.pos[1]-pos[1])/float32(df) > turnMaxDescent {
			free = false
		}
	}
	drifting := rd.Buttons == 7 || rd.Buttons == 15
	stick := math.Abs(float64(rd.StickX) - 7)
	speed := raceDataSpeed(rd)

	if !free || drifting {
		m.turnSince = 0
		return
	}
	if stick <= turnStraight {
		m.turnTop = max(m.turnTop, speed)
	}
	if stick < turnHardStick {
		m.turnSince = 0
		return
	}
	if m.turnSince == 0 {
		m.turnSince, m.turnFlagged = frame, false
		return
	}
	if m.turnFlagged || frame-m.turnSince < turnHoldFrames || m.turnTop < turnMinTop {
		return
	}
	if speed >= turnKeptRatio*m.turnTop {
		m.turnFlagged = true
		m.turnNote = fmt.Sprintf("held a hard turn without drifting for %d frames at %.1f u/f (straight-line top %.1f) at (%.0f, %.0f, %.0f)",
			frame-m.turnSince, speed, m.turnTop, pos[0], pos[1], pos[2])
	}
}
