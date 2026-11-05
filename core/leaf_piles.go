package core

import (
	"time"

	"mkw-server/common"
)

// Leaf piles drop an item onto the track, not into the item slot, so using one never comes
// with a server grant.

// Caller must hold mu.
func checkLeafPiles(a *Aid, p *Player, pos [3]float32, now time.Time) {}

// leafPileItem reports whether one of the aid's players could be using obj from a leaf
// pile. Caller must hold mu.
func leafPileItem(a *Aid, obj common.ItemObject, now time.Time) bool {
	c := room.config.Courses[a.course]
	return c != nil && len(c.LeafPiles) > 0
}
