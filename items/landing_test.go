package items

import (
	"os"
	"path/filepath"
	"testing"

	"mkw-server/common"
)

// Throws from a test session on GBA Bowser Castle 3, with where the thrower later drove
// into each Banana.
func TestBananaLanding(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	raw, err := os.ReadFile(filepath.Join(dir, courseFiles[common.RBC3]+".szs"))
	if err != nil {
		t.Skip(err)
	}
	c, err := parseCourse(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range []struct{ pos, vel, hit [3]float32 }{
		{[3]float32{11692, 3888, 20924}, [3]float32{84, 50, 88}, [3]float32{19972, 3662, 29779}},
		{[3]float32{1060, 3788, -26416}, [3]float32{16, 7, 0}, [3]float32{1348, 3665, -26420}},
	} {
		l, ok := c.Floors.Landing(x.pos, x.vel, 1.05, 150)
		if !ok {
			t.Fatalf("throw from %v didn't land", x.pos)
		}
		if d := Distance([3]float32{l[0], 0, l[2]}, [3]float32{x.hit[0], 0, x.hit[2]}); d > 150 {
			t.Errorf("throw from %v landed at %v, %.0f from where it was hit", x.pos, l, d)
		}
	}
}
