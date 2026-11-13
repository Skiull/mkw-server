package core

import (
	"fmt"
)

// Trick-anywhere: a trick takes about 23 frames of being able to trick (13 on the trickable
// surface, 10 in the air after leaving it), so when the game reports a trick the player must
// have been on or just above a trickable surface (items/trick.go) a moment before. The
// positions from the last moveWindowFrames (about half a second) plus the current one are
// checked, plus known objects players trick off. Log only for now.

const (
	// Generous: positions are the vehicle's center, sent every few frames, and a trick can
	// start from the lip of a ramp.
	trickReach = 400
	// Surface at most this far above the reported position, or this far below it (high in
	// the air off a big ramp).
	trickAbove = 200
	trickBelow = 2500
)

// checkTrickSurface sets m.trickNote when a trick happened nowhere near a trickable surface.
// Caller must hold mu.
func checkTrickSurface(m *moveState, frame uint32, pos [3]float32) {
	if m.floors == nil || m.floors.Trick == nil || len(m.floors.Trick.Tris) == 0 {
		return
	}
	// Cannons and Bullet Bills move the player in ways the positions can't follow.
	if frame < m.exemptUntil || frame < m.bulletUntil {
		return
	}
	near := func(p [3]float32) bool {
		return m.floors.NearTrickable(p, trickReach, trickAbove, trickBelow) || m.course != nil && m.course.NearTrickObject(p)
	}
	if near(pos) {
		return
	}
	for _, s := range m.samples {
		if near(s.pos) {
			return
		}
	}
	m.trickNote = fmt.Sprintf("trick at frame %d at (%.0f, %.0f, %.0f) with no trickable surface nearby in the last %d positions",
		frame, pos[0], pos[1], pos[2], len(m.samples)+1)
}
