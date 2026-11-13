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

// GOBJ objects with collision the game makes trickable (Kinoko's object code calls
// setCurrentCollisionTrickable for these), by object id:
var trickObjectIDs = map[uint16]bool{
	0x1f5: true, // KinokoUd: Mushroom Gorge's up-down mushrooms
	0x1f6: true, // KinokoBend: Mushroom Gorge's bending mushrooms
	0x1fa: true, // KinokoNm: Mushroom Gorge's normal mushrooms
	0x1fd: true, // FlamePole: Bowser's Castle's fire pillar bumps
	0x1fe: true, // TwistedWay: Bowser's Castle's twisting road
	0x204: true, // Aurora: Rainbow Road's aurora
	0x208: true, // DCPillar: Dry Dry Ruins' fallen pillars
	0x20e: true, // Ami
}

// How far (horizontally) from a trick object's origin a trick still counts as off it.
const trickObjectReach = 3500

// TrickObject is a trickable object's placement.
type TrickObject struct {
	ID   uint16
	Pos  [3]float32
	RotY float32 // Degrees.
}

// Rainbow Road's aurora (wavy road) is a strip from its origin along its local z axis:
// |x| <= 2000, 0 <= z <= 15000 (Kinoko's ObjectAurora COLLISION_SIZE).
const (
	auroraHalfWidth = 2000
	auroraLength    = 15000
	auroraMargin    = 1000
	// Bowser's Castle's twisting road: centered on its origin, |x| <= 4000, |z| <= 7500
	// (Kinoko's ObjectTwistedWay WIDTH*2 and HALF_DEPTH).
	twistedHalfWidth = 4000
	twistedHalfDepth = 7500
)

func kmpTrickObjects(kmp []byte) ([]TrickObject, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	var out []TrickObject
	for i := 0; i < count; i++ {
		e := data[i*gobjEntryLen:]
		id := binary.BigEndian.Uint16(e)
		if !trickObjectIDs[id] {
			continue
		}
		o := TrickObject{ID: id, RotY: math.Float32frombits(binary.BigEndian.Uint32(e[0x14:]))}
		for k := range o.Pos {
			o.Pos[k] = math.Float32frombits(binary.BigEndian.Uint32(e[4+k*4:]))
		}
		out = append(out, o)
	}
	return out, nil
}

// NearTrickObject reports whether pos is (horizontally) on or near an object players trick off.
func (c *Course) NearTrickObject(pos [3]float32) bool {
	for _, o := range c.TrickObjects {
		dx, dz := float64(pos[0]-o.Pos[0]), float64(pos[2]-o.Pos[2])
		// Into the object's frame (rotation about y), for the long wavy roads.
		r := float64(o.RotY) * math.Pi / 180
		lx := dx*math.Cos(r) - dz*math.Sin(r)
		lz := dx*math.Sin(r) + dz*math.Cos(r)
		switch o.ID {
		case 0x204:
			if math.Abs(lx) <= auroraHalfWidth+auroraMargin && lz >= -auroraMargin && lz <= auroraLength+auroraMargin {
				return true
			}
			continue
		case 0x1fe:
			if math.Abs(lx) <= twistedHalfWidth+auroraMargin && math.Abs(lz) <= twistedHalfDepth+auroraMargin {
				return true
			}
			// Also counts by distance below (its ends).
		}
		if dx*dx+dz*dz <= trickObjectReach*trickObjectReach {
			return true
		}
	}
	return false
}
