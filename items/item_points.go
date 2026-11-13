package items

import (
	"encoding/binary"
	"math"
)

// Item routes (KMP ITPT points grouped by ITPH) are what Bullet Bills and Red Shells follow.
// A point's setting 2 is a set of decimal-digit flags; a ones digit of 1 is DONT_DROP: a
// Bullet Bill heading to that point can't end there (used over pits and through cannons).
// https://mkwiiki.org/wiki/KMP_(File_Format), https://mkwiiki.org/wiki/ITPT_Settings

// How far (horizontally) from a no-drop segment a player still counts as on it. Item routes
// run down the middle of the road; a bill can steer about 10 units per unit of a point's
// width, so this is generous.
const noDropReach = 3000

// kmpNoDropSegments returns the item route segments whose end point is DONT_DROP.
func kmpNoDropSegments(kmp []byte) ([][2][3]float32, error) {
	itpt, count, err := kmpSection(kmp, kmpITPTOffset, "ITPT", 0x14)
	if err != nil {
		return nil, err
	}
	itph, groups, err := kmpSection(kmp, kmpITPHOffset, "ITPH", 0x10)
	if err != nil {
		return nil, err
	}
	pos := func(i int) [3]float32 {
		e := itpt[i*0x14:]
		var p [3]float32
		for k := range p {
			p[k] = math.Float32frombits(binary.BigEndian.Uint32(e[k*4:]))
		}
		return p
	}
	dontDrop := func(i int) bool { return binary.BigEndian.Uint16(itpt[i*0x14+0x12:])%10 == 1 }

	var segs [][2][3]float32
	add := func(from, to int) {
		if from >= 0 && to >= 0 && from < count && to < count && dontDrop(to) {
			segs = append(segs, [2][3]float32{pos(from), pos(to)})
		}
	}
	for g := 0; g < groups; g++ {
		e := itph[g*0x10:]
		start, n := int(e[0]), int(e[1])
		for i := start + 1; i < start+n; i++ {
			add(i-1, i)
		}
		// Into the next groups' first points.
		for _, next := range e[8:14] {
			if next != 0xff && int(next) < groups {
				add(start+n-1, int(itph[int(next)*0x10]))
			}
		}
	}
	// A lone DONT_DROP point (a route of one) still counts, as a zero-length segment.
	for i := 0; i < count; i++ {
		if dontDrop(i) {
			segs = append(segs, [2][3]float32{pos(i), pos(i)})
		}
	}
	return segs, nil
}

// InNoDropZone reports whether pos is (horizontally) near an item route segment where a
// Bullet Bill can't end.
func (c *Course) InNoDropZone(pos [3]float32) bool {
	for _, s := range c.NoDropSegments {
		if horizDistToSegment(pos, s[0], s[1]) <= noDropReach {
			return true
		}
	}
	return false
}

func horizDistToSegment(p, a, b [3]float32) float32 {
	ax, az := float64(b[0]-a[0]), float64(b[2]-a[2])
	px, pz := float64(p[0]-a[0]), float64(p[2]-a[2])
	t := 0.0
	if l := ax*ax + az*az; l > 0 {
		t = math.Max(0, math.Min(1, (px*ax+pz*az)/l))
	}
	dx, dz := px-t*ax, pz-t*az
	return float32(math.Hypot(dx, dz))
}
