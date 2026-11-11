package records

import (
	"encoding/hex"
	"testing"

	"mkw-server/common"
)

func TestShotDecode(t *testing.T) {
	// Captured: Used Mushroom then Shot Banana, thrower at (12744, 3695, 22028).
	slots, _ := hex.DecodeString("234210101010101010101010101010101010101010101010")
	data, _ := hex.DecodeString("22c00c2374c007060600220c7b03cc158b00000054325a")
	e, err := ParseEvent(append(slots, data...))
	if err != nil {
		t.Fatal(err)
	}
	s, ok := e.ShotAt(1)
	if !ok || s.Object != common.ObjBanana {
		t.Fatalf("got %+v %v", s, ok)
	}
	if s.Pos[0] < 12600 || s.Pos[0] > 12900 || s.Pos[2] < 21900 || s.Pos[2] > 22200 {
		t.Fatalf("position %v, want near (12744, _, 22028)", s.Pos)
	}
	if s.Vel != [3]float32{168, 50, 180} {
		t.Fatalf("velocity %v", s.Vel)
	}
	if s.Frame != 0x2374 || s.Owner != 0 || s.Counter != 7 {
		t.Fatalf("header frame %d owner %d counter %d", s.Frame, s.Owner, s.Counter)
	}
}

func TestItemReportDecode(t *testing.T) {
	// Slot 0: HitFree Green Shell, frame 0x1234, owner 1, counter 5, destroyed.
	slots := make([]byte, EventSlotCount)
	slots[0] = byte(common.EventHitFree)<<5 | byte(common.ObjGreenShell)
	for i := 1; i < EventSlotCount; i++ {
		slots[i] = 0x10 // Empty slot with the "none" object.
	}
	e, err := ParseEvent(append(slots, 0x12, 0x34, 0xC1, 5, 0xC1))
	if err != nil {
		t.Fatal(err)
	}
	r, ok := e.ItemReportAt(0)
	if !ok || r.Frame != 0x1234 || r.Owner != 1 || r.Counter != 5 || r.Code != ItemReportDestroyed {
		t.Fatalf("got %+v %v", r, ok)
	}
}
