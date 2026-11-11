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
// with constant horizontal speed and 2.22 u/f^2 gravity and settles 150 units above the
// floor (fitted to 125 logged throw/hit pairs, 10-28 to 11-10: median 136 units from where
// they were hit, 90% within 363). Fake Item Boxes use 2.5 u/f^2; thrown ones land less predictably (they seem
// to bounce), so they're only checked against the player's own hit reports. Throws faster than the 127 u/f the record can hold, and throws from the
// air (ramps, trick snipes), aren't tracked.
// A player who drives through one must get hit, so a no-hit code shows up even though its
// game never reports the collision.

const (
	groundItemRadius    = 60  // Kart center to item, in course units.
	groundItemHeight    = 250 // Allowed height difference.
	bananaGravity       = 2.22
	fakeBoxGravity      = 2.5
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
	// A hit reported on an item the player threw this recently removes it wherever it is.
	ownThrowHitWindow = 3 * time.Second
	hitReportSlack    = 400  // Position lag between touching an item and the report (a few frames at ~90 u/f).
	droppedDistance   = 600  // Landing this close to the spawn point counts as a drop.
	maxThrowDistance  = 6000 // Items landing further from where they were thrown aren't tracked.
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
	// The throw's EVENT slot, and the aids the owner's packets told about it (relayed.go).
	slot   int
	raw    byte
	seenBy uint16
}

// ownerGrace reports whether a's touching g is just its owner driving off a fresh drop.
func (g *groundItem) ownerGrace(a *Aid, now time.Time) bool {
	// Any own fresh item: dropped ones are driven off, and forward tosses slower than the
	// kart get overtaken right away (Shy Guy Beach 11-03 23:57: tossed at 32 u/f, driving 84).
	return g.owner == a && now.Sub(g.at) < ownItemGrace
}

// Guarded by mu.
var groundItems []*groundItem

// trackShot starts tracking a dropped Banana or Fake Item Box. Caller must hold mu.
func trackShot(a *Aid, s *records.Shot, slot int, raw byte, now time.Time) {
	if s.Object != common.ObjBanana && s.Object != common.ObjFakeItemBox {
		return
	}
	n := len(groundItems)
	trackShotLanding(a, s, slot, raw, now)
	if len(groundItems) == n || (n == maxGroundItems && groundItems[len(groundItems)-1].at != now) {
		untrackedThrow(a, s)
	}
}

// untrackedThrow handles a Banana or Fake Item Box thrown where its landing can't be
// predicted (from the air, too fast, onto a hazard...). It can still land on a tracked item
// and take it out unreported (11-10: a Banana thrown off a Thwomp did), so tracked items it
// could reach are only checked against players' own hit reports from now on.
func untrackedThrow(a *Aid, s *records.Shot) {
	for _, g := range groundItems {
		if !g.reportOnly && items.Distance(g.pos, s.Pos) < maxThrowDistance {
			g.reportOnly = true
			logging.Log("Aid %d's untracked %v throw may reach aid %d's %v at (%.0f, %.0f, %.0f): report-only now",
				a.aid, s.Object, g.owner.aid, g.obj, g.pos[0], g.pos[1], g.pos[2])
		}
	}
}

// trackShotLanding predicts where a throw lands and tracks it there, if it can.
func trackShotLanding(a *Aid, s *records.Shot, slot int, raw byte, now time.Time) {
	// The velocity bytes top out at 127 (x and z are packed halved): a faster throw's real
	// speed isn't known.
	if v := s.Vel; abs32(v[1]) >= 127 || abs32(v[0]) >= 254 || abs32(v[2]) >= 254 {
		return
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
		g, reportOnly = fakeBoxGravity, true
		// Boxes land within about 300 units of the prediction, sometimes 600 (they seem to
		// bounce), dropped ones too (11-10: a dropped box counted as driven through when the
		// player passed beside it a lap later): too loose to say someone drove through one,
		// close enough to check a player's own report of hitting one.
	}
	pos, ok := c.ThrowLanding(s.Pos, s.Vel, g, itemRestHeight)
	if !ok {
		return // Off the course, or it hits a wall on the way and bounces.
	}
	if items.Distance([3]float32{pos[0], 0, pos[2]}, [3]float32{s.Pos[0], 0, s.Pos[2]}) > maxThrowDistance {
		return // Thrown far: the longer the arc, the less sure where it ends up.
	}
	if c.NearHazard(pos) {
		return // A geyser, firebar, Thwomp, car... may destroy it unseen.
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
	groundItems = append(groundItems, &groundItem{obj: s.Object, pos: pos, owner: a, at: now, reportOnly: reportOnly, slot: slot, raw: raw,
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
	if obj == common.ObjGreenShell || obj == common.ObjRedShell {
		shellHitGroundItem(a, p.lastPos, now)
	}

	best, bestDist := -1, float32(ownItemHitRadius)
	for i, g := range groundItems {
		if g.obj == obj {
			if d := items.Distance(g.pos, p.lastPos); d < bestDist {
				best, bestDist = i, d
			}
		}
	}
	// The player's own item, thrown moments ago, is likely still in the air (or just landed)
	// nowhere near the predicted spot: a hit on it right after the throw removes it. The hit
	// was an item running into it or the player catching it, not driving over it later.
	if best < 0 {
		for i := len(groundItems) - 1; i >= 0; i-- {
			if g := groundItems[i]; g.obj == obj && g.owner == a && now.Sub(g.at) < ownThrowHitWindow {
				groundItems = append(groundItems[:i], groundItems[i+1:]...)
				logging.Log("Aid %d hit its own fresh %v: no longer tracked", a.aid, obj)
				return
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
		hit := !g.reportOnly && !g.ownerGrace(a, now) && g.sentTo(a, now) &&
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

// shellHitGroundItem handles a shell reported destroyed (HIT-FREE on a Green or Red Shell).
// The report carries the reporter's kart position, not the shell's, and the item the shell
// ran into may be reported only as the shell: so the nearest tracked Banana or Fake Item Box
// within ownItemHitRadius may be gone. A drive-through counted on it moments ago is dropped
// (seen 11-10: a Green Shell took out a Banana a second before another player drove there,
// and only the shell was reported, after). Caller must hold mu.
func shellHitGroundItem(reporter *Aid, at [3]float32, now time.Time) {
	kept := hitExpectations[:0]
	for _, e := range hitExpectations {
		if e.item != nil && now.Sub(e.at) < ownItemHitWindow && items.Distance(e.item.pos, at) < ownItemHitRadius {
			logging.Log("Aid %d: the %v it drove through at (%.0f, %.0f, %.0f) was likely hit by aid %d's shell first",
				e.aid.aid, e.item.obj, e.item.pos[0], e.item.pos[1], e.item.pos[2], reporter.aid)
			continue
		}
		kept = append(kept, e)
	}
	hitExpectations = kept

	best, bestDist := -1, float32(ownItemHitRadius)
	for i, g := range groundItems {
		if d := items.Distance(g.pos, at); d < bestDist {
			best, bestDist = i, d
		}
	}
	if best >= 0 {
		g := groundItems[best]
		groundItems = append(groundItems[:best], groundItems[best+1:]...)
		logging.Log("Aid %d's shell was hit %.0f units from aid %d's %v: no longer tracked", reporter.aid, bestDist, g.owner.aid, g.obj)
	}
}
