package core

import (
	"fmt"
	"math"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
	"mkw-server/logging"
)

// Ground items (log only): Bananas and Fake Item Boxes lying on the track. The throw's EVENT
// data gives where an item spawns and its velocity (the thrower's included). A Banana flies
// with constant horizontal speed and 1.05 u/f^2 gravity and settles 150 units above the
// floor (fitted to test throws: landings within 150 units of where they were hit, 55 on
// average). Fake Item Boxes use 1.2 u/f^2; thrown ones land less predictably (they seem
// to bounce), so they're only checked against the player's own hit reports. Throws faster than the 127 u/f the record can hold, and throws from the
// air (ramps, trick snipes), aren't tracked.
// A player who drives through one must get hit, so a no-hit code shows up even though its
// game never reports the collision.

const (
	groundItemRadius    = 80  // Kart center to item, in course units.
	groundItemHeight    = 250 // Allowed height difference.
	bananaGravity       = 1.05
	fakeBoxGravity      = 1.2
	itemRestHeight      = 150 // An item's position settles this far above the floor.
	fakeBoxDropMaxSpeed = 45  // Horizontal spawn speed (u/f) of a dropped (not thrown) box.
	maxThrowHeight      = 400 // Throws from higher above the floor aren't tracked.
	fakeBoxRadius       = 230 // Fake Item Boxes are bigger than Bananas.
	ownItemGrace        = 2 * time.Second
	groundItemLifetime  = 3 * time.Minute
	maxGroundItems      = 64
	// A HitFree reported further than radius+hitReportSlack from the item means the player's
	// own item (a shell) hit it. The report carries the kart's position, not the shell's,
	// so the item can be this far away.
	ownItemHitRadius = 3000
	// A drive-through expectation is dropped if the item turns out to have been destroyed
	// by the player's shell this soon after (the reports can arrive in either order).
	ownItemHitWindow = 1500 * time.Millisecond
	hitReportSlack      = 400 // Position lag between touching an item and the report (a few frames at ~90 u/f).
	droppedDistance     = 600 // Landing this close to the spawn point counts as a drop.
)

type groundItem struct {
	obj   common.ItemObject
	pos   [3]float32
	owner *Aid
	at    time.Time
	// Landed close to where it spawned: dropped behind the thrower, who is still driving
	// away from it at first (thrown items land well ahead).
	dropped bool
	// Only checked against the player's own hit reports (position not precise enough).
	reportOnly bool
}

// ownerGrace reports whether a's touching g is just its owner driving off a fresh drop.
func (g *groundItem) ownerGrace(a *Aid, now time.Time) bool {
	return g.dropped && g.owner == a && now.Sub(g.at) < ownItemGrace
}

// Guarded by mu.
var groundItems []*groundItem

// trackShot starts tracking a dropped Banana or Fake Item Box. Caller must hold mu.
func trackShot(a *Aid, s *records.Shot, now time.Time) {
	if s.Object != common.ObjBanana && s.Object != common.ObjFakeItemBox {
		return
	}
	// The velocity bytes top out at 127: a faster throw's real speed isn't known.
	for _, v := range s.Vel {
		if v >= 127 || v <= -127 {
			return
		}
	}
	c := room.config.Courses[a.course]
	if c == nil || c.Floors == nil {
		return
	}
	// Thrown from the air (off a ramp, trick snipes): the arc isn't predictable enough.
	if h, ok := c.Floors.Height(s.Pos[0], s.Pos[1], s.Pos[2], 2000); !ok || s.Pos[1]-h > maxThrowHeight {
		return
	}
	g, reportOnly := float32(bananaGravity), false
	if s.Object == common.ObjFakeItemBox {
		g = fakeBoxGravity
		// Thrown boxes land within about 300 units of the prediction, sometimes 600 (they
		// seem to bounce): too loose to say someone drove through one, close enough to
		// check a player's own report of hitting one.
		reportOnly = math.Hypot(float64(s.Vel[0]), float64(s.Vel[2])) > fakeBoxDropMaxSpeed
	}
	pos, ok := c.Floors.Landing(s.Pos, s.Vel, g, itemRestHeight)
	if !ok {
		return // Off the course.
	}
	if ty, ok := c.Floors.At([3]float32{pos[0], pos[1] - itemRestHeight, pos[2]}); !ok || !items.ItemsStayPut(ty) {
		return // Ice, water, conveyors: it won't stay there.
	}
	if c.NearBelt([3]float32{pos[0], pos[1] - itemRestHeight, pos[2]}) {
		return // Maybe on a conveyor belt.
	}
	if c.Floors.NearEdge([3]float32{pos[0], pos[1] - itemRestHeight, pos[2]}, edgeMargin) {
		return // At the edge of a drop: it may fall off.
	}
	if len(groundItems) >= maxGroundItems {
		groundItems = groundItems[1:]
	}
	groundItems = append(groundItems, &groundItem{obj: s.Object, pos: pos, owner: a, at: now, reportOnly: reportOnly,
		dropped: items.Distance([3]float32{pos[0], 0, pos[2]}, [3]float32{s.Pos[0], 0, s.Pos[2]}) < droppedDistance})
}

// removeHitItem handles a player reporting they hit an item (EVENT HIT-FREE). The nearest
// tracked item goes away. If the player was right on it, they drove into it and must get
// hit: a code that makes touched items vanish still sends this report, but skips the hit.
// From further away, their own item hit it. Caller must hold mu.
func removeHitItem(a *Aid, obj common.ItemObject, now time.Time) {
	if len(a.players) == 0 {
		return
	}
	p := a.players[0]

	// Already counted as driven through: if this report is from well away from it, the
	// player's own item destroyed it first. Drop that expectation.
	kept := hitExpectations[:0]
	cancelled := false
	for _, e := range hitExpectations {
		if !cancelled && e.item != nil && e.aid == a && e.item.obj == obj && now.Sub(e.at) < ownItemHitWindow &&
			items.Distance(e.item.pos, p.lastPos) > e.item.radius()+hitReportSlack {
			cancelled = true
			logging.Log("Aid %d's own item hit the %v at (%.0f, %.0f, %.0f) before they drove there", a.aid, obj, e.item.pos[0], e.item.pos[1], e.item.pos[2])
			continue
		}
		kept = append(kept, e)
	}
	hitExpectations = kept
	if cancelled {
		return
	}

	best, bestDist := -1, float32(ownItemHitRadius)
	for i, g := range groundItems {
		if g.obj == obj {
			if d := items.Distance(g.pos, p.lastPos); d < bestDist {
				best, bestDist = i, d
			}
		}
	}
	if best < 0 {
		return
	}
	g := groundItems[best]
	groundItems = append(groundItems[:best], groundItems[best+1:]...)
	if bestDist <= g.radius()+hitReportSlack && !p.invincible(now) && !g.ownerGrace(a, now) {
		expectHit(a, p, fmt.Sprintf("reported driving into a %v dropped by aid %d", g.obj, g.owner.aid), now, ramDeadline, false)
	}
}

// checkGroundItems expects a hit on a player whose movement from prev to pos went through
// a tracked item, and removes the item (a real hit destroys it). Caller must hold mu.
func checkGroundItems(a *Aid, p *Player, prev, pos [3]float32, now time.Time) {
	// In the air (a hop or a jump) at either end: it may have gone over the item.
	if c := room.config.Courses[a.course]; c != nil && (airborne(c.Floors, prev) || airborne(c.Floors, pos)) {
		return
	}
	kept := groundItems[:0]
	for _, g := range groundItems {
		if now.Sub(g.at) > groundItemLifetime {
			continue
		}
		hit := !g.reportOnly && !g.ownerGrace(a, now) &&
			math.Abs(float64(g.pos[1]-pos[1])) <= groundItemHeight &&
			pointSegmentDistance(g.pos, prev, pos) <= g.radius()
		if !hit {
			kept = append(kept, g)
			continue
		}
		if !p.invincible(now) {
			e := expectHit(a, p, fmt.Sprintf("drove through a %v dropped by aid %d", g.obj, g.owner.aid), now, ramDeadline, false)
			e.item = g
			logging.Log("Aid %d player %d drove through a %v at (%.0f, %.0f, %.0f)", a.aid, playerIndex(a, p), g.obj, g.pos[0], g.pos[1], g.pos[2])
		}
	}
	groundItems = kept
}

// pointSegmentDistance is the distance from p to the segment a-b, ignoring height.
func pointSegmentDistance(p, a, b [3]float32) float32 {
	ax, az := float64(b[0]-a[0]), float64(b[2]-a[2])
	px, pz := float64(p[0]-a[0]), float64(p[2]-a[2])
	t := 0.0
	if l := ax*ax + az*az; l > 0 {
		t = math.Max(0, math.Min(1, (px*ax+pz*az)/l))
	}
	return float32(math.Hypot(px-t*ax, pz-t*az))
}

// resetGroundItems forgets all items (new race). Caller must hold mu.
func resetGroundItems() {
	groundItems = nil
}

func (g *groundItem) radius() float32 {
	if g.obj == common.ObjFakeItemBox {
		return fakeBoxRadius
	}
	return groundItemRadius
}

// A kart's center is about 50-100 units above the floor when driving.
const airborneHeight = 120

// Items landing this close to a drop aren't tracked.
const edgeMargin = 250

func airborne(f *items.Floors, pos [3]float32) bool {
	if f == nil {
		return false
	}
	h, ok := f.Height(pos[0], pos[1]+50, pos[2], 1000)
	return !ok || pos[1]-h > airborneHeight
}
