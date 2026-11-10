package core

import (
	"math"
	"testing"
)

// A straight line along z and a player run beside it (x = 100) that covers the middle
// stretch in fewer frames.
func TestSplice(t *testing.T) {
	var base []lineFrame
	for i := 0; i < 300; i++ {
		base = append(base, lineFrame{Frame: uint32(i), Pos: [3]float32{0, 0, float32(i) * 10}, RaceCompletion: 1 + float32(i)/300})
	}
	run := &humanRun{}
	for i := 0; i < 200; i++ {
		run.samples = append(run.samples, humanSample{frame: float64(i), pos: [3]float32{100, 0, float32(i) * 20}, btn: 0x9})
	}
	bs := hlBoundaries(base)
	if len(bs) < 2 {
		t.Fatalf("boundaries: %d", len(bs))
	}
	cross := crossings(func(i int) [3]float32 { return run.samples[i].pos }, len(run.samples), bs)
	if cross[0] < 0 || cross[1] < 0 {
		t.Fatalf("run doesn't cross: %v", cross)
	}
	c := hlCandidate{from: bs[0].idx, to: bs[1].idx, run: run, sFrom: cross[0], sTo: cross[1]}
	out := splice(base, []hlCandidate{c})
	// 90 base frames replaced by the run's 45.
	if want := len(base) - (c.to - c.from) + 45; len(out) != want {
		t.Fatalf("spliced length %d, want %d", len(out), want)
	}
	for i, f := range out {
		if f.Frame != uint32(i) {
			t.Fatalf("frame %d numbered %d", i, f.Frame)
		}
		if i > 0 && f.RaceCompletion < out[i-1].RaceCompletion {
			t.Fatalf("race completion goes back at %d", i)
		}
	}
	// Eased onto the line at both ends, the run's own path in the middle.
	first, mid, last := out[c.from], out[c.from+22], out[c.from+44]
	if math.Abs(float64(first.Pos[0])) > 1 || math.Abs(float64(last.Pos[0]-100*1/20.0)) > 10 {
		t.Errorf("ends not on the line: %v %v", first.Pos, last.Pos)
	}
	if mid.Pos[0] < 90 || mid.Buttons != 0x9 {
		t.Errorf("middle isn't the run: %v buttons %x", mid.Pos, mid.Buttons)
	}
}
