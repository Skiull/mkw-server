package records

import (
	"fmt"

	"mkw-server/common"
)

const ItemLen = 0x8

// Item activation mode: 0 is no item, 1-7 are handshake states.
const maxItemMode = 7

// ItemState is a player's item status from an ITEM record.
type ItemState struct {
	Timer       byte
	Held        common.ItemType // Item in the item box slot.
	Trailing    common.ItemType // Item being dragged behind the vehicle.
	Mode        byte
	TrailMode   byte
	Acknowledge byte
	AckTimer    byte
}

func ParseItem(data []byte) (*ItemState, error) {
	if len(data) != ItemLen {
		return nil, fmt.Errorf("size mismatch (%d != %d)", len(data), ItemLen)
	}

	s := &ItemState{
		Timer:       data[0],
		Held:        common.ItemType(data[1]),
		Trailing:    common.ItemType(data[2]),
		Mode:        data[3],
		TrailMode:   data[4],
		Acknowledge: data[5],
		AckTimer:    data[6],
	}

	if !s.Held.IsValid() {
		return nil, fmt.Errorf("invalid held item (%d)", s.Held)
	}

	if !s.Trailing.IsValid() {
		return nil, fmt.Errorf("invalid trailing item (%d)", s.Trailing)
	}

	if s.Mode > maxItemMode {
		return nil, fmt.Errorf("invalid mode (%d)", s.Mode)
	}

	if data[7] != 0 {
		return nil, fmt.Errorf("padding (%d) != 0", data[7])
	}

	return s, nil
}

// ParseItemRecords parses the ITEM records in a packet. There's one per local player.
func ParseItemRecords(data []byte, ctx *Context) ([]*ItemState, error) {
	if len(data)%ItemLen != 0 {
		return nil, fmt.Errorf("length (%d) isn't a multiple of %d", len(data), ItemLen)
	}

	count := len(data) / ItemLen
	if count != ctx.LocalPlayerCount {
		return nil, fmt.Errorf("record count (%d) != local player count (%d)", count, ctx.LocalPlayerCount)
	}

	out := make([]*ItemState, 0, count)
	for i := range count {
		s, err := ParseItem(data[i*ItemLen : (i+1)*ItemLen])
		if err != nil {
			return nil, fmt.Errorf("player %d: %w", i+1, err)
		}
		out = append(out, s)
	}
	return out, nil
}
