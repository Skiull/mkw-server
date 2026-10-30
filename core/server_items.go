package core

import (
	"fmt"
	"time"

	"mkw-server/common"
	"mkw-server/items"
	"mkw-server/logging"
)

// Server-side item decisions. See docs/server-item-decisions.md for the protocol.
//
// Client -> server, on touching an item box:  "IR" playerIdx seq
// Server -> client, the decided item:        "IT" playerIdx seq item
//
// item is common.NoItem when the request is refused.

const itemRequestMagic = "IR"
const itemResponseMagic = "IT"
const itemRequestLen = 4

// Item boxes respawn and the roulette takes a few seconds, so legit requests are never
// this close together.
const minItemRequestInterval = time.Second

// A grant that never shows up in the player's ITEM record is dropped after this long, so a
// lost response can't lock a player out of items.
const itemGrantTimeout = 15 * time.Second

// The race clock starts this long after the server sees the countdown start (the 3-2-1).
// Overestimated, so start-locked items are never handed out early.
const countdownLength = 5 * time.Second

func isItemRequestPacket(data []byte) bool {
	return len(data) == itemRequestLen && string(data[:2]) == itemRequestMagic
}

// Caller must hold mu.
func handleItemRequest(a *Aid, data []byte, now time.Time) {
	engine := room.config.ItemEngine
	if engine == nil {
		return
	}

	playerIdx, seq := data[2], data[3]
	if int(playerIdx) >= a.localPlayerCount() {
		strike(a, fmt.Sprintf("item request for player %d it doesn't have", playerIdx), now)
		return
	}
	p := a.players[playerIdx]

	// Retransmitted request: answer the same way instead of rolling again.
	if p.hasRequested && p.lastRequestSeq == seq {
		sendItemResponse(a, playerIdx, seq, p.lastRequestItem)
		return
	}

	item, reason := decideItem(engine, a, p, now)
	if reason != "" {
		logging.Log("Aid %d player %d item request refused: %s", a.aid, playerIdx, reason)
	}

	p.hasRequested = true
	p.lastRequestSeq = seq
	p.lastRequestItem = item
	sendItemResponse(a, playerIdx, seq, item)
}

// decideItem returns the item for a request, or NoItem and why it was refused.
// Caller must hold mu.
func decideItem(engine *items.Engine, a *Aid, p *Player, now time.Time) (common.ItemType, string) {
	if !a.isRacer || !a.raceStarted {
		return common.NoItem, "not racing"
	}

	expireGrant(p, now)
	if p.grant != nil {
		if now.Sub(p.grant.grantedAt) < minItemRequestInterval {
			return common.NoItem, "requests too close together"
		}
		// The slot is empty (an instant item like Lightning may be used before a packet
		// shows it), or holds the last Mushroom being used as the box is touched (the
		// mushroom glitch). The ITEM record lags the item box touch by a packet or two.
		if p.held != common.NoItem && p.held != common.Mushroom {
			return common.NoItem, fmt.Sprintf("still has %v", p.grant.item)
		}
		p.grant = nil
	}

	req := items.Request{Mode: a.mode, InPlay: itemsInPlay(), Invincible: p.invincible(now)}
	switch a.mode {
	case common.ModeVS:
		if p.rank == 0 {
			return common.NoItem, "no rank yet"
		}
		req.Rank = int(p.rank)
		if room.config.CheckMovement && room.config.Courses[a.course] != nil {
			req.Rank, _ = itemRank(p, a.course)
		}
		req.PlayerCount = racingPlayerCount()
		req.RaceTime = now.Sub(a.raceStartedAt) - countdownLength
	case common.ModeBalloonBattle, common.ModeCoinRunners:
		req.BehindLeader = int(leaderScore()) - int(p.battleScore)
	}

	box, boxProblem := checkItemBox(a, p, now)
	if boxProblem != "" {
		if room.config.EnforceBoxes {
			return common.NoItem, "box check: " + boxProblem
		}
		logging.Log("Aid %d player %d box check failed (not enforced): %s", a.aid, p.index(a), boxProblem)
	}

	p.boxCount++
	var item common.ItemType
	if forcedMushroom(p.boxCount) {
		item = common.Mushroom
	} else if box != nil && box.Special != 0 {
		// A special box gives its fixed item, or a Mushroom if that item isn't available.
		item = engine.SpecialBoxItem(box.Special)
		if !engine.Available(item, req, now) {
			item = common.Mushroom
		}
	} else {
		var err error
		if item, err = engine.Decide(req, now); err != nil {
			p.boxCount--
			return common.NoItem, err.Error()
		}
	}

	p.grant = &itemGrant{item: item, grantedAt: now}
	recordItem(a, p.index(a), "granted", item, now)
	p.prevItem = p.lastItem
	p.lastItem = item
	return item, ""
}

// index is the player's local index in its aid (0, or 1 for a guest).
func (p *Player) index(a *Aid) int {
	for i, q := range a.players {
		if q == p {
			return i
		}
	}
	return -1
}

// forcedMushroom is the game's own rule (ItemRoulette::start): a race's 16th, 18th, 20th,
// and 21st+ item boxes always give a Mushroom, whatever the position.
func forcedMushroom(box int) bool {
	return box >= 21 || (box >= 16 && box%2 == 0)
}

func sendItemResponse(a *Aid, playerIdx, seq byte, item common.ItemType) {
	packet := []byte{itemResponseMagic[0], itemResponseMagic[1], playerIdx, seq, byte(item)}
	if _, err := room.conn.WriteTo(packet, a.addr); err != nil {
		logging.Log("Error sending item response to aid %d: %v", a.aid, err)
	}
}

func expireGrant(p *Player, now time.Time) {
	if p.grant != nil && !p.grant.seenHeld && now.Sub(p.grant.grantedAt) > itemGrantTimeout {
		p.grant = nil
	}
}

// heldMatchesGrant reports whether a held item is what was granted. Triple items may be
// reported as their single counterpart once some have been used.
func heldMatchesGrant(held, granted common.ItemType) bool {
	return held == granted || (granted.Uses() > 1 && held.Object() == granted.Object())
}

// How long after the last granted item was last seen it may reappear in the slot.
const itemReturnWindow = 10 * time.Second

// observeItems checks a packet's ITEM and EVENT records against what the server granted,
// and advances each player's grant as the item gets used. Caller must hold mu.
func observeItems(a *Aid, pkt *RacePacket, now time.Time) error {
	for i, st := range pkt.Items {
		p := a.players[i]
		p.held = st.Held
		p.trailing = st.Trailing
		expireGrant(p, now)

		switch {
		case st.Held == common.NoItem && p.grant != nil && st.Trailing != common.NoItem && st.Trailing.Object() == p.grant.item.Object():
			// Dragged behind the vehicle: still the granted item, not used yet.
		case st.Held == common.NoItem:
			// The slot emptying after the item was seen means it's been used.
			if p.grant != nil && p.grant.seenHeld {
				switch p.grant.item {
				case common.MegaMushroom:
					p.megaAt = now // Raises the speed cap (movement check).
				case common.Star:
					p.starAt = now
				case common.BulletBill:
					p.bulletAt, p.bulletEnded = now, false
				case common.Lightning:
					lightningStruck(a)
				}
				p.grant = nil
			}
		case p.grant != nil && heldMatchesGrant(st.Held, p.grant.item):
			p.grant.seenHeld = true
		case st.Held == common.ThunderCloud:
			// Thunder Clouds are passed between players by bumping, not item boxes.
		case heldMatchesGrant(st.Held, p.lastItem) && now.Sub(p.lastItemSeenAt) <= itemReturnWindow:
			// The last granted item back in the slot after it looked used: a dragged item
			// returning after a hit, a Cataquack or a cannon. Track it as the grant again.
			p.grant = &itemGrant{item: p.lastItem, grantedAt: now, seenHeld: true}
		default:
			return &ungrantedItemError{fmt.Sprintf("player %d holds %v without a grant", i+1, st.Held)}
		}
		if p.lastItem != common.NoItem && (heldMatchesGrant(st.Held, p.lastItem) || st.Trailing.Object() == p.lastItem.Object()) {
			p.lastItemSeenAt = now
		}

		if st.Trailing != common.NoItem && st.Trailing.Object() != p.lastItem.Object() && st.Trailing.Object() != p.prevItem.Object() {
			return fmt.Errorf("player %d trails %v, last granted %v", i+1, st.Trailing, p.lastItem)
		}
	}

	if pkt.Event == nil {
		return nil
	}

	// Event slots stay unchanged for a few seconds, so only check slots that changed.
	prev, hadPrev := a.lastEventSlots, a.hasEventSlots
	a.lastEventSlots, a.hasEventSlots = pkt.Event.RawSlots, true

	for i, slot := range pkt.Event.Slots {
		if hadPrev && pkt.Event.RawSlots[i] == prev[i] {
			continue
		}
		switch slot.Action {
		case common.EventUsed:
			recordItem(a, 0, "used", slot.Object, now)
		case common.EventShot:
			recordItem(a, 0, "threw", slot.Object, now)
		case common.EventHitFree, common.EventHitDragged:
			recordItem(a, 0, "hit", slot.Object, now)
		}
		if slot.Action == common.EventShot {
			if shot, ok := pkt.Event.ShotAt(i); ok {
				// Temporary: throws and hits with positions, to work out item gravity.
				logging.Log("Aid %d threw %v from (%.0f, %.0f, %.0f) at (%.0f, %.0f, %.0f) u/f", a.aid, shot.Object,
					shot.Pos[0], shot.Pos[1], shot.Pos[2], shot.Vel[0], shot.Vel[1], shot.Vel[2])
				trackShot(a, shot, now)
			}
		}
		if slot.Action == common.EventHitFree && len(a.players) > 0 {
			p := a.players[0]
			logging.Log("Aid %d hit a %v at (%.0f, %.0f, %.0f)", a.aid, slot.Object, p.lastPos[0], p.lastPos[1], p.lastPos[2])
			removeHitItem(a, slot.Object, now)
		}
		if !slot.Action.IsOwnItem() {
			continue
		}
		if !aidMayUseObject(a, slot.Object) {
			if leafPileItem(a, slot.Object, now) {
				continue
			}
			return &ungrantedItemError{fmt.Sprintf("event slot %d uses %d without a grant", i, slot.Object)}
		}
		switch slot.Object {
		case common.ObjLightning:
			lightningStruck(a)
		case common.ObjBlueShell:
			expectBlueShell(a, now)
		}
	}
	return nil
}

func aidMayUseObject(a *Aid, obj common.ItemObject) bool {
	for _, p := range a.players {
		if (p.lastItem != common.NoItem && p.lastItem.Object() == obj) || (p.prevItem != common.NoItem && p.prevItem.Object() == obj) {
			return true
		}
	}
	return false
}

// itemsInPlay counts item objects held, trailed, or granted but not yet shown, across the
// room. Objects already on the track or flying (a dropped Banana, a Blue Shell on its way)
// aren't visible to the server and aren't counted. Caller must hold mu.
func itemsInPlay() map[common.ItemObject]int {
	counts := make(map[common.ItemObject]int)
	add := func(item common.ItemType) {
		if item != common.NoItem {
			counts[item.Object()] += item.Uses()
		}
	}
	for _, a := range aids {
		if a.kicked {
			continue
		}
		for _, p := range a.players {
			add(p.held)
			add(p.trailing)
			if p.grant != nil && !p.grant.seenHeld {
				add(p.grant.item)
			}
		}
	}
	return counts
}

// racingPlayerCount counts players (guests included) in the race. Caller must hold mu.
func racingPlayerCount() int {
	n := 0
	for _, a := range aids {
		if (a.isRacer || a.bot != nil) && !a.kicked {
			n += a.localPlayerCount()
		}
	}
	return n
}

// leaderScore is the highest battle score in the room. Caller must hold mu.
func leaderScore() byte {
	var best byte
	for _, a := range aids {
		if !a.isRacer || a.kicked {
			continue
		}
		for _, p := range a.players {
			best = max(best, p.battleScore)
		}
	}
	return best
}

// resetItemState clears item state when a new race starts. Caller must hold mu.
func resetItemState(a *Aid) {
	a.hasEventSlots = false
	for _, p := range a.players {
		p.grant = nil
		p.lastItem = common.NoItem
		p.prevItem = common.NoItem
		p.held = common.NoItem
		p.trailing = common.NoItem
		p.hasRequested = false
		p.boxCount = 0
		p.positions = nil
	}
}

// ungrantedItemError is a player holding or using an item the server never gave them,
// which only a cheat can do.
type ungrantedItemError struct{ msg string }

func (e *ungrantedItemError) Error() string { return e.msg }

// When a Lightning was last used. Guarded by mu.
var lastLightningAt time.Time

// lightningStruck ends the Mega Mushroom of everyone but the Lightning's user: a shock
// shrinks Mega players back down, so their speed limit goes back to normal. Caller must
// hold mu.
func lightningStruck(user *Aid) {
	// One Lightning can be reported twice (the event and the emptied item slot).
	now := time.Now()
	if now.Sub(lastLightningAt) < 2*time.Second {
		return
	}
	lastLightningAt = now
	expectLightning(user, now)
	for _, a := range aids {
		if a == user {
			continue
		}
		for _, p := range a.players {
			startShrinkLog(a, p, time.Now())
			startShock(p, now)
			p.megaAt = time.Time{}
		}
	}
}

// How long a used Star, Mega Mushroom or Bullet Bill lasts, plus slack for packet lag.
const (
	starDuration   = 8 * time.Second
	megaItemWindow = 8 * time.Second
	bulletDuration = 10 * time.Second
)

// invincible reports whether the player may be in a Star, Mega or Bullet Bill, which
// rules out pulling a Thunder Cloud.
func (p *Player) invincible(now time.Time) bool {
	within := func(at time.Time, d time.Duration) bool { return !at.IsZero() && now.Sub(at) <= d }
	return within(p.starAt, starDuration) || within(p.megaAt, megaItemWindow) || (within(p.bulletAt, bulletDuration) && !p.bulletEnded)
}
