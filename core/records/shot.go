package records

import (
	"encoding/binary"

	"mkw-server/common"
)

// Shot is a thrown or dropped item, from an EVENT "Shot" slot's data. Decoded from
// captured throws (undocumented): position as int16 x4 at 0x8, velocity as int8 u/f at
// 0x11. Green Shells, Bananas and Fake Item Boxes take 20 bytes; Red Shells and Bob-ombs
// add a byte (likely the target).
type Shot struct {
	Object common.ItemObject
	Pos    [3]float32
	Vel    [3]float32
}

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
		Object: s.Object,
		Pos:    [3]float32{pos(8), pos(10), pos(12)},
		Vel:    [3]float32{float32(int8(d[17])), float32(int8(d[18])), float32(int8(d[19]))},
	}, true
}
