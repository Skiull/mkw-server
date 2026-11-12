package core

import (
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
)

// flatCourse is a course with a flat road at height 0, for landing items.
func flatCourse() Config {
	return Config{Courses: map[common.Course]*items.Course{
		common.MarioCircuit: {Floors: items.NewTestFloors(items.FloorRoad, 100000)},
	}}
}

func TestDroppedBananaExpectsHit(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	driver := addTestAid(t, 1, false)
	now := time.Now()

	// Too fast for the record (clamped at 127): not tracked.
	trackShotSeen(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 5000}, Vel: [3]float32{100, 40, 254}}, now)
	// Thrown from high in the air: not tracked.
	trackShotSeen(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 900, 5000}, Vel: [3]float32{0, 40, 140}}, now)
	// Dropped in place: tracked.
	trackShotSeen(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{-6, 7, -2}}, now)
	if len(groundItems) != 1 {
		t.Fatalf("tracking %d items, want 1", len(groundItems))
	}

	// Its owner driving away right after dropping it: no hit expected.
	checkGroundItems(owner, owner.players[0], [3]float32{0, 0, -50}, [3]float32{0, 0, 50}, now)
	if len(hitExpectations) != 0 || len(groundItems) != 1 {
		t.Fatal("owner hit their own fresh drop")
	}

	// Someone else driving straight through it between two packets.
	later := now.Add(5 * time.Second)
	checkGroundItems(driver, driver.players[0], [3]float32{-200, 0, 30}, [3]float32{200, 0, 30}, later)
	if len(hitExpectations) != 1 || len(groundItems) != 0 {
		t.Fatalf("%d expectations and %d items, want 1 and 0", len(hitExpectations), len(groundItems))
	}
}

func TestHitReportRemovesItem(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	trackShotSeen(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{1000, 0, 0}}, time.Now())
	// Reported from 650 away: the reporter's own item hit it. Removed, no hit expected.
	other.players[0].lastPos = [3]float32{1650, 0, 0}
	removeHitItem(other, common.ObjFakeItemBox, time.Now())
	if len(groundItems) != 0 || len(hitExpectations) != 0 {
		t.Fatalf("%d items, %d expectations, want 0 and 0", len(groundItems), len(hitExpectations))
	}
}

func TestDrivingIntoVanishingItemExpectsHit(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	now := time.Now()
	trackShotSeen(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{1000, 0, 0}}, now)
	other.players[0].lastPos = [3]float32{1050, 0, 0}
	other.players[0].move.lastSpeed = 90
	removeHitItem(other, common.ObjBanana, now)
	if len(groundItems) != 0 || len(hitExpectations) != 1 {
		t.Fatalf("%d items, %d expectations, want 0 and 1", len(groundItems), len(hitExpectations))
	}
	if missed := resolveHits(now.Add(2 * time.Second)); len(missed) != 1 {
		t.Fatal("driving into a Banana without a hit not logged")
	}
}

func TestSlowPassGetsNoVerdict(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	p := owner.players[0]
	p.move.lastSpeed = 20 // Turned around, driving slowly back into it.
	now := time.Now()
	for _, obj := range []common.ItemObject{common.ObjFakeItemBox, common.ObjBanana} {
		trackShotSeen(owner, &records.Shot{Object: obj, Pos: [3]float32{0, 0, 0}}, now)
		later := now.Add(10 * time.Second)
		checkGroundItems(owner, p, [3]float32{-100, 0, 0}, [3]float32{100, 0, 0}, later)
		if missed := resolveHits(later.Add(2 * time.Second)); len(missed) != 0 {
			t.Fatalf("slow %v drive-through logged", obj)
		}
		now = later.Add(3 * time.Second)
	}
}

func TestFakeItemBoxHitBySlowdown(t *testing.T) {
	// A real Fake Item Box hit doesn't flip its hit flag; the slowdown shows it.
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	p := other.players[0]
	p.move.lastSpeed = 97
	now := time.Now()
	trackShotSeen(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{0, 0, 0}}, now)
	checkGroundItems(other, p, [3]float32{-100, 0, 0}, [3]float32{100, 0, 0}, now)
	p.move.lastSpeed = 7
	if missed := resolveHits(now.Add(2 * time.Second)); len(missed) != 0 {
		t.Fatal("real Fake Item Box hit logged as no-hit")
	}
}

func TestOwnerHitsOwnThrownBanana(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	p := owner.players[0]
	p.move.lastSpeed = 90
	now := time.Now()
	// Thrown forward: lands far ahead. Driving into it a second later is a real hit.
	trackShotSeen(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{0, 30, 200}}, now)
	if len(groundItems) != 1 || groundItems[0].dropped {
		t.Fatal("thrown Banana not tracked as thrown")
	}
	g := groundItems[0].pos
	// Overtaking a fresh toss isn't checked (own-item grace)...
	checkGroundItems(owner, p, [3]float32{g[0], 0, g[2] - 100}, [3]float32{g[0], 0, g[2] + 100}, now.Add(time.Second))
	if len(hitExpectations) != 0 || len(groundItems) != 1 {
		t.Fatal("owner overtaking their fresh Banana was checked")
	}
	// ...driving into it later is.
	checkGroundItems(owner, p, [3]float32{g[0], 0, g[2] - 100}, [3]float32{g[0], 0, g[2] + 100}, now.Add(3*time.Second))
	if len(hitExpectations) != 1 {
		t.Fatal("owner driving into their thrown Banana not checked")
	}
}

func TestThrownFakeItemBoxOnlyChecksReports(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	now := time.Now()
	trackShotSeen(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{0, 30, 180}}, now)
	if len(groundItems) != 1 || !groundItems[0].reportOnly {
		t.Fatal("thrown Fake Item Box not tracked for reports")
	}
	g := groundItems[0].pos
	later := now.Add(5 * time.Second)
	checkGroundItems(other, other.players[0], [3]float32{g[0] - 100, 0, g[2]}, [3]float32{g[0] + 100, 0, g[2]}, later)
	if len(hitExpectations) != 0 {
		t.Fatal("driving past a thrown Fake Item Box expected a hit")
	}
	other.players[0].lastPos = [3]float32{g[0] + 300, 0, g[2]}
	removeHitItem(other, common.ObjFakeItemBox, later)
	if len(hitExpectations) != 1 {
		t.Fatal("report of hitting a thrown Fake Item Box not checked")
	}
}

// A player's shell destroys their Banana, then they drive over its spot. The shell-hit
// report can arrive just after the drive-through, or just before it from far away.
func TestOwnShellDestroyedBanana(t *testing.T) {
	for _, reportFirst := range []bool{false, true} {
		setupRoom(t, flatCourse())
		a := addTestAid(t, 0, false)
		now := time.Now()
		trackShotSeen(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{-6, 7, -2}}, now)
		later := now.Add(3 * time.Second)
		report := func() {
			a.players[0].lastPos = [3]float32{0, 0, -860} // The kart, well behind the Banana.
			removeHitItem(a, common.ObjBanana, later)
		}
		if reportFirst {
			report()
		}
		checkGroundItems(a, a.players[0], [3]float32{-200, 0, 30}, [3]float32{200, 0, 30}, later)
		if !reportFirst {
			report()
		}
		if len(hitExpectations) != 0 {
			t.Errorf("reportFirst=%v: %d hit expectations, want 0", reportFirst, len(hitExpectations))
		}
	}
}

// Throwing a Banana forward and running into it a moment later destroys it far from where
// it was predicted to land; driving over that spot later isn't a no-hit.
func TestOwnFreshThrowHitIsRemoved(t *testing.T) {
	setupRoom(t, flatCourse())
	a := addTestAid(t, 0, false)
	now := time.Now()
	trackShotSeen(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{0, 10, 80}}, now)
	if len(groundItems) != 1 {
		t.Fatalf("%d tracked", len(groundItems))
	}
	a.players[0].lastPos = [3]float32{0, 0, -9000} // Nowhere near the predicted landing.
	removeHitItem(a, common.ObjBanana, now.Add(time.Second))
	if len(groundItems) != 0 {
		t.Fatal("fresh own Banana still tracked after a hit on it")
	}
}

// Items aren't tracked where the server can't be sure they stay: near objects that destroy
// them, or after a long throw.
func TestUntrackedNearHazardsAndLongThrows(t *testing.T) {
	cfg := flatCourse()
	cfg.Courses[common.MarioCircuit].Hazards = []items.Hazard{{Pos: [3]float32{0, 0, 2000}, Reach: 1000}}
	setupRoom(t, cfg)
	a := addTestAid(t, 0, false)
	now := time.Now()
	trackShotSeen(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 1800}, Vel: [3]float32{-6, 7, -2}}, now)
	if len(groundItems) != 0 {
		t.Fatal("tracked a Banana dropped next to a geyser")
	}
	trackShotSeen(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, -9000}, Vel: [3]float32{0, 40, -260}}, now)
	if len(groundItems) != 0 {
		t.Fatal("tracked a Banana thrown far")
	}
	trackShotSeen(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, -6000}, Vel: [3]float32{-6, 7, -2}}, now)
	if len(groundItems) != 1 {
		t.Fatalf("%d tracked, want the plain drop", len(groundItems))
	}
}

// trackShotSeen tracks a throw that the thrower's packets told every player about.
func trackShotSeen(a *Aid, s *records.Shot, now time.Time) {
	n := len(groundItems)
	trackShot(a, s, nil, 0, 0, now)
	if len(groundItems) > n || (n == maxGroundItems && len(groundItems) == n) {
		groundItems[len(groundItems)-1].seenBy = 0x7fff
	}
}

// A Banana whose throw was left out of the victim's packets isn't expected to hit them.
func TestGroundItemNotSent(t *testing.T) {
	setupRoom(t, Config{})
	owner := addTestAid(t, 0, false)
	victim := addTestAid(t, 1, false)
	now := time.Now()
	g := &groundItem{obj: common.ObjBanana, owner: owner, at: now, seenBy: 1 << 2}
	if g.sentTo(victim, now) {
		t.Fatal("item counted as sent to an aid its packets skipped")
	}
	g.seenBy |= 1 << victim.aid
	if !g.sentTo(victim, now) {
		t.Fatal("item not counted as sent")
	}
}

// A shell reported destroyed near a Banana someone just "drove through": the shell likely
// took the Banana out first, so no kick.
func TestShellHitCancelsDriveThrough(t *testing.T) {
	setupRoom(t, Config{KickNoHit: true})
	owner := addTestAid(t, 1, false)
	driver := addTestAid(t, 0, false)
	now := time.Now()
	g := &groundItem{obj: common.ObjBanana, pos: [3]float32{4715, 9968, -37693}, owner: owner, at: now.Add(-time.Minute), seenBy: 0x7fff}
	groundItems = []*groundItem{g}
	p := driver.players[0]
	p.move.lastSpeed = 83
	e := expectHit(driver, p, "drove through a Banana", now, ramDeadline, false)
	e.item = g
	owner.players[0].lastPos = [3]float32{2993, 9854, -38950}
	removeHitItem(owner, common.ObjGreenShell, now.Add(time.Second))
	resolveHits(now.Add(3 * time.Second))
	if driver.kicked {
		t.Fatal("kicked for a Banana a shell had destroyed")
	}
}

// A Banana thrown from high up (untracked) near a tracked one: the tracked one is no longer
// checked on drive-throughs (the untracked one may have hit it).
func TestUntrackedThrowMakesNearbyReportOnly(t *testing.T) {
	setupRoom(t, Config{})
	a := addTestAid(t, 0, false)
	g := &groundItem{obj: common.ObjBanana, pos: [3]float32{1000, 0, 1000}, owner: a, at: time.Now()}
	groundItems = []*groundItem{g}
	untrackedThrow(a, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 3000, 0}})
	if !g.reportOnly {
		t.Fatal("nearby tracked item still checked on drive-throughs")
	}
}
