package records

import (
	"encoding/binary"
	"fmt"
	"math"
)

const RaceDataLen = 0x40

// Analog stick axes range from 0 to 0xe, with 7 as the center.
const maxStickAxis = 0xe

// Ranks go from 1 to 12. 0 is sent before the race has started.
const maxRank = 12

// RaceData holds one player's race state. Fields whose meaning is known are decoded; the
// raw bytes are kept for everything else.
type RaceData struct {
	Position [3]uint32 // 28 bit fixed point, 1/16 units. 0x9f42410 is 0.0.
	Speed    uint16    // 0x3e90 is stationary.
	Tilt     float32   // 1 is center, < 1 is left, > 1 is right.
	StickX   byte
	StickY   byte
	Lakitu   byte
	Buttons  byte
	Drift    byte
	Rank     byte
	data     [RaceDataLen]byte
}

// WorldPos is the player's position in course (KMP) units. From the community's protocol
// docs (https://mkwiiki.org/wiki/Network_Protocol/RACEDATA): add 0x3f800000 to each 28 bit
// value, read it as a float32, and subtract 1e6. Precision is 1/16 unit.
func (r *RaceData) WorldPos() [3]float32 {
	var p [3]float32
	for i, v := range r.Position {
		p[i] = math.Float32frombits(v+0x3f800000) - 1e6
	}
	return p
}

// HasPosition reports whether the record carries a real position. Before the player's kart
// is placed at the start of a race, the position fields are all zero, which decodes to
// about -1e6 on every axis.
func (r *RaceData) HasPosition() bool {
	return r.Position != [3]uint32{}
}

// Lakitu is set while Lakitu is rescuing the player, falling while they fall off the course.
// Either way the player is about to be moved to a respawn point.
func (r *RaceData) Respawning() bool {
	return r.data[0x27]>>6&3 != 0 || r.data[0x27]&1 != 0
}

// CannonFlips flips with each cannon the player is shot out of.
func (r *RaceData) CannonFlips() byte {
	return byte(binary.BigEndian.Uint16(r.data[0x2f:]) >> 6 & 7)
}

// BulletBillFlip flips each time the player uses a Bullet Bill.
func (r *RaceData) BulletBillFlip() byte {
	return r.data[0x29] >> 5 & 1
}

func ParseRaceData(data []byte) (*RaceData, error) {
	if len(data) != RaceDataLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), RaceDataLen)
	}

	r := &RaceData{
		Position: [3]uint32{
			binary.BigEndian.Uint32(data[0x0:]) >> 4 & 0x0fffffff,
			binary.BigEndian.Uint32(data[0x3:]) & 0x0fffffff,
			binary.BigEndian.Uint32(data[0x7:]) >> 4 & 0x0fffffff,
		},
		Speed:   binary.BigEndian.Uint16(data[0x11:0x13]),
		Tilt:    math.Float32frombits(binary.BigEndian.Uint32(data[0x18:0x1c])),
		StickX:  data[0x25] >> 4,
		StickY:  data[0x26] >> 4,
		Lakitu:  data[0x27] >> 6 & 0x3,
		Buttons: data[0x28] >> 4,
		Drift:   data[0x32] >> 6 & 0x3,
		Rank:    byte(binary.BigEndian.Uint16(data[0x32:0x34]) >> 7 & 0xf),
		data:    [RaceDataLen]byte(data),
	}

	if r.StickX > maxStickAxis {
		return nil, fmt.Errorf("invalid stick X axis (%d)", r.StickX)
	}

	if r.StickY > maxStickAxis {
		return nil, fmt.Errorf("invalid stick Y axis (%d)", r.StickY)
	}

	// NaN or infinite floats are never produced by the game and can misbehave on receivers.
	tilt := float64(r.Tilt)
	if math.IsNaN(tilt) || math.IsInf(tilt, 0) {
		return nil, fmt.Errorf("invalid tilt (%v)", r.Tilt)
	}

	if r.Rank > maxRank {
		return nil, fmt.Errorf("invalid rank (%d)", r.Rank)
	}

	return r, nil
}

// ParseRaceDataRecords parses the RaceData records in a packet. There's one per local player.
func ParseRaceDataRecords(data []byte, ctx *Context) ([]*RaceData, error) {
	if len(data)%RaceDataLen != 0 {
		return nil, fmt.Errorf("length (%d) isn't a multiple of %d", len(data), RaceDataLen)
	}

	count := len(data) / RaceDataLen
	if count != ctx.LocalPlayerCount {
		return nil, fmt.Errorf("record count (%d) != local player count (%d)", count, ctx.LocalPlayerCount)
	}

	out := make([]*RaceData, 0, count)
	for i := range count {
		r, err := ParseRaceData(data[i*RaceDataLen : (i+1)*RaceDataLen])
		if err != nil {
			return nil, fmt.Errorf("player %d: %w", i+1, err)
		}
		out = append(out, r)
	}
	return out, nil
}

// Flags below are from https://mkwiiki.org/wiki/Network_Protocol/RACEDATA (bits numbered
// from the most significant).

// MushroomBoost is set from using a Mushroom until its boost is over.
func (r *RaceData) MushroomBoost() bool { return r.data[0x29]>>6&1 != 0 }

// MegaFlip flips each time the player uses a Mega Mushroom.
func (r *RaceData) MegaFlip() byte { return r.data[0x29] & 1 }

// StarFlip flips each time the player uses a Star.
func (r *RaceData) StarFlip() byte { return r.data[0x2a] >> 7 & 1 }

// MiniTurboFlip flips each time the player releases a mini-turbo (or a start boost).
func (r *RaceData) MiniTurboFlip() byte { return r.data[0x28] >> 3 & 1 }

// TrickFlip flips each time the player does a trick.
func (r *RaceData) TrickFlip() byte { return r.data[0x2f] >> 5 & 1 }

// ThwompFlip flips each time a Thwomp or a Toad's Factory press squishes the player.
func (r *RaceData) ThwompFlip() byte { return r.data[0x2a] & 1 }

// HitFlags holds the bits that flip when the player gets hit: by a Star or Mega user (or a
// cow), a Fake Item Box, a Blue Shell or Bob-omb (full or half), or a Goomba.
func (r *RaceData) HitFlags() byte { return r.data[0x2c] & 0xf2 }

// BlueShellHitFlags holds the Blue Shell / Bob-omb hit bits (full and half).
func (r *RaceData) BlueShellHitFlags() byte { return r.data[0x2c] & 0x30 }

// Raw returns the record's bytes (for diagnostics).
func (r *RaceData) Raw() []byte { return r.data[:] }
