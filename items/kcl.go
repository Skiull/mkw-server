package items

import (
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// Walls are a course's solid-for-players collision triangles, read from course.kcl, with a
// grid over x/z so a movement step only tests the triangles near it.
// Format: https://mkwiiki.org/wiki/KCL_(File_Format), flags: https://mkwiiki.org/wiki/KCL_flag.
type Walls struct {
	Tris  [][3][3]float32
	Flags []uint16     // Full KCL flag of each triangle.
	Norms [][3]float32 // Face normal of each triangle (points out of the solid side).
	cells map[[2]int32][]int32
}

const wallCellSize = 512

// KCL base types (flag & 0x1f) that stop players.
var playerWallTypes = map[uint16]bool{
	0x0c: true, // Wall
	0x0d: true, // Invisible wall
	0x0f: true, // Wall 2
	0x14: true, // Player-only wall
	0x1f: true, // Invisible wall 2
}

func vec3(b []byte) [3]float32 {
	return [3]float32{f32(b), f32(b[4:]), f32(b[8:])}
}

func parseWalls(kcl []byte) (*Walls, error) {
	w, _, err := parseKCL(kcl)
	return w, err
}

// parseKCL reads the player walls and the floors (offroad and the rest) of a course.
func parseKCL(kcl []byte) (*Walls, *Floors, error) {
	if len(kcl) < 0x38 {
		return nil, nil, errors.New("KCL file is too small")
	}
	posOff := int(binary.BigEndian.Uint32(kcl[0x0:]))
	nrmOff := int(binary.BigEndian.Uint32(kcl[0x4:]))
	prismOff := int(binary.BigEndian.Uint32(kcl[0x8:])) + 0x10 // Stored 1-indexed.
	blockOff := int(binary.BigEndian.Uint32(kcl[0xc:]))

	// The prism count isn't stored: it runs until the next section or the end of the file.
	end := len(kcl)
	offs := []int{posOff, nrmOff, blockOff, len(kcl)}
	sort.Ints(offs)
	for _, o := range offs {
		if o > prismOff {
			end = o
			break
		}
	}
	if prismOff < 0 || prismOff > end || end > len(kcl) {
		return nil, nil, errors.New("bad prism section")
	}

	w := &Walls{cells: make(map[[2]int32][]int32)}
	f := &Floors{Walls: Walls{cells: make(map[[2]int32][]int32)}}
	for p := prismOff; p+0x10 <= end; p += 0x10 {
		e := kcl[p:]
		flag := binary.BigEndian.Uint16(e[0xe:])
		isWall := playerWallTypes[flag&0x1f]
		if !isWall && !floorTypes[flag&0x1f] {
			continue
		}
		height := f32(e)
		idx := func(o int) int { return int(binary.BigEndian.Uint16(e[o:])) }
		at := func(base, i int) ([3]float32, bool) {
			o := base + i*12
			if o < 0 || o+12 > len(kcl) {
				return [3]float32{}, false
			}
			return vec3(kcl[o:]), true
		}
		pos, ok1 := at(posOff, idx(4))
		fn, ok2 := at(nrmOff, idx(6))
		ea, ok3 := at(nrmOff, idx(8))
		eb, ok4 := at(nrmOff, idx(10))
		ec, ok5 := at(nrmOff, idx(12))
		if !(ok1 && ok2 && ok3 && ok4 && ok5) {
			continue
		}
		// Decoding from the community docs (Mario Kart 7's parseModel).
		crossA, crossB := cross3(ea, fn), cross3(eb, fn)
		da, db := dot3(crossA, ec), dot3(crossB, ec)
		if da == 0 || db == 0 {
			continue
		}
		v2 := add3(pos, scale3(crossB, height/db))
		v3 := add3(pos, scale3(crossA, height/da))
		tri := [3][3]float32{pos, v2, v3}
		if isWall {
			w.add(tri)
			w.Flags = append(w.Flags, flag)
			w.Norms = append(w.Norms, fn)
		} else {
			f.add(tri)
			f.Types = append(f.Types, uint8(flag&0x1f))
		}
	}
	return w, f, nil
}

func (w *Walls) add(t [3][3]float32) {
	i := int32(len(w.Tris))
	w.Tris = append(w.Tris, t)
	minX := min(t[0][0], t[1][0], t[2][0])
	maxX := max(t[0][0], t[1][0], t[2][0])
	minZ := min(t[0][2], t[1][2], t[2][2])
	maxZ := max(t[0][2], t[1][2], t[2][2])
	for cx := cell(minX); cx <= cell(maxX); cx++ {
		for cz := cell(minZ); cz <= cell(maxZ); cz++ {
			k := [2]int32{cx, cz}
			w.cells[k] = append(w.cells[k], i)
		}
	}
}

func cell(v float32) int32 {
	return int32(math.Floor(float64(v / wallCellSize)))
}

// Crosses reports whether moving straight from a to b passes through a wall from its front
// side, and which.
func (w *Walls) Crosses(a, b [3]float32) (int, bool) {
	if w == nil {
		return -1, false
	}
	seen := map[int32]bool{}
	for cx := cell(min(a[0], b[0])); cx <= cell(max(a[0], b[0])); cx++ {
		for cz := cell(min(a[2], b[2])); cz <= cell(max(a[2], b[2])); cz++ {
			for _, i := range w.cells[[2]int32{cx, cz}] {
				if seen[i] {
					continue
				}
				seen[i] = true
				// Collision only works from a wall's front side (the way its normal points);
				// from behind, players pass through, like one-way walls and ceilings.
				if int(i) < len(w.Norms) && dot3(sub3(b, a), w.Norms[i]) >= 0 {
					continue
				}
				if !segmentHitsTriangle(a, b, w.Tris[i]) {
					continue
				}
				return int(i), true
			}
		}
	}
	return -1, false
}

// segmentHitsTriangle is the Möller-Trumbore test limited to the segment a-b.
func segmentHitsTriangle(a, b [3]float32, t [3][3]float32) bool {
	const eps = 1e-6
	dir := sub3(b, a)
	e1, e2 := sub3(t[1], t[0]), sub3(t[2], t[0])
	h := cross3(dir, e2)
	det := dot3(e1, h)
	if det > -eps && det < eps {
		return false
	}
	f := 1 / det
	s := sub3(a, t[0])
	u := f * dot3(s, h)
	if u < 0 || u > 1 {
		return false
	}
	q := cross3(s, e1)
	v := f * dot3(dir, q)
	if v < 0 || u+v > 1 {
		return false
	}
	d := f * dot3(e2, q)
	return d > 0 && d < 1
}

func sub3(a, b [3]float32) [3]float32 { return [3]float32{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func add3(a, b [3]float32) [3]float32 { return [3]float32{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func scale3(a [3]float32, s float32) [3]float32 {
	return [3]float32{a[0] * s, a[1] * s, a[2] * s}
}
func dot3(a, b [3]float32) float32 { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross3(a, b [3]float32) [3]float32 {
	return [3]float32{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func sqrt32(f float32) float32 { return float32(math.Sqrt(float64(f))) }
