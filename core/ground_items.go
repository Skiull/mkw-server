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
//
// With Kinoko (the AI bot folder), every throw is also simulated with the game's own Banana /
// Fake Item Box movement (kinoko/docs/thrown-items.md): it flies until its first floor contact
// and stays there. The simulated spot replaces the fitted one. Throws the fit can't place
// (from the air, far, off a wall, near an edge) are tracked from the simulation alone, log
// only until the logs show how often it's right.
//
// Bob-ombs (simulation only, log only): they bounce, then sit on their fuse (at most 120-180
// frames). A kart other than the thrower's within 1044 units (36 x their 29-unit hit radius)
// sets it off within 4 frames, and the blast grows to about that size: a player driving close
// to a sitting Bob-omb is expected to get hit. Tracked Bananas and boxes in its blast become
// report-only (it may have destroyed them).

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
	maxThrowDistance  = 12000 // Items landing further from where they were thrown aren't tracked (forward tosses land ~9000 ahead).
	// Bob-ombs: closer than this to a sitting one, a kart sets it off and is caught in the
	// blast (trigger 1044; kept well inside it so a kart leaving the blast isn't expected).
	bombTriggerRadius = 700
	bombBlastRadius   = 1100
	bombFuse          = 3*time.Second + 500*time.Millisecond // Longest fuse (180 frames) and the blast.
	frameTime         = time.Second / 60
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
	// Placed by the simulation only: a drive-through is logged, never kicked.
	logOnly bool
	// Bob-omb: sitting on its fuse between these times.
	armedAt, until time.Time
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
var (
	groundItems []*groundItem
	groundRace  int // Bumped on every reset, so late simulations from a past race are dropped.
)

// trackShot starts tracking a dropped Banana or Fake Item Box. Caller must hold mu.
// data is the Shot slot's 20 bytes (nil if not available).
func trackShot(a *Aid, s *records.Shot, data []byte, slot int, raw byte, now time.Time) {
	if s.Object == common.ObjBobOmb {
		if room.config.AIBotDir != "" && len(data) >= 20 {
			simulateGroundItem(a, s, data, slot, raw, now, nil, "Bob-omb not simulated")
		}
		return
	}
	if s.Object != common.ObjBanana && s.Object != common.ObjFakeItemBox {
		return
	}
	n := len(groundItems)
	why := trackShotLanding(a, s, slot, raw, now)
	var fitted *groundItem
	if why == "" && len(groundItems) > 0 && (len(groundItems) > n || groundItems[len(groundItems)-1].at == now) {
		fitted = groundItems[len(groundItems)-1]
	}
	if room.config.AIBotDir != "" && len(data) >= 20 {
		simulateGroundItem(a, s, data, slot, raw, now, fitted, why)
		return
	}
	if why != "" {
		logging.Log("Aid %d's %v not tracked: %s", a.aid, s.Object, why)
		untrackedThrow(a, s)
	}
}

// simulateGroundItem runs the throw in Kinoko and tracks the item where it lands. fitted is
// the item already tracked from the fit (nil if the fit couldn't place it, for reason why).
// Caller must hold mu.
func simulateGroundItem(a *Aid, s *records.Shot, data []byte, slot int, raw byte, now time.Time, fitted *groundItem, why string) {
	dir, course, race := room.config.AIBotDir, a.course, groundRace
	shot := *s
	go func() {
		points, end, err := runKinokoItems(dir, course, data, shot.Object)
		mu.Lock()
		defer mu.Unlock()
		if race != groundRace {
			return
		}
		if err != nil || end != "landed" {
			if err == nil {
				err = fmt.Errorf("ends %s", end)
			}
			logging.Log("Aid %d %v simulation: %v", a.aid, shot.Object, err)
			if fitted == nil && shot.Object != common.ObjBobOmb {
				logging.Log("Aid %d's %v not tracked: %s", a.aid, shot.Object, why)
				untrackedThrow(a, &shot)
			}
			return
		}
		last := points[len(points)-1]
		// The simulation gives the item's center; tracked positions are at rest height.
		pos := last.pos
		pos[1] += itemRestHeight - last.radius
		if fitted != nil {
			for _, g := range groundItems {
				if g == fitted {
					logging.Log("Aid %d's %v lands at (%.0f, %.0f, %.0f) after %d frames, %.0f from the fit",
						a.aid, shot.Object, pos[0], pos[1], pos[2], len(points), items.Distance(pos, g.pos))
					g.pos = pos
					return
				}
			}
			return // Already hit or gone.
		}
		c := room.config.Courses[course]
		if c == nil || c.Floors == nil {
			return
		}
		if shot.Object == common.ObjBobOmb {
			bombLanded(a, pos)
		}
		if unstable := restUnstable(c, pos); unstable != "" {
			logging.Log("Aid %d's %v not tracked: lands %s", a.aid, shot.Object, unstable)
			if shot.Object != common.ObjBobOmb {
				untrackedThrow(a, &shot)
			}
			return
		}
		if len(groundItems) >= maxGroundItems {
			groundItems = groundItems[1:]
		}
		g := &groundItem{obj: shot.Object, pos: pos, owner: a, at: now, slot: slot, raw: raw,
			reportOnly: shot.Object == common.ObjFakeItemBox, logOnly: true,
			dropped: items.Distance([3]float32{pos[0], 0, pos[2]}, [3]float32{shot.Pos[0], 0, shot.Pos[2]}) < droppedDistance}
		if shot.Object == common.ObjBobOmb {
			g.armedAt = now.Add(time.Duration(len(points)) * frameTime)
			g.until = g.armedAt.Add(bombFuse)
		}
		groundItems = append(groundItems, g)
		logging.Log("Aid %d's %v lands at (%.0f, %.0f, %.0f) after %d frames (simulation only: log only)",
			a.aid, shot.Object, pos[0], pos[1], pos[2], len(points))
	}()
}

// bombLanded makes tracked items in a Bob-omb's blast report-only. Caller must hold mu.
func bombLanded(a *Aid, pos [3]float32) {
	for _, g := range groundItems {
		if g.obj != common.ObjBobOmb && !g.reportOnly && items.Distance(g.pos, pos) < bombBlastRadius {
			g.reportOnly = true
			logging.Log("Aid %d's Bob-omb lands by aid %d's %v: report-only now", a.aid, g.owner.aid, g.obj)
		}
	}
}

// restUnstable says why an item resting at pos may not stay there ("" if it will).
func restUnstable(c *items.Course, pos [3]float32) string {
	floor := [3]float32{pos[0], pos[1] - itemRestHeight, pos[2]}
	if c.NearHazard(pos) {
		return "near a hazard" // A geyser, firebar, Thwomp, car... may destroy it unseen.
	}
	if ty, ok := c.Floors.At(floor); !ok || !items.ItemsStayPut(ty) {
		return "on a floor where items move" // Ice, water, conveyors.
	}
	if c.NearBelt(floor) {
		return "near a conveyor"
	}
	return ""
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
func trackShotLanding(a *Aid, s *records.Shot, slot int, raw byte, now time.Time) (skipped string) {
	// The velocity bytes top out at 127 (x and z are packed halved): a faster throw's real
	// speed isn't known.
	if v := s.Vel; abs32(v[1]) >= 127 || abs32(v[0]) >= 254 || abs32(v[2]) >= 254 {
		return "too fast for the record"
	}
	c := room.config.Courses[a.course]
	if c == nil || c.Floors == nil {
		return "no course floors"
	}
	// Thrown from the air (off a ramp, trick snipes): the arc isn't predictable enough.
	if h, ok := c.Floors.Height(s.Pos[0], s.Pos[1], s.Pos[2], 2000); !ok || s.Pos[1]-h > maxThrowHeight {
		return "thrown from the air"
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
		return "landing not found (off the course or a wall)" // Off the course, or it hits a wall on the way and bounces.
	}
	if items.Distance([3]float32{pos[0], 0, pos[2]}, [3]float32{s.Pos[0], 0, s.Pos[2]}) > maxThrowDistance {
		return "landing too far" // Thrown far: the longer the arc, the less sure where it ends up.
	}
	if unstable := restUnstable(c, pos); unstable != "" {
		return unstable
	}
	if c.Floors.NearEdge([3]float32{pos[0], pos[1] - itemRestHeight, pos[2]}, edgeMargin) {
		return "at an edge" // At the edge of a drop: it may fall off.
	}
	if len(groundItems) >= maxGroundItems {
		groundItems = groundItems[1:]
	}
	groundItems = append(groundItems, &groundItem{obj: s.Object, pos: pos, owner: a, at: now, reportOnly: reportOnly, slot: slot, raw: raw,
		dropped: items.Distance([3]float32{pos[0], 0, pos[2]}, [3]float32{s.Pos[0], 0, s.Pos[2]}) < droppedDistance})
	return ""
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
		if now.Sub(g.at) > groundItemLifetime || (!g.until.IsZero() && now.After(g.until)) {
			continue
		}
		bomb := g.obj == common.ObjBobOmb
		if bomb && (g.owner == a || now.Before(g.armedAt)) {
			kept = append(kept, g) // The thrower doesn't set it off; still in flight.
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
			what := "drove through"
			if bomb {
				what = "drove up to"
			}
			e := expectHit(a, p, fmt.Sprintf("%s a %v dropped by aid %d", what, g.obj, g.owner.aid), now, ramDeadline, false)
			e.item = g
			e.logOnly = g.logOnly
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
	groundRace++
}

func (g *groundItem) radius() float32 {
	switch g.obj {
	case common.ObjFakeItemBox:
		return fakeBoxRadius
	case common.ObjBobOmb:
		return bombTriggerRadius
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
