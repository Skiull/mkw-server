package items

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"mkw-server/common"
)

// Item box positions, read from each course's course.kmp (GOBJ section). Layout from the
// community's KMP documentation: https://mkwiiki.org/wiki/KMP_(File_Format) and
// https://mkwiiki.org/wiki/Item_Settings_-_Item_Boxes. Object names from Wiimm's KMP object
// database (https://szs.wiimm.de/cgi/mkw/object).

const (
	kmpMagic      = "RKMD"
	kmpGOBJOffset = 0x2c // Header field holding the GOBJ offset, relative to the header end.
	gobjEntryLen  = 0x3c
	itemBoxObject = 0x65 // itembox
	sandBoxObject = 0x76 // s_itembox: rises out of the sand (Thwomp Desert)
	// Item boxes that move along a route (KMP POTI).
	routeBoxObject  = 0xc9 // f_itembox: route controlled
	waveBoxObject   = 0xd4 // w_itembox: several boxes along a route
	lineBoxObject   = 0xd5 // w_itemboxline: Toad's Factory press turns blocks into boxes on a route
	sineBoxObject   = 0xee // sin_itembox: back and forth along a route (Ghost Valley 2)
	chompBoxObject  = 0xe9 // Hwanwan: a rolling Chain Chomp carrying item boxes along a route (DS Peach Gardens)
	kmpPOTIOffset   = 0x30
	framesPerSecond = 60
)

// Box is one item box on a course.
type Box struct {
	Pos [3]float32
	// For moving boxes: the route it travels. The box can be anywhere along it, and several
	// boxes may share it, so taken/respawn tracking doesn't apply.
	Route [][3]float32
	// How long the box is gone after someone takes it (setting 6).
	Respawn time.Duration
	// Setting 2: 0 for a normal box, otherwise a fixed item set (see SpecialBoxItem).
	Special uint16
}

// courseFiles maps course ids to their file names in /Race/Course.
var courseFiles = map[common.Course]string{
	common.MarioCircuit:       "castle_course",
	common.MooMooMeadows:      "farm_course",
	common.MushroomGorge:      "kinoko_course",
	common.GrumbleVolcano:     "volcano_course",
	common.ToadsFactory:       "factory_course",
	common.CoconutMall:        "shopping_course",
	common.DKSummit:           "boardcross_course",
	common.WariosGoldMine:     "truck_course",
	common.LuigiCircuit:       "beginner_course",
	common.DaisyCircuit:       "senior_course",
	common.MoonviewHighway:    "ridgehighway_course",
	common.MapleTreeway:       "treehouse_course",
	common.BowsersCastle:      "koopa_course",
	common.RainbowRoad:        "rainbow_course",
	common.DryDryRuins:        "desert_course",
	common.KoopaCape:          "water_course",
	common.RPeachBeach:        "old_peach_gc",
	common.RMarioCircuit:      "old_mario_gc",
	common.RWaluigiStadium:    "old_waluigi_gc",
	common.RDKMountain:        "old_donkey_gc",
	common.RYoshiFalls:        "old_falls_ds",
	common.RDesertHills:       "old_desert_ds",
	common.RPeachGardens:      "old_garden_ds",
	common.RDelfinoSquare:     "old_town_ds",
	common.RMarioCircuit3:     "old_mario_sfc",
	common.RGhostValley2:      "old_obake_sfc",
	common.RMarioRaceway:      "old_mario_64",
	common.RSherbetLand:       "old_sherbet_64",
	common.RBowsersCastle:     "old_koopa_64",
	common.RDKJungleParkway:   "old_donkey_64",
	common.RBC3:               "old_koopa_gba",
	common.RShyGuyBeach:       "old_heyho_gba",
	common.DelfinoPier:        "venice_battle",
	common.BlockPlaza:         "block_battle",
	common.ChainChompRoulette: "casino_battle",
	common.FunkyStadium:       "skate_battle",
	common.ThwompDesert:       "sand_battle",
	common.GCNCookieLand:      "old_CookieLand_gc",
	common.DSTwilightHouse:    "old_House_ds",
	common.SNESBattleCourse4:  "old_battle4_sfc",
	common.GBABattleCourse3:   "old_battle3_gba",
	common.N64Skyscraper:      "old_matenro_64",
}

func kmpBoxes(kmp []byte) ([]Box, error) {
	data, count, err := kmpSection(kmp, kmpGOBJOffset, "GOBJ", gobjEntryLen)
	if err != nil {
		return nil, err
	}
	routes, err := kmpRoutes(kmp)
	if err != nil {
		return nil, err
	}

	var boxes []Box
	for i := range count {
		e := data[i*gobjEntryLen:]
		id := binary.BigEndian.Uint16(e)
		var b Box
		b.Pos = vec3(e[4:])
		b.Respawn = respawnTime(0)
		settings := e[0x2a:]
		switch id {
		case itemBoxObject:
			b.Special = binary.BigEndian.Uint16(settings[2:])
			b.Respawn = respawnTime(binary.BigEndian.Uint16(settings[10:]))
		case sandBoxObject:
			// Settings aren't documented for this one.
		case routeBoxObject, waveBoxObject, lineBoxObject, sineBoxObject, chompBoxObject:
			if route := int(binary.BigEndian.Uint16(e[0x28:])); route < len(routes) {
				b.Route = routes[route]
			}
			if id == routeBoxObject || id == waveBoxObject {
				b.Special = binary.BigEndian.Uint16(settings[2:]) // Item type, like itembox.
			}
		default:
			continue
		}
		boxes = append(boxes, b)
	}
	return boxes, nil
}

// kmpRoutes reads the POTI routes: each is a count, two settings bytes, then 0x10 byte
// points (position, two settings).
func kmpRoutes(kmp []byte) ([][][3]float32, error) {
	data, count, err := kmpSection(kmp, kmpPOTIOffset, "POTI", 0)
	if err != nil {
		return nil, nil // Courses without routes.
	}
	var routes [][][3]float32
	off := 0
	for range count {
		if off+4 > len(data) {
			return nil, errors.New("POTI section is truncated")
		}
		n := int(binary.BigEndian.Uint16(data[off:]))
		off += 4
		if off+n*0x10 > len(data) {
			return nil, errors.New("POTI route is truncated")
		}
		route := make([][3]float32, n)
		for k := range route {
			route[k] = vec3(data[off+k*0x10:])
		}
		routes = append(routes, route)
		off += n * 0x10
	}
	return routes, nil
}

// DistanceTo is how close p gets to the box: to its spot, or for a moving box to any point
// of its route.
func (b *Box) DistanceTo(p [3]float32) float32 {
	best := Distance(p, b.Pos)
	for i := 1; i < len(b.Route); i++ {
		best = min(best, distanceToSegment(p, b.Route[i-1], b.Route[i]))
	}
	return best
}

func distanceToSegment(p, a, b [3]float32) float32 {
	ab := sub3(b, a)
	l := dot3(ab, ab)
	if l == 0 {
		return Distance(p, a)
	}
	t := max(0, min(1, dot3(sub3(p, a), ab)/l))
	return Distance(p, add3(a, scale3(ab, t)))
}

// respawnTime is how long a box with setting 6 = timing stays gone (in frames, from
// the community's Item Settings page).
func respawnTime(timing uint16) time.Duration {
	frames := 150
	switch timing {
	case 4, 5:
		frames = 600
	case 6:
		frames = 900
	}
	return time.Duration(frames) * time.Second / framesPerSecond
}

// SpecialBoxItem rolls the fixed item of a special box (setting 2), or returns NoItem for a
// normal box. Values from the community's Item Settings page; only value 12 (Chain Chomp
// Roulette's center boxes) appears on Nintendo's courses.
func (e *Engine) SpecialBoxItem(special uint16) common.ItemType {
	switch special {
	case 1:
		return common.Banana
	case 2:
		return common.Mushroom
	case 3:
		return common.TripleMushroom
	case 4:
		return common.Star
	case 5:
		return common.TripleGreenShells
	case 6:
		if e.rng.IntN(2) == 0 {
			return common.Banana
		}
		return common.Mushroom
	case 7:
		return common.GreenShell
	case 8:
		return common.BobOmb
	case 9:
		return common.RedShell
	case 10:
		return common.MegaMushroom
	case 11:
		return common.ThunderCloud
	case 12:
		if e.rng.IntN(100) < 95 {
			return common.Star
		}
		return common.Mushroom
	case 13, 14, 15, 16:
		return common.Mushroom
	}
	return common.NoItem
}

// Distance between two points.
func Distance(a, b [3]float32) float32 {
	dx, dy, dz := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}
