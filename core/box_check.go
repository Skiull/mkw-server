package core

import (
	"fmt"
	"time"

	"mkw-server/common"
	"mkw-server/items"
	"mkw-server/logging"
)

// Item box checks: an item request must come from a player who was next to an item box
// that's there to take (not taken by someone else and still respawning).

const (
	// How far back a player's reported positions count. Covers the time between touching
	// the box and the request arriving, plus lag between position updates.
	boxPositionWindow = 1500 * time.Millisecond
	// How close a reported position must be to a box's center, in course units. Real races
	// logged 183-563 on the ground, and ~1100 for a box hit mid-jump on Bowser's Castle
	// (positions arrive every few frames at up to ~120 u/f).
	boxTouchRadius = 1200
	// Two players touching the same box at nearly the same moment can both get it in their
	// own games (lag), so a box taken this recently doesn't count as gone yet.
	boxTakenGrace = 300 * time.Millisecond
)

type timedPos struct {
	at  time.Time
	pos [3]float32
}

type boxKey struct {
	course common.Course
	index  int
}

// When each box was last taken. Guarded by mu.
var boxTakenAt = make(map[boxKey]time.Time)

// recordPosition remembers a player's reported position. Caller must hold mu.
func recordPosition(p *Player, pos [3]float32, now time.Time) {
	p.positions = append(p.positions, timedPos{at: now, pos: pos})
	cut := 0
	for cut < len(p.positions) && now.Sub(p.positions[cut].at) > boxPositionWindow {
		cut++
	}
	p.positions = p.positions[cut:]
}

// checkItemBox finds the box a request is for. It returns the box (nil if none matched or
// the course's boxes aren't known) and, when the request doesn't look like a real box
// touch, why. Caller must hold mu.
func checkItemBox(a *Aid, p *Player, now time.Time) (*items.Box, string) {
	c := room.config.Courses[a.course]
	if c == nil || len(c.Boxes) == 0 {
		return nil, ""
	}
	boxes := c.Boxes
	if len(p.positions) == 0 {
		return nil, "no recent position"
	}

	nearest := float32(-1)
	takenIdx := -1
	for i := range boxes {
		b := &boxes[i]
		closest := float32(-1)
		for _, tp := range p.positions {
			if d := b.DistanceTo(tp.pos); closest < 0 || d < closest {
				closest = d
			}
		}
		if nearest < 0 || closest < nearest {
			nearest = closest
		}
		if closest > boxTouchRadius {
			continue
		}
		key := boxKey{a.course, i}
		if taken, ok := boxTakenAt[key]; ok && len(b.Route) == 0 {
			since := now.Sub(taken)
			if since > boxTakenGrace && since < b.Respawn {
				takenIdx = i
				continue
			}
		}
		boxTakenAt[key] = now
		// Logged while the radius is being tuned from real races.
		logging.Log("Aid %d box check ok: box %d on course %d, %.0f units away", a.aid, i, a.course, closest)
		return b, ""
	}

	if takenIdx >= 0 {
		return nil, fmt.Sprintf("box %d was taken %v ago and hasn't respawned", takenIdx,
			now.Sub(boxTakenAt[boxKey{a.course, takenIdx}]).Round(time.Millisecond))
	}
	last := p.positions[len(p.positions)-1].pos
	return nil, fmt.Sprintf("no item box nearby (nearest %.0f units; player at %.0f, %.0f, %.0f on course %d)",
		nearest, last[0], last[1], last[2], a.course)
}
