package core

import (
	"time"

	"mkw-server/common"
)

// Game specific context of a player.
type Player struct {
	aid     byte
	isGuest bool

	// Latest rank reported in RaceData. 0 before the race starts.
	rank byte
	// Latest battle score reported in RaceMode.
	battleScore byte

	// Server-side item state. Only used when server items are enabled.
	grant *itemGrant
	// Most recently granted item. Players can still be dragging it after it's left their
	// item slot, so it stays allowed as a trailing item and in events.
	lastItem common.ItemType
	// Last item request sequence number and its answer, so a retransmitted request gets
	// the same answer instead of a new roll.
	lastRequestSeq  byte
	lastRequestItem common.ItemType
	hasRequested    bool
}

// itemGrant is an item the server handed to a player that hasn't been used up yet.
type itemGrant struct {
	item      common.ItemType
	grantedAt time.Time
	// Set once the player's ITEM record has shown the item, so the slot later going empty
	// means the item was used.
	seenHeld bool
}

func newPlayer(aid byte, isGuest bool) *Player {
	return &Player{
		aid:      aid,
		isGuest:  isGuest,
		lastItem: common.NoItem,
	}
}
