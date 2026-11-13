package items

import (
	"encoding/binary"
	"math"
)

// Trickable surfaces (https://mkwiiki.org/wiki/KCL_flag): boost ramps (0x07), jump pads
// (0x08) and half-pipe ramps (0x13) always are; any other drivable surface is when its
// collision effect bit 0 (flag 0x2000) is set. Tricks off objects (bouncy mushrooms,
// cannons' landings and so on) aren't in course.kcl, so this only covers course collision.

const trickableFlag = 0x2000

const floorHalfPipe = 0x13

func isTrickable(flag uint16) bool {
	switch flag & 0x1f {
	case FloorBoostRamp, FloorJumpPad, floorHalfPipe:
		return true
	}
	return flag&trickableFlag != 0 && floorTypes[flag&0x1f]
}

// NearTrickable reports whether pos is over (or right beside) a trickable surface: within
// reach horizontally, with the surface at most above over pos or below under it.
func (f *Floors) NearTrickable(pos [3]float32, reach, above, below float32) bool {
	if f == nil || f.Trick == nil {
		return false
	}
	for _, d := range [][2]float32{{0, 0}, {reach, 0}, {-reach, 0}, {0, reach}, {0, -reach}} {
		p := [3]float32{pos[0] + d[0], pos[1], pos[2] + d[1]}
		top := [3]float32{p[0], p[1] + above, p[2]}
		bottom := [3]float32{p[0], p[1] - below, p[2]}
		for _, i := range f.Trick.cells[[2]int32{cell(p[0]), cell(p[2])}] {
			if segmentHitsTriangle(top, bottom, f.Trick.Tris[i]) {
				return true
			}
		}
	}
	return false
}

// GOBJ objects with collision players trick off, found from recorded races: Bowser's Castle's
// bumps (0x1fd, 0x1fe) and Dry Dry Ruins' ramps (0x208).
var trickObjectIDs = map[uint16]bool{0x1fd: true, 0x1fe: true, 0x208: true}

// How far (horizontally) from a trick object's origin a trick still counts as off it.
const trickObjectReach = 3500

func kmpTrickObjects(kmp []byte) ([][3]float32, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	var out [][3]float32
	for i := 0; i < count; i++ {
		e := data[i*gobjEntryLen:]
		if !trickObjectIDs[binary.BigEndian.Uint16(e)] {
			continue
		}
		var p [3]float32
		for k := range p {
			p[k] = math.Float32frombits(binary.BigEndian.Uint32(e[4+k*4:]))
		}
		out = append(out, p)
	}
	return out, nil
}

// NearTrickObject reports whether pos is (horizontally) near an object players trick off.
func (c *Course) NearTrickObject(pos [3]float32) bool {
	for _, o := range c.TrickObjects {
		dx, dz := pos[0]-o[0], pos[2]-o[2]
		if dx*dx+dz*dz <= trickObjectReach*trickObjectReach {
			return true
		}
	}
	return false
}
