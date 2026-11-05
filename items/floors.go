package items

// Floors are a course's drivable collision triangles with their KCL type, for finding what
// a player is driving on.
type Floors struct {
	Walls         // Triangles and grid (same layout as walls).
	Types []uint8 // KCL base type of each triangle.
}

// KCL floor types (flag & 0x1f), https://mkwiiki.org/wiki/KCL_flag.
const (
	FloorRoad         = 0x00
	FloorSlippery     = 0x01
	FloorWeakOffroad  = 0x02
	FloorOffroad      = 0x03
	FloorHeavyOffroad = 0x04
	FloorSlippery2    = 0x05
	FloorBoostPanel   = 0x06
	FloorBoostRamp    = 0x07
	FloorJumpPad      = 0x08
	FloorItemRoad     = 0x09
	FloorSolidFall    = 0x0a
	FloorMovingWater  = 0x0b
	FloorFall         = 0x10
	FloorMovingRoad   = 0x15 // Conveyors (Toad's Factory).
	FloorStickyRoad   = 0x16
	FloorRoad2        = 0x17
	FloorRotatingRoad = 0x1d
)

var floorTypes = map[uint16]bool{
	FloorRoad: true, FloorSlippery: true, FloorWeakOffroad: true, FloorOffroad: true,
	FloorHeavyOffroad: true, FloorSlippery2: true, FloorBoostPanel: true, FloorBoostRamp: true,
	FloorJumpPad: true, FloorItemRoad: true, FloorSolidFall: true, FloorMovingWater: true,
	FloorMovingRoad: true, FloorStickyRoad: true, FloorRoad2: true, FloorRotatingRoad: true,
}

// How far above and below a reported position the floor is looked for. Positions are the
// vehicle's center, a little above the ground.
const (
	floorSearchUp   = 150
	floorSearchDown = 200
)

// At returns the type of the floor right under pos, or false if there's none close (in the
// air, or the floor is a moving object rather than course collision).
func (f *Floors) At(pos [3]float32) (uint8, bool) {
	if f == nil {
		return 0, false
	}
	top := [3]float32{pos[0], pos[1] + floorSearchUp, pos[2]}
	bottom := [3]float32{pos[0], pos[1] - floorSearchDown, pos[2]}
	best, found := float32(2), -1
	for _, i := range f.cells[[2]int32{cell(pos[0]), cell(pos[2])}] {
		if d, ok := segmentTriangleDistance(top, bottom, f.Tris[i]); ok && d < best {
			best, found = d, int(i)
		}
	}
	if found < 0 {
		return 0, false
	}
	return f.Types[found], true
}

// segmentTriangleDistance is segmentHitsTriangle returning where along a-b (0..1) it hits.
func segmentTriangleDistance(a, b [3]float32, t [3][3]float32) (float32, bool) {
	const eps = 1e-6
	dir := sub3(b, a)
	e1, e2 := sub3(t[1], t[0]), sub3(t[2], t[0])
	h := cross3(dir, e2)
	det := dot3(e1, h)
	if det > -eps && det < eps {
		return 0, false
	}
	inv := 1 / det
	s := sub3(a, t[0])
	u := inv * dot3(s, h)
	if u < 0 || u > 1 {
		return 0, false
	}
	q := cross3(s, e1)
	v := inv * dot3(dir, q)
	if v < 0 || u+v > 1 {
		return 0, false
	}
	d := inv * dot3(e2, q)
	return d, d >= 0 && d <= 1
}

// NewTestFloors makes a floor of one type covering x/z -size..size at height 0, for tests.
func NewTestFloors(ty uint8, size float32) *Floors {
	f := &Floors{Walls: Walls{cells: make(map[[2]int32][]int32)}}
	a, b, c, d := [3]float32{-size, 0, -size}, [3]float32{size, 0, -size}, [3]float32{size, 0, size}, [3]float32{-size, 0, size}
	f.add([3][3]float32{a, b, c})
	f.add([3][3]float32{a, c, d})
	f.Types = append(f.Types, ty, ty)
	return f
}

// Height returns the height of the floor under (x, z), searching down from y, or false if
// there's none within maxDrop.
func (f *Floors) Height(x, y, z, maxDrop float32) (float32, bool) {
	if f == nil {
		return 0, false
	}
	top := [3]float32{x, y, z}
	bottom := [3]float32{x, y - maxDrop, z}
	best := float32(2)
	for _, i := range f.cells[[2]int32{cell(x), cell(z)}] {
		if d, ok := segmentTriangleDistance(top, bottom, f.Tris[i]); ok && d < best {
			best = d
		}
	}
	if best > 1 {
		return 0, false
	}
	return y - best*maxDrop, true
}
