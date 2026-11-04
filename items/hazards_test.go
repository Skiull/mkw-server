package items

import (
	"os"
	"testing"
)

func TestBowsersCastleHasGeysers(t *testing.T) {
	szs, err := os.ReadFile("../../host/Course/koopa_course.szs")
	if err != nil {
		t.Skip("no course files")
	}
	c, err := parseCourse(szs)
	if err != nil {
		t.Fatal(err)
	}
	// 14 FlamePole, 3 FlamePole_v, 4 WLfireringGC, 3 koopaFirebar, 7 Thwomps, 1 koopaBall.
	if len(c.Hazards) != 32 {
		t.Fatalf("%d hazards", len(c.Hazards))
	}
	if !c.NearHazard(c.Hazards[0].Pos) {
		t.Fatal("a geyser's own spot isn't near a hazard")
	}
}

func TestThrowLandingStopsAtWalls(t *testing.T) {
	c := &Course{Floors: NewTestFloors(FloorRoad, 100000), Walls: &Walls{cells: map[[2]int32][]int32{}}}
	if _, ok := c.ThrowLanding([3]float32{0, 1000, 0}, [3]float32{0, 5, 30}, 1.05, 150); !ok {
		t.Fatal("no landing without walls")
	}
	// A wall across the path at z=200, both windings (walls are one-sided).
	for _, t := range [][3][3]float32{
		{{-500, -500, 200}, {500, -500, 200}, {0, 2000, 200}},
		{{500, -500, 200}, {-500, -500, 200}, {0, 2000, 200}},
	} {
		c.Walls.add(t)
	}
	if _, ok := c.ThrowLanding([3]float32{0, 1000, 0}, [3]float32{0, 5, 30}, 1.05, 150); ok {
		t.Fatal("landed through a wall")
	}
}
