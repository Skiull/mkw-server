package core

import (
	"mkw-server/common"
	"mkw-server/core/records"
)

// Item spy: every player's ITEM record says what's in their item slot, and it's relayed to
// everyone else, so a cheat can read it before the item is used. Other games don't need it:
// they learn about item uses from EVENT records, and they acknowledge ITEM records by their
// timer byte, not the item. So the server blanks the held item (and its phase, which must say
// "no item" when the item is empty, or the game rejects the record) before relaying.
// Trailing items stay: everyone can see them behind the kart.

// ITEM record layout (records/item.go): [1] held item, [3] held phase.
const (
	itemHeldOffset  = 1
	itemPhaseOffset = 3
)

// Held phases (mkw-structures ItemHandler::Packet::HeldPhase).
const (
	heldPhaseNoItem    = 0
	heldPhaseDecided   = 1
	heldPhaseFinalised = 2
	heldPhaseThreeLeft = 4
	heldPhaseTwoLeft   = 5
	heldPhaseOneLeft   = 6
)

// maskHeldItems blanks the held items in a Race packet's ITEM records, in place. Called after
// the server has read the packet and before it's relayed. Caller must hold mu.
func maskHeldItems(data []byte, pkt *RacePacket) {
	for i, st := range pkt.Items {
		if st.Held == common.NoItem || st.Held == common.ThunderCloud {
			// Thunder Clouds are passed between players by bumping and show above the kart.
			continue
		}
		switch st.Mode {
		case heldPhaseDecided, heldPhaseFinalised, heldPhaseThreeLeft, heldPhaseTwoLeft, heldPhaseOneLeft:
		default:
			continue
		}
		rec := data[pkt.itemOffset+i*records.ItemLen:]
		rec[itemHeldOffset] = byte(common.NoItem)
		rec[itemPhaseOffset] = heldPhaseNoItem
	}
}
