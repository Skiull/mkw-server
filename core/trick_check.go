package core

import (
	"fmt"
	"time"
)

// Trick-anywhere: a trick takes about 23 frames of being able to trick (13 on the trickable
// surface, 10 in the air after leaving it), so when the game reports a trick the player must
// have been on or just above a trickable surface (items/trick.go) a moment before. The
// positions from the last moveWindowFrames (about half a second) plus the current one are
// checked, plus known objects players trick off. With -kick-trick-cheats, a few flagged tricks
// in a short time kick (trickCheat).

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

// A trick-anywhere cheat is this many flagged tricks within trickKickWindow. One flag alone
// could be an object or ramp the check doesn't know; the cheat codes trick over and over.
const (
	trickKickCount  = 3
	trickKickWindow = 30 * time.Second
)

// trickCheat records a flagged trick and reports whether p now has enough of them to count
// as cheating. Caller must hold mu.
func trickCheat(p *Player, now time.Time) bool {
	kept := p.trickFlags[:0]
	for _, t := range p.trickFlags {
		if now.Sub(t) < trickKickWindow {
			kept = append(kept, t)
		}
	}
	p.trickFlags = append(kept, now)
	return len(p.trickFlags) >= trickKickCount
}
