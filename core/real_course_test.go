package core

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
	"time"

	"mkw-server/core/records"
	"mkw-server/items"
)

// rdAt builds a RaceData record at pos (optionally respawning).
func rdAt(t *testing.T, pos [3]float32, respawning bool) *records.RaceData {
	t.Helper()
	d := raceData(1)
	var v [3]uint32
	for i, f := range pos {
		v[i] = (math.Float32bits(f+1e6) - 0x3f800000) & 0x0fffffff
	}
	binary.BigEndian.PutUint32(d[0x0:], v[0]<<4|uint32(d[3]&0x0f))
	binary.BigEndian.PutUint32(d[0x3:], binary.BigEndian.Uint32(d[0x3:])&0xf0000000|v[1])
	binary.BigEndian.PutUint32(d[0x7:], v[2]<<4|uint32(d[0xa]&0x0f))
	if respawning {
		d[0x27] = 1 << 6
	}
	rd, err := records.ParseRaceData(d)
	if err != nil {
		t.Fatal(err)
	}
	return rd
}

func mid(cp items.Checkpoint) [2]float32 {
	return [2]float32{(cp.Left[0] + cp.Right[0]) / 2, (cp.Left[1] + cp.Right[1]) / 2}
}

// Drives every race course along its checkpoint centerline (main route) for 3 laps. The
// server must count exactly 3 laps and the respawn check must accept every respawn point.
func TestRealCourseLaps(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	courses, errs := items.LoadCourses(dir)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	for id, c := range courses {
		cps := c.Checkpoints
		if cps == nil {
			continue
		}
		p := newPlayer(0, false)
		// Route: start one checkpoint behind the lap counter, follow Next[0].
		start := cps.List[cps.LapCounter].Prev[0]
		route := []int{start}
		for i := cps.LapCounter; len(route) < 3*len(cps.List)+2; i = cps.List[i].Next[0] {
			route = append(route, i)
			if len(route) > 1 && i == start && len(route) > 3*len(cps.List) {
				break
			}
		}
		laps := 0
		var last *items.Checkpoint
		steps := 0
		for i := 0; laps < 3 && steps < 10000; i = (i + 1) % len(route) {
			steps++
			cp := cps.List[route[i]]
			// Points through the zone between this checkpoint and the next.
			next := cps.List[cp.Next[0]]
			for _, f := range []float32{0.25, 0.5, 0.75} {
				a, b := mid(cp), mid(next)
				pos := [3]float32{a[0] + (b[0]-a[0])*f, 0, a[1] + (b[1]-a[1])*f}
				updateProgress(p, c, rdAt(t, pos, false), uint32(steps))
			}
			laps = p.progress.lap
			last = &cp
		}
		_ = last
		if laps != 3 {
			t.Errorf("course %d: counted %d laps after %d steps", id, laps, steps)
		}
		// Not finished on lap 3 (until the very end of it).
		if p.progress.completion < 3.9 && finishOK(&p.progress) {
			t.Errorf("course %d: finish accepted on lap 3 at %.2f", id, p.progress.completion)
		}

		// Respawn at each checkpoint's own respawn point: accepted.
		bad := 0
		for i, cp := range cps.List {
			if cp.Respawn >= len(c.Respawns) {
				continue
			}
			if problem := checkRespawn(c, i, c.Respawns[cp.Respawn]); problem != "" {
				bad++
			}
		}
		if bad > 0 {
			t.Errorf("course %d: %d respawn points rejected", id, bad)
		}
	}
}

func TestRespawnFarAwayRejected(t *testing.T) {
	c := &items.Course{Respawns: [][3]float32{{0, 0, 0}, {5000, 0, 0}}}
	if problem := checkRespawn(c, -1, [3]float32{100, 300, 100}); problem != "" {
		t.Fatalf("landing at a respawn point rejected: %s", problem)
	}
	if problem := checkRespawn(c, -1, [3]float32{20000, 0, 20000}); problem == "" {
		t.Fatal("landing far from every respawn point accepted")
	}
}

// Real finishes (claimed time, server frame at the line) from a test session.
func TestFinishTimeCalibration(t *testing.T) {
	real := []struct {
		claimed time.Duration
		frame   uint32
	}{
		{3*time.Minute + 45979*time.Millisecond, (3*60+49)*60 + 46}, // 3:49.767
		{2*time.Minute + 3941*time.Millisecond, (2*60+7)*60 + 50},   // 2:07.833
		{1*time.Minute + 16483*time.Millisecond, (1*60+20)*60 + 25}, // 1:20.417
	}
	for _, r := range real {
		if r.claimed < finishEarliest(r.frame) {
			t.Errorf("real finish %v rejected (earliest %v)", r.claimed, finishEarliest(r.frame))
		}
		if fake := r.claimed - 2*time.Second; fake >= finishEarliest(r.frame) {
			t.Errorf("finish 2s faster than real (%v) accepted", fake)
		}
	}
}

// Like TestRealCourseLaps, but positions only land in every third zone, as when a fast kart
// skips thin zones between two updates. Laps must still count.
func TestRealCourseLapsSkippingZones(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	courses, _ := items.LoadCourses(dir)
	for id, c := range courses {
		cps := c.Checkpoints
		if cps == nil {
			continue
		}
		for _, skip := range []int{2, 3} {
			p := newPlayer(0, false)
			i := cps.List[cps.LapCounter].Prev[0]
			for step := 0; p.progress.lap < 3 && step < 10000; step++ {
				cp := cps.List[i]
				a, b := mid(cp), mid(cps.List[cp.Next[0]])
				pos := [3]float32{(a[0] + b[0]) / 2, 0, (a[1] + b[1]) / 2}
				updateProgress(p, c, rdAt(t, pos, false), uint32(step))
				for range skip {
					i = cps.List[i].Next[0]
				}
			}
			if p.progress.lap != 3 {
				t.Errorf("course %d skipping %d zones: counted %d laps", id, skip-1, p.progress.lap)
			}
		}
	}
}

func TestItemRank(t *testing.T) {
	setupRoom(t, DefaultConfig())
	at := func(aid byte, completion float32) *Player {
		a := addTestAid(t, aid, false)
		a.isRacer = true
		p := a.players[0]
		p.progress = newProgress()
		p.progress.tracked, p.progress.completion = true, completion
		p.progressCourse = 8
		return p
	}
	leader := at(0, 2.50)
	pack1 := at(1, 2.10)
	pack2 := at(2, 2.08) // ~1s behind pack1.
	last := at(3, 1.60)

	mu.Lock()
	defer mu.Unlock()
	check := func(p *Player, claimed, want int) {
		t.Helper()
		p.rank = byte(claimed)
		if got, _ := itemRank(p, 8); got != want {
			t.Errorf("claimed %d: got rank %d, want %d", claimed, got, want)
		}
	}
	check(leader, 4, 1) // Can't claim last from a clear lead.
	check(leader, 1, 1)
	check(pack1, 3, 3) // Lag: pack2 is right behind, so 3rd is plausible.
	check(pack1, 4, 3) // But not behind the clear last place.
	check(pack2, 2, 2) // Claiming better is always fine.
	check(last, 4, 4)
	check(last, 0, 4) // No client rank yet: use the plausible worst.
}
