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
	// Item last seen trailing or circling that was accepted, and when.
	trailItem   common.ItemType
	trailItemAt time.Time
	// Last cannon launch and respawn seen (they move the kart on their own).
	launchedAt  time.Time
	respawnedAt time.Time
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
	// Recent reported positions, for item box checks.
	positions []timedPos
	// When this player last drove through a leaf pile, which can drop an item.
	leafPileAt time.Time
	leafPile   int // Index of that pile in the course's LeafPiles.
	// When this player last used a Mega Mushroom.
	megaAt time.Time
	// When this player last used a Star or Bullet Bill.
	starAt, bulletAt time.Time
	// A Lightning shrink (shrink.go): when it hit and when it wears off.
	shockFrom, shockUntil time.Time
	// The Bullet Bill looks over (back to driving speed); see checkMovement.
	bulletEnded bool
	shrink      shrinkLog // Speeds after a Lightning, for calibrating a shrunk limit.
	// No-hit checks (hit_check.go).
	hitFlags, blueHitFlags byte
	hitFlagsSeen           bool
	kartActions            uint32 // KartAction flip bits seen last (RACEDATA word A).
	cloudZap               byte   // Thunder Cloud zap flip bit seen last.
	hitAt, rammedAt        time.Time
	lastPos                [3]float32
	lastPosAt              time.Time
	// Vehicle and character from the latest RaceInfo, for offroad speed.
	vehicle   common.Vehicle
	character common.Character
	// Movement check state.
	move moveState
	// Server-side race progress, and the course it's for.
	progress       progressState
	progressCourse common.Course
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
		aid:       aid,
		isGuest:   isGuest,
		lastItem:  common.NoItem,
		prevItem:  common.NoItem,
		trailItem: common.NoItem,
		held:      common.NoItem,
		trailing:  common.NoItem,
		progress:  newProgress(),
		// Not a course yet: the first RaceData starts fresh progress.
		progressCourse: common.NoVote,
	}
}
