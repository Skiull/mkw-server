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
	// When the last granted item was last seen held or trailing.
	lastItemSeenAt time.Time
	// The item granted before lastItem: a player can trail it while holding a new one.
	prevItem common.ItemType
	// Held and trailing items in the player's latest ITEM record.
	held     common.ItemType
	trailing common.ItemType
	// Last item request sequence number and its answer, so a retransmitted request gets
	// the same answer instead of a new roll.
	lastRequestSeq  byte
	lastRequestItem common.ItemType
	hasRequested    bool
	// Item boxes this player has been given an item for this race.
	boxCount int
	// When this player last used a Mega Mushroom.
	megaAt time.Time
	// When this player last used a Star or Bullet Bill.
	starAt, bulletAt time.Time
	// The Bullet Bill has ended early.
	bulletEnded bool
	// Vehicle and character from the latest RaceInfo.
	vehicle   common.Vehicle
	character common.Character
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
		prevItem: common.NoItem,
		held:     common.NoItem,
		trailing: common.NoItem,
	}
}
