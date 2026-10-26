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
