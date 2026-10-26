package records

import (
	"fmt"

	"mkw-server/common"
)

// EVENT starts with one byte per slot, followed by the data of every non-empty slot.
const EventSlotCount = 0x18
const EventMinLen = EventSlotCount
const EventMaxLen = 0xf8

// EventSlot is one entry of the EVENT slot list.
type EventSlot struct {
	Action common.ItemEventAction
	Object common.ItemObject
}

type Event struct {
	Slots [EventSlotCount]EventSlot
	// Raw slot bytes, so new events can be detected by comparing against a previous packet.
	RawSlots [EventSlotCount]byte
	Data     []byte
}

func ParseEvent(data []byte) (*Event, error) {
	if len(data) < EventMinLen || len(data) > EventMaxLen {
		return nil, fmt.Errorf("invalid length (%d)", len(data))
	}

	e := &Event{
		RawSlots: [EventSlotCount]byte(data[:EventSlotCount]),
		Data:     data[EventSlotCount:],
	}

	knownDataSize := 0
	for i, raw := range e.RawSlots {
		slot := EventSlot{
			Action: common.ItemEventAction(raw >> 5),
			Object: common.ItemObject(raw & 0x1f),
		}

		if !slot.Object.IsValid() {
			return nil, fmt.Errorf("slot %d: invalid object (%d)", i, slot.Object)
		}

		// Empty slots always use the "none" object, and using an item always names one.
		emptyMismatch := slot.Action == common.EventNone && slot.Object != common.ObjNone
		ownItemMismatch := slot.Action.IsOwnItem() && slot.Object == common.ObjNone
		if emptyMismatch || ownItemMismatch {
			return nil, fmt.Errorf("slot %d: action %d with object %d", i, slot.Action, slot.Object)
		}

		// Shot sizes vary per item and aren't documented, so only count known sizes.
		if size := slot.Action.EventDataSize(); size > 0 {
			knownDataSize += size
		}

		e.Slots[i] = slot
	}

	if knownDataSize > len(e.Data) {
		return nil, fmt.Errorf("event data (%d bytes) too short for its events (%d bytes)", len(e.Data), knownDataSize)
	}

	return e, nil
}
