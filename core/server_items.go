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
		return common.NoItem, fmt.Sprintf("still has %v", p.grant.item)
	}

	req := items.Request{Mode: a.mode}
	switch a.mode {
	case common.ModeVS:
		if p.rank == 0 {
			return common.NoItem, "no rank yet"
		}
		req.Rank = int(p.rank)
		req.PlayerCount = racingPlayerCount()
	case common.ModeBalloonBattle, common.ModeCoinRunners:
		req.BehindLeader = int(leaderScore()) - int(p.battleScore)
	}

	item, err := engine.Decide(req, now)
	if err != nil {
		return common.NoItem, err.Error()
	}

	p.grant = &itemGrant{item: item, grantedAt: now}
	p.lastItem = item
	return item, ""
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

// observeItems checks a packet's ITEM and EVENT records against what the server granted,
// and advances each player's grant as the item gets used. Caller must hold mu.
func observeItems(a *Aid, pkt *RacePacket, now time.Time) error {
	for i, st := range pkt.Items {
		p := a.players[i]
		expireGrant(p, now)

		switch {
		case st.Held == common.NoItem:
			// The slot emptying after the item was seen means it's been used.
			if p.grant != nil && p.grant.seenHeld {
				p.grant = nil
			}
		case p.grant != nil && heldMatchesGrant(st.Held, p.grant.item):
			p.grant.seenHeld = true
		case st.Held == common.ThunderCloud:
			// Thunder Clouds are passed between players by bumping, not item boxes.
		default:
			return fmt.Errorf("player %d holds %v without a grant", i+1, st.Held)
		}

		if st.Trailing != common.NoItem && st.Trailing.Object() != p.lastItem.Object() {
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
		if !slot.Action.IsOwnItem() {
			continue
		}
		if !aidMayUseObject(a, slot.Object) {
			return fmt.Errorf("event slot %d uses %d without a grant", i, slot.Object)
		}
	}
	return nil
}

func aidMayUseObject(a *Aid, obj common.ItemObject) bool {
	for _, p := range a.players {
		if p.lastItem != common.NoItem && p.lastItem.Object() == obj {
			return true
		}
	}
	return false
}

// racingPlayerCount counts players (guests included) in the race. Caller must hold mu.
func racingPlayerCount() int {
	n := 0
	for _, a := range aids {
		if a.isRacer && !a.kicked {
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
		p.hasRequested = false
	}
}
