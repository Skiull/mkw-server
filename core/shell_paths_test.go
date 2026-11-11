package core

import (
	"bufio"
	"strings"
	"testing"
	"time"

	"mkw-server/common"
	"mkw-server/core/records"
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
