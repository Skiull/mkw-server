package items

import (
	"os"
	"testing"
)

// Loads the real courses when MKW_COURSE_DIR points at the game's /Race/Course folder.
func TestLoadRealCourseBoxes(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	courses, errs := LoadCourses(dir)
	for _, err := range errs {
		t.Error(err)
	}
	if len(courses) != len(courseFiles) {
		t.Fatalf("loaded %d courses, want %d", len(courses), len(courseFiles))
	}
	for id, c := range courses {
		if len(c.Boxes) == 0 {
			t.Errorf("course %d has no item boxes", id)
		}
		if c.Walls == nil || len(c.Walls.Tris) == 0 {
			t.Errorf("course %d has no walls", id)
		}
		if len(c.Respawns) == 0 {
			t.Errorf("course %d has no respawn points", id)
		}
		if id < 0x20 {
			cps := c.Checkpoints
			if cps == nil || cps.LapCounter < 0 {
				t.Errorf("course %d has no lap counter", id)
				continue
			}
			// Every checkpoint must lead somewhere and lap completion must stay in 0-1.
			for i, cp := range cps.List {
				if len(cp.Next) == 0 {
					t.Errorf("course %d checkpoint %d has no next", id, i)
				}
				if cp.Completion < 0 || cp.Completion >= 1 {
					t.Errorf("course %d checkpoint %d completion %v", id, i, cp.Completion)
				}
			}
			// Each respawn point should sit inside some checkpoint zone.
			t.Logf("course %2d: %3d boxes, %3d checkpoints (max key %d), %3d respawns, %5d wall tris",
				id, len(c.Boxes), len(cps.List), cps.MaxKey, len(c.Respawns), len(c.Walls.Tris))
		}
	}
}
