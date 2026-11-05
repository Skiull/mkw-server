package items

import (
	"errors"
)

// Checkpoints are a course's lap checkpoints (KMP CKPT, grouped by CKPH). Each checkpoint
// and the one after it form a quadrilateral zone on the ground (x/z); a player is "in" the
// checkpoint while inside its zone. See https://mkwiiki.org/wiki/Checkpoint.
type Checkpoints struct {
	List []Checkpoint
	// Highest key checkpoint number (0 if the course has none).
	MaxKey int
	// Index of the lap counter (type 0) checkpoint.
	LapCounter int
}

type Checkpoint struct {
	Left, Right [2]float32 // x, z
	Respawn     int        // JGPT index.
	// 0: lap counter. 1-254: key checkpoint. -1: normal.
	Type int
	Next []int // Following checkpoints (several after a route split).
	Prev []int
	// Lap completion (0-1) at this checkpoint, and how much one zone is worth.
	Completion float32
	Span       float32
}

func (c *Checkpoint) IsKey() bool { return c.Type > 0 }

// kmpCheckpoints reads CKPT and CKPH. Returns nil for courses without checkpoints (battle).
func kmpCheckpoints(kmp []byte) (*Checkpoints, error) {
	ckpt, count, err := kmpSection(kmp, kmpCKPTOffset, "CKPT", 0x14)
	if err != nil {
		return nil, err
	}
	ckph, groups, err := kmpSection(kmp, kmpCKPHOffset, "CKPH", 0x10)
	if err != nil {
		return nil, err
	}
	if count == 0 || groups == 0 {
		return nil, nil
	}

	cps := &Checkpoints{List: make([]Checkpoint, count), LapCounter: -1}
	for i := range cps.List {
		e := ckpt[i*0x14:]
		c := &cps.List[i]
		c.Left = [2]float32{f32(e), f32(e[4:])}
		c.Right = [2]float32{f32(e[8:]), f32(e[12:])}
		c.Respawn = int(e[0x10])
		if e[0x11] == 0xff {
			c.Type = -1
		} else {
			c.Type = int(e[0x11])
		}
		if c.Type == 0 && cps.LapCounter < 0 {
			cps.LapCounter = i
		}
		if c.Type > cps.MaxKey {
			cps.MaxKey = c.Type
		}
	}

	type group struct {
		start, length int
		next          []int
	}
	gs := make([]group, groups)
	for i := range gs {
		e := ckph[i*0x10:]
		gs[i] = group{start: int(e[0]), length: int(e[1])}
		for _, n := range e[8:14] {
			if n != 0xff && int(n) < groups {
				gs[i].next = append(gs[i].next, int(n))
			}
		}
		if gs[i].length == 0 || gs[i].start+gs[i].length > count {
			return nil, errors.New("CKPH group out of range")
		}
	}

	// Links: next within a group, or the first checkpoint of each following group.
	for _, g := range gs {
		for k := range g.length {
			i := g.start + k
			if k+1 < g.length {
				cps.link(i, i+1)
				continue
			}
			for _, ng := range g.next {
				cps.link(i, gs[ng].start)
			}
		}
	}

	// Lap completion, like the game: groups are laid out by depth from the first group, each
	// depth is an equal share of the lap, split evenly over a group's checkpoints.
	depth := make([]int, groups)
	for i := range depth {
		depth[i] = -1
	}
	depth[0] = 0
	queue := []int{0}
	maxDepth := 0
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		for _, n := range gs[g].next {
			if depth[n] < 0 {
				depth[n] = depth[g] + 1
				maxDepth = max(maxDepth, depth[n])
				queue = append(queue, n)
			}
		}
	}
	depths := float32(maxDepth + 1)
	for gi, g := range gs {
		d := max(depth[gi], 0)
		span := 1 / (depths * float32(g.length))
		for k := range g.length {
			c := &cps.List[g.start+k]
			c.Span = span
			c.Completion = (float32(d) + float32(k)/float32(g.length)) / depths
		}
	}
	return cps, nil
}

func (cps *Checkpoints) link(from, to int) {
	cps.List[from].Next = append(cps.List[from].Next, to)
	cps.List[to].Prev = append(cps.List[to].Prev, from)
}

// cross is the 2D cross product (b-a) x (p-a) on the x/z plane.
func cross(a, b, p [2]float32) float32 {
	return (b[0]-a[0])*(p[1]-a[1]) - (b[1]-a[1])*(p[0]-a[0])
}

// inZone reports whether p (x, z) is inside the zone between checkpoint i and next j, and
// how far through it (0 at i, 1 at j).
func (cps *Checkpoints) inZone(i, j int, p [2]float32) (float32, bool) {
	a, b := &cps.List[i], &cps.List[j]
	quad := [4][2]float32{a.Left, a.Right, b.Right, b.Left}
	// Convex quad: p is inside if it's on the same side of every edge.
	var sign float32
	for k := range 4 {
		c := cross(quad[k], quad[(k+1)%4], p)
		if c == 0 {
			continue
		}
		if sign == 0 {
			sign = c
		} else if (c > 0) != (sign > 0) {
			return 0, false
		}
	}
	// Distance to each checkpoint line gives how far through the zone p is.
	da := abs32(cross(a.Left, a.Right, p)) / length2(a.Left, a.Right)
	db := abs32(cross(b.Left, b.Right, p)) / length2(b.Left, b.Right)
	if da+db == 0 {
		return 0, true
	}
	return da / (da + db), true
}

// Locate finds the checkpoint zone p is in, looking near hint first (the player's last
// checkpoint, or -1), the way the game does. Returns the checkpoint and how far through
// its zone p is.
func (cps *Checkpoints) Locate(p [2]float32, hint int) (int, float32, bool) {
	if hint >= 0 && hint < len(cps.List) {
		// The current zone, then a few zones forward and back.
		seen := map[int]bool{}
		frontier := []int{hint}
		for range 4 {
			var next []int
			for _, i := range frontier {
				if seen[i] {
					continue
				}
				seen[i] = true
				for _, j := range cps.List[i].Next {
					if t, ok := cps.inZone(i, j, p); ok {
						return i, t, true
					}
				}
				next = append(next, cps.List[i].Next...)
				next = append(next, cps.List[i].Prev...)
			}
			frontier = next
		}
	}
	for i := range cps.List {
		for _, j := range cps.List[i].Next {
			if t, ok := cps.inZone(i, j, p); ok {
				return i, t, true
			}
		}
	}
	return -1, 0, false
}

func abs32(f float32) float32 {
	if f < 0 {
		return -f
	}
	return f
}

func length2(a, b [2]float32) float32 {
	dx, dz := b[0]-a[0], b[1]-a[1]
	l := dx*dx + dz*dz
	if l == 0 {
		return 1
	}
	return sqrt32(l)
}
