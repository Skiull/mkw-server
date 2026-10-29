package core

import (
	"time"

	"mkw-server/common"
	"mkw-server/items"
	"mkw-server/logging"
)

// Leaf piles drop an item onto the track, not into the item slot, so using one never comes
// with a server grant. Each pile's KMP settings name the item it gives (undocumented; see
// leafPileItems). Every use is logged with the pile's setting.

const (
	// How close a position must be to a pile's center. Piles are a bit bigger than boxes.
	leafPileRadius = 1500
	// How long a dropped item may lie on the track before the player uses it.
	leafPileItemWindow = 10 * time.Second
)

// Items each leaf pile item setting can give, from test races (2026-10-27): the two
// setting-4 piles (20% chance) gave the Stars, the others gave Mushrooms and Bananas. That
// matches the ItemDirect object's numbering (4 = Star). Which of 1 and 6 is the Mushroom
// isn't pinned down, so both allow either. Settings not listed may give leafPileAnyItem.
var leafPileItems = map[uint16][]common.ItemObject{
	1: {common.ObjMushroom, common.ObjBanana},
	4: {common.ObjStar},
	6: {common.ObjMushroom, common.ObjBanana},
}

var leafPileAnyItem = []common.ItemObject{common.ObjMushroom, common.ObjBanana, common.ObjStar}

// checkLeafPiles notes a player passing through a leaf pile. Caller must hold mu.
func checkLeafPiles(a *Aid, p *Player, pos [3]float32, now time.Time) {
	c := room.config.Courses[a.course]
	if c == nil {
		return
	}
	for i, pile := range c.LeafPiles {
		if pile.Chance > 0 && items.Distance(pile.Pos, pos) <= leafPileRadius {
			if p.leafPileAt.IsZero() || p.leafPile != i || now.Sub(p.leafPileAt) > leafPileItemWindow {
				logging.Log("Aid %d drove through leaf pile %d (item setting %d, chance %d)", a.aid, i, pile.Item, pile.Chance)
			}
			p.leafPileAt, p.leafPile = now, i
			return
		}
	}
}

// leafPileItem reports whether one of the aid's players could be using obj from the leaf
// pile they last drove through. Caller must hold mu.
func leafPileItem(a *Aid, obj common.ItemObject, now time.Time) bool {
	c := room.config.Courses[a.course]
	if c == nil {
		return false
	}
	for _, p := range a.players {
		if p.leafPileAt.IsZero() || now.Sub(p.leafPileAt) > leafPileItemWindow || p.leafPile >= len(c.LeafPiles) {
			continue
		}
		pile := c.LeafPiles[p.leafPile]
		if pileGives(pile.Item, obj) {
			logging.Log("Aid %d used %v from leaf pile %d (item setting %d)", a.aid, obj, p.leafPile, pile.Item)
			if obj == common.ObjStar {
				p.starAt = now // No Thunder Cloud while it lasts.
			}
			return true
		}
	}
	return false
}

func pileGives(setting uint16, obj common.ItemObject) bool {
	allowed, ok := leafPileItems[setting]
	if !ok {
		allowed = leafPileAnyItem
	}
	for _, o := range allowed {
		if o == obj {
			return true
		}
	}
	return false
}
