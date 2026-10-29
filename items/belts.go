package items

import "encoding/binary"

// Conveyor belt objects (GOBJ 0x25a BeltEasy, 0x25b BeltCrossing, 0x25c BeltCurveA,
// 0x25d BeltCurveB) from Toad's Factory. Their collision is the object's model, not the
// course KCL, so only their positions are known.
func kmpBelts(kmp []byte) ([][3]float32, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	var belts [][3]float32
	for i := range count {
		e := data[i*gobjEntryLen:]
		if id := binary.BigEndian.Uint16(e); id >= 0x25a && id <= 0x25d {
			belts = append(belts, vec3(e[4:]))
		}
	}
	return belts, nil
}

// NearBelt reports whether pos may be on a conveyor belt: within reach of a belt object,
// at about its height. The belts are long, and their shapes aren't known.
func (c *Course) NearBelt(pos [3]float32) bool {
	for _, b := range c.Belts {
		dx, dz := pos[0]-b[0], pos[2]-b[2]
		if dx*dx+dz*dz < beltReach*beltReach && pos[1]-b[1] < beltHeight && b[1]-pos[1] < beltHeight {
			return true
		}
	}
	return false
}

const (
	beltReach  = 20000
	beltHeight = 400
)
