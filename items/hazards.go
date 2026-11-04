package items

import "encoding/binary"

// Course objects that destroy (or carry off) items lying on the track: fire geysers and
// firebars (Bowser's Castle), Thwomps, Chain Chomps, Piranha Plants, Goombas, moving cars and
// carts, and the like. An item that lands near one may be gone before anyone drives over its
// spot, without any player's game reporting it, so the server doesn't hold players to it.
// Ids and names: https://wiki.tockdom.com/wiki/Object.

// Reach of each hazard object (course units, at scale 1) around its spot or its route.
var hazardReach = map[uint16]float32{
	0x0cc: 900,  // CarA1 (Coconut Mall cars, on routes)
	0x0e7: 900,  // CarA2
	0x0e8: 900,  // CarA3
	0x0cd: 700,  // basabasa (Swoops)
	0x0cf: 1500, // koopaBall (Bowser's Castle rolling fireball)
	0x0d0: 1100, // kart_truck (Moonview Highway)
	0x0d1: 1000, // car_body
	0x0de: 1000, // K_bomb_car
	0x0d6: 1500, // VolcanoBall1 (Grumble Volcano)
	0x0d7: 600,  // penguin_s
	0x0d8: 700,  // penguin_m
	0x0db: 1000, // dossunc (Thwomp)
	0x0dd: 900,  // boble (GBA lava bubble)
	0x0e2: 1100, // hanachan (Wiggler)
	0x0e5: 600,  // crab
	0x0e9: 1300, // Hwanwan (route Chain Chomp)
	0x0ea: 1300, // HeyhoBallGBA (cannonball landing spot)
	0x18b: 3000, // KoopaFigure64 (fire-breathing Bowser statue)
	0x191: 700,  // kuribo (Goomba)
	0x192: 800,  // choropu (Monty Mole)
	0x193: 900,  // cow
	0x194: 1200, // pakkun_f (Piranha Plant)
	0x195: 2500, // WLfirebarGC
	0x196: 2500, // wanwan (Chain Chomp)
	0x197: 1200, // poihana (Cataquack)
	0x198: 1300, // DKrockGC (falling rock)
	0x199: 900,  // sanbo (Pokey)
	0x19a: 800,  // choropu2
	0x19b: 1000, // TruckWagon (Wario's Gold Mine)
	0x19c: 700,  // heyho (Shy Guy)
	0x1a1: 2500, // WLfireringGC
	0x1a4: 1500, // FireSnake (grounded Podoboo)
	0x1a5: 2500, // koopaFirebar
	0x1a8: 1500, // FireSnake_v
	0x1aa: 800,  // puchi_pakkun
	0x1fd: 1000, // FlamePole (Bowser's Castle fire geyser)
	0x212: 900,  // FlamePole_v
	0x216: 1600, // FlamePole_v_big
}

// Hazard is one item-destroying object: where it is (or the route it follows) and its reach.
type Hazard struct {
	Pos   [3]float32
	Route [][3]float32
	Reach float32
}

func kmpHazards(kmp []byte) ([]Hazard, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	routes, err := kmpRoutes(kmp)
	if err != nil {
		return nil, err
	}
	var hazards []Hazard
	for i := range count {
		e := data[i*gobjEntryLen:]
		reach, ok := hazardReach[binary.BigEndian.Uint16(e)]
		if !ok {
			continue
		}
		scale := max(f32(e[0x1c:]), f32(e[0x24:]), 1)
		h := Hazard{Pos: vec3(e[4:]), Reach: reach * min(scale, 4)}
		if route := int(binary.BigEndian.Uint16(e[0x28:])); route < len(routes) {
			h.Route = routes[route]
		}
		hazards = append(hazards, h)
	}
	return hazards, nil
}

// NearHazard reports whether an item at p could be destroyed by a course object.
func (c *Course) NearHazard(p [3]float32) bool {
	for _, h := range c.Hazards {
		d := Distance(p, h.Pos)
		for i := 1; i < len(h.Route); i++ {
			d = min(d, distanceToSegment(p, h.Route[i-1], h.Route[i]))
		}
		if d < h.Reach {
			return true
		}
	}
	return false
}

// ThrowLanding is Floors.Landing that also gives up when the item's path hits a wall (it
// bounces off somewhere the server can't predict).
func (c *Course) ThrowLanding(pos, vel [3]float32, g, rest float32) ([3]float32, bool) {
	if c.Floors == nil {
		return pos, false
	}
	p, vy := pos, vel[1]
	for range 600 {
		next := [3]float32{p[0] + vel[0], p[1] + vy, p[2] + vel[2]}
		if c.Walls != nil {
			if _, hit := c.Walls.Crosses(p, next); hit {
				return next, false
			}
		}
		p = next
		vy -= g
		if vy >= 0 {
			continue
		}
		if h, ok := c.Floors.Height(p[0], p[1]+300, p[2], 800); ok && p[1] <= h+rest {
			return [3]float32{p[0], h + rest, p[2]}, true
		}
	}
	return p, false
}
