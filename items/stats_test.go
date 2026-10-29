package items

import (
	"os"
	"path/filepath"
	"testing"

	"mkw-server/common"
)

func TestRealStats(t *testing.T) {
	dir := os.Getenv("MKW_COURSE_DIR")
	if dir == "" {
		t.Skip("MKW_COURSE_DIR not set")
	}
	s, err := LoadStats(filepath.Join(dir, "..", "Common.szs"))
	if err != nil {
		t.Skip(err)
	}
	for _, c := range []struct {
		v    common.Vehicle
		c    common.Character
		ty   uint8
		want float32
	}{
		{common.Magikruiser, common.Toadette, FloorOffroad, 0.675 + 0.037},
		{common.FlameRunner, common.FunkyKong, FloorOffroad, 0.380},
		{common.Magikruiser, common.Toadette, FloorRoad, 1},
	} {
		if got := s.FloorSpeed(c.v, c.c, c.ty); got < c.want-0.01 || got > c.want+0.02 {
			t.Errorf("%v + %v on floor %#x: %.3f, want about %.3f", c.v, c.c, c.ty, got, c.want)
		}
	}
	// Mega scales run 1.55-2.70 by vehicle (kartParam.bin), shocked scales 0.40-0.70.
	lo, hi := float32(10), float32(0)
	for v := range common.Vehicle(36) {
		m := s.MegaScale(v)
		lo, hi = min(lo, m), max(hi, m)
		if sh := s.ShockScale(v); sh < 0.39 || sh > 0.71 {
			t.Errorf("vehicle %v shocked scale %.2f out of range", v, sh)
		}
	}
	if lo < 1.5 || hi > 2.75 || hi < 2.6 {
		t.Errorf("Mega scales %.2f-%.2f, want about 1.55-2.70", lo, hi)
	}
}
