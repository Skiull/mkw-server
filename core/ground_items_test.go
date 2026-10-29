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
	trackShot(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 5000}, Vel: [3]float32{50, 40, 127}}, now)
	// Thrown from high in the air: not tracked.
	trackShot(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 900, 5000}, Vel: [3]float32{0, 40, 70}}, now)
	// Dropped in place: tracked.
	trackShot(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{-3, 7, -1}}, now)
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
	trackShot(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{1000, 0, 0}}, time.Now())
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
	trackShot(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{1000, 0, 0}}, now)
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
		trackShot(owner, &records.Shot{Object: obj, Pos: [3]float32{0, 0, 0}}, now)
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
	trackShot(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{0, 0, 0}}, now)
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
	trackShot(owner, &records.Shot{Object: common.ObjBanana, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{0, 30, 100}}, now)
	if len(groundItems) != 1 || groundItems[0].dropped {
		t.Fatal("thrown Banana not tracked as thrown")
	}
	g := groundItems[0].pos
	checkGroundItems(owner, p, [3]float32{g[0], 0, g[2] - 100}, [3]float32{g[0], 0, g[2] + 100}, now.Add(time.Second))
	if len(hitExpectations) != 1 {
		t.Fatal("owner driving into their thrown Banana not checked")
	}
}

func TestThrownFakeItemBoxOnlyChecksReports(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	now := time.Now()
	trackShot(owner, &records.Shot{Object: common.ObjFakeItemBox, Pos: [3]float32{0, 0, 0}, Vel: [3]float32{0, 50, 100}}, now)
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
