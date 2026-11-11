package records

import (
	"encoding/binary"

	"mkw-server/common"
)

// Shot is a thrown or dropped item, from an EVENT "Shot" slot's data (the game's serializer,
// see kinoko/docs/thrown-items.md): [0..2) race frame of the throw, [2] 0xC0 | owner,
// [3] owner's item counter, [4..8) item flags, [8..14) position as int16 x4, [14..17)
// previous position minus position, [17..20) velocity as int8 (x/2, y, z/2) u/f. Green
// Shells, Bananas and Fake Item Boxes take 20 bytes; Red Shells and Bob-ombs add a byte
// (likely the target).
type Shot struct {
	Object  common.ItemObject
	Frame   uint16 // Race frame of the throw.
	Owner   byte   // Player index on the thrower's console.
	Counter byte   // The owner's item counter; EVENT action-4 slots name the item by it.
	Pos     [3]float32
	Vel     [3]float32
	// Green Shell dropped behind: bit 1 of the velocity y byte, the speed is negative.
	Reverse bool
}

// ItemReport is an EVENT action-4 (HitFree) slot: [frame u16][owner][item counter][code].
type ItemReport struct {
	Object  common.ItemObject
	Frame   uint16
	Owner   byte // The item's owner (low bits; the shot header has 0xC0 | owner).
	Counter byte
	Code    byte
}

const (
	ItemReportDestroyed = 0xC1 // The owner's game says the item was destroyed.
	ItemReportHit       = 0x02 // The sender says the item hit them.
)

const shotLen = 20

// shotSize is the data size of a Shot slot for an item, or 0 if unknown.
func shotSize(obj common.ItemObject) int {
	switch obj {
	case common.ObjGreenShell, common.ObjBanana, common.ObjFakeItemBox:
		return shotLen
	case common.ObjRedShell, common.ObjBobOmb:
		return shotLen + 1
	}
	return 0
}

// SlotData returns slot i's data, if every slot before it has a known size.
func (e *Event) SlotData(i int) ([]byte, bool) {
	off := 0
	for j, s := range e.Slots {
		size := s.Action.EventDataSize()
		if s.Action == common.EventShot {
			size = shotSize(s.Object)
			if size == 0 {
				return nil, false
			}
		}
		if size < 0 {
			return nil, false
		}
		if j == i {
			if off+size > len(e.Data) {
				return nil, false
			}
			return e.Data[off : off+size], true
		}
		off += size
	}
	return nil, false
}

// ShotAt decodes slot i as a Shot.
func (e *Event) ShotAt(i int) (*Shot, bool) {
	s := e.Slots[i]
	if s.Action != common.EventShot {
		return nil, false
	}
	d, ok := e.SlotData(i)
	if !ok || len(d) < shotLen {
		return nil, false
	}
	pos := func(o int) float32 { return float32(int16(binary.BigEndian.Uint16(d[o:]))) * 4 }
	return &Shot{
		Object:  s.Object,
		Frame:   binary.BigEndian.Uint16(d),
		Owner:   d[2] &^ 0xC0,
		Counter: d[3],
		Pos:     [3]float32{pos(8), pos(10), pos(12)},
		// x and z are packed halved (clamped to +-255 first).
		Vel:     [3]float32{float32(int8(d[17])) * 2, float32(int8(d[18])), float32(int8(d[19])) * 2},
		Reverse: d[18]&2 != 0,
	}, true
}

// ItemReportAt decodes slot i as an ItemReport (action 4).
func (e *Event) ItemReportAt(i int) (*ItemReport, bool) {
	s := e.Slots[i]
	if s.Action != common.EventHitFree {
		return nil, false
	}
	d, ok := e.SlotData(i)
	if !ok || len(d) < 5 {
		return nil, false
	}
	return &ItemReport{Object: s.Object, Frame: binary.BigEndian.Uint16(d), Owner: d[2] &^ 0xC0, Counter: d[3], Code: d[4]}, true
}
