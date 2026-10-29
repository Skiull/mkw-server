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
	if s.Vel != [3]float32{84, 50, 90} {
		t.Fatalf("velocity %v", s.Vel)
	}
}
