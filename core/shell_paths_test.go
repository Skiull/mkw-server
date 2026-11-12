package core

import (
	"bufio"
	"os"
	"strings"
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
	"mkw-server/items"
)

func TestParseKinokoItems(t *testing.T) {
	out := "s 3 10.000 20.000 30.000 24.80 27.20 9 0\ns 4 15.000 20.000 35.000 25.73 28.22 9 0\ns end wall 2\n"
	points, end, err := parseKinokoItems(bufio.NewScanner(strings.NewReader(out)))
	if err != nil || len(points) != 2 || end != "wall" {
		t.Fatalf("got %v %q %v", points, end, err)
	}
	if points[1].pos != [3]float32{15, 20, 35} || points[0].hitRadius != 27.2 {
		t.Fatalf("points %+v", points)
	}
}

func TestShellPathExpectsHit(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	victim := addTestAid(t, 1, false)
	now := time.Now()

	// A shell going straight along z at 100 u/f from z=0.
	var points []shellPoint
	for i := range 120 {
		points = append(points, shellPoint{pos: [3]float32{0, 0, float32(i * 100)}, hitRadius: 40})
	}
	s := &shellPath{owner: owner, idx: 0, counter: 3, at: now, points: points, cut: len(points), expected: map[*Player]time.Time{}}
	shellPaths = []*shellPath{s}

	// Half a second in (frame 30) the shell is at z=3000: a player there should be hit.
	p := victim.players[0]
	p.lastPos = [3]float32{50, 0, 3000}
	checkShells(victim, p, now.Add(500*time.Millisecond))
	if len(hitExpectations) != 1 || !hitExpectations[0].logOnly || hitExpectations[0].shell != s {
		t.Fatalf("%d expectations, want 1 log-only from the shell", len(hitExpectations))
	}
	// The owner is never expected to be hit by its own shell.
	checkShells(owner, owner.players[0], now.Add(500*time.Millisecond))
	if len(hitExpectations) != 1 {
		t.Fatal("owner expected to be hit by its own shell")
	}

	// The victim reports the hit: the expectation is met and the path stops there.
	shellReport(victim, &records.ItemReport{Object: common.ObjGreenShell, Frame: 30, Owner: 0, Counter: 3, Code: records.ItemReportHit}, now)
	if !hitExpectations[0].reported || s.cut != 31 {
		t.Fatalf("reported %v, cut %d", hitExpectations[0].reported, s.cut)
	}
	if missed := resolveHits(now.Add(time.Minute)); len(missed) != 0 {
		t.Fatal("reported hit counted as missed")
	}

	// Another player there later: the shell is gone.
	checkShells(victim, p, now.Add(time.Second))
	if len(shellPaths) != 0 {
		t.Fatal("shell path kept after its cut")
	}
}

func TestShellDestroyedOnlyByOwner(t *testing.T) {
	setupRoom(t, flatCourse())
	owner := addTestAid(t, 0, false)
	other := addTestAid(t, 1, false)
	s := &shellPath{owner: owner, counter: 1, points: make([]shellPoint, 100), cut: 100, expected: map[*Player]time.Time{}}
	shellPaths = []*shellPath{s}
	r := &records.ItemReport{Object: common.ObjGreenShell, Frame: 20, Counter: 1, Code: records.ItemReportDestroyed}
	shellReport(other, r, time.Now())
	if s.cut != 100 {
		t.Fatal("another player's destroyed report cut the path")
	}
	shellReport(owner, r, time.Now())
	if s.cut != 21 {
		t.Fatalf("cut %d, want 21", s.cut)
	}
}

// A Banana dropped on course 28 (11-11, untracked by the fit: "at an edge")
// lands in Kinoko. Needs MKW_KINOKO_DIR (the AI bot folder with kinoko.exe).
func TestKinokoBananaLands(t *testing.T) {
	dir := os.Getenv("MKW_KINOKO_DIR")
	if dir == "" {
		t.Skip("MKW_KINOKO_DIR not set")
	}
	// Shot: owner 0, dropped, at (20336, 8216, -17044), moving (-2, 7, 8).
	data := []byte{0, 0, 0xc0, 0, 0, 0, 0, 0, 0x13, 0xdc, 0x08, 0x06, 0xef, 0x5b, 1, 0xf9, 0xfc, 0xff, 7, 4}
	points, end, err := runKinokoItems(dir, 28, data, common.ObjBanana)
	if err != nil || end != "landed" {
		t.Fatalf("end %q, err %v", end, err)
	}
	p := points[len(points)-1]
	t.Logf("landed after %d frames at (%.0f, %.0f, %.0f)", len(points), p.pos[0], p.pos[1], p.pos[2])
	if items.Distance(p.pos, [3]float32{20336, 8216, -17044}) > 600 {
		t.Fatal("a drop landed far from where it was dropped")
	}
}
