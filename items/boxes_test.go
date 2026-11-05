package items

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// makeKMP builds a minimal KMP with a GOBJ section holding the given objects.
func makeKMP(objs []struct {
	id       uint16
	pos      [3]float32
	settings [8]uint16
}) []byte {
	const headerLen = 0x4c
	kmp := make([]byte, headerLen)
	copy(kmp, "RKMD")
	binary.BigEndian.PutUint16(kmp[0xa:], headerLen)
	binary.BigEndian.PutUint32(kmp[0x2c:], 0) // GOBJ right after the header.
	sec := make([]byte, 8+len(objs)*gobjEntryLen)
	copy(sec, "GOBJ")
	binary.BigEndian.PutUint16(sec[4:], uint16(len(objs)))
	for i, o := range objs {
		e := sec[8+i*gobjEntryLen:]
		binary.BigEndian.PutUint16(e, o.id)
		for k, v := range o.pos {
			binary.BigEndian.PutUint32(e[4+4*k:], math.Float32bits(v))
		}
		for k, v := range o.settings {
			binary.BigEndian.PutUint16(e[0x2a+2*k:], v)
		}
	}
	return append(kmp, sec...)
}

func TestKMPBoxes(t *testing.T) {
	kmp := makeKMP([]struct {
		id       uint16
		pos      [3]float32
		settings [8]uint16
	}{
		{id: 0x65, pos: [3]float32{100, 200, 300}},
		{id: 0x02, pos: [3]float32{9, 9, 9}}, // Not an item box.
		{id: 0x65, pos: [3]float32{-5, 0, 5}, settings: [8]uint16{0, 12, 0, 0, 0, 6, 0, 0}},
	})
	boxes, err := kmpBoxes(kmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(boxes) != 2 {
		t.Fatalf("got %d boxes, want 2", len(boxes))
	}
	if boxes[0].Pos != [3]float32{100, 200, 300} || boxes[0].Respawn != 2500*time.Millisecond || boxes[0].Special != 0 {
		t.Fatalf("box 0: %+v", boxes[0])
	}
	if boxes[1].Special != 12 || boxes[1].Respawn != 15*time.Second {
		t.Fatalf("box 1: %+v", boxes[1])
	}
	if _, err := kmpBoxes([]byte("nope")); err == nil {
		t.Fatal("accepted a non-KMP file")
	}
}

func TestMovingBoxDistance(t *testing.T) {
	b := Box{Pos: [3]float32{0, 0, 0}, Route: [][3]float32{{0, 0, 0}, {10000, 0, 0}}}
	if d := b.DistanceTo([3]float32{5000, 100, 0}); d > 101 {
		t.Fatalf("halfway along the route: %v", d)
	}
	if d := b.DistanceTo([3]float32{12000, 0, 0}); d < 1999 || d > 2001 {
		t.Fatalf("past the end of the route: %v", d)
	}
}
