package core

import (
	"fmt"
	"slices"
	"time"

	"mkw-server/common"
	"mkw-server/logging"
)

// A game sends its own copy of each update to every other player, through the server. A
// cheat can leave something out of the copy for one player: show the server a Banana landing
// in someone's path or a Lightning, but never tell that player's game. Their game then has
// nothing to hit, and the no-hit check would kick them. So a hit is only expected from items
// the victim's game was actually sent.

// noteRelayedEvents records which aids this packet tells about the sender's throws and
// Lightning. Caller must hold mu.
func noteRelayedEvents(a *Aid, pkt *RacePacket, now time.Time) {
	if pkt.RaceInfo != nil {
		a.playerIds = pkt.RaceInfo.PlayerIds(a.aid)
	}
	checkEventPlayers(a, pkt, now)
	if pkt.Event == nil || pkt.Destinations == 0 {
		return
	}
	for i, slot := range pkt.Event.Slots {
		switch {
		case slot.Action == common.EventUsed && slot.Object == common.ObjLightning:
			if a.lightningSentTo == nil {
				a.lightningSentTo = map[byte]time.Time{}
			}
			for d := byte(0); d < 16; d++ {
				if pkt.Destinations&(1<<d) != 0 {
					a.lightningSentTo[d] = now
				}
			}
		case slot.Action == common.EventShot:
			raw := pkt.Event.RawSlots[i]
			for k := len(groundItems) - 1; k >= 0; k-- {
				if g := groundItems[k]; g.owner == a && g.slot == i && g.raw == raw {
					g.seenBy |= pkt.Destinations
					break
				}
			}
		}
	}
}

// Packets to different players leave together; this covers lag between them.
const relayGrace = 3 * time.Second

// itemReachedVictim reports whether an item-caused expectation's victim was told about the
// item (always true for other expectations).
func itemReachedVictim(e *hitExpectation) bool {
	if e.sentBy == nil {
		return true
	}
	t, ok := e.sentBy.lightningSentTo[e.aid.aid]
	return ok && t.After(e.at.Add(-relayGrace))
}

// sentTo reports whether a's game was told about this ground item. Logged (once) when not.
func (g *groundItem) sentTo(a *Aid, now time.Time) bool {
	if g.owner == a || g.seenBy&(1<<a.aid) != 0 {
		return true
	}
	if g.seenBy&(1<<15) == 0 { // Bit 15 (no such aid): already logged.
		g.seenBy |= 1 << 15
		logging.Log("Aid %d's %v was never sent to aid %d's game; not checking it there", g.owner.aid, g.obj, a.aid)
		recordFlag(g.owner, "item not sent to every player", now)
	}
	return false
}

// SHOT and DROP data name the player whose item it is, and the receiving games believe it:
// a cheat can send a Blue Shell "thrown by" someone else. The server never takes it from
// there (items are always the packet sender's), but the games it's relayed to would.
//
// Where the index sits isn't documented. From recordings (all player id 0): byte 2 reads
// 0xC0, likely flags in the top bits and the player id below. Until a recording with a
// non-zero player id confirms that, a mismatch is only logged and flagged.
func checkEventPlayers(a *Aid, pkt *RacePacket, now time.Time) {
	if pkt.Event == nil || len(a.playerIds) == 0 || a.bot != nil {
		return
	}
	for i, slot := range pkt.Event.Slots {
		if slot.Action != common.EventShot && slot.Action != common.EventDrop {
			continue
		}
		d, ok := pkt.Event.SlotData(i)
		if !ok || len(d) < 3 {
			continue
		}
		if id := int(d[2] & 0x3f); !slices.Contains(a.playerIds, id) {
			msg := fmt.Sprintf("%v %v names player %d (byte 2 = %#02x), sender's players are %v",
				eventActionName(slot.Action), slot.Object, id, d[2], a.playerIds)
			if a.lastEventPlayerMsg != msg {
				a.lastEventPlayerMsg = msg
				logging.Log("Aid %d item event for another player? %s", a.aid, msg)
				recordFlag(a, "item event for another player: "+msg, now)
			}
		}
	}
}

func eventActionName(act common.ItemEventAction) string {
	if act == common.EventDrop {
		return "DROP"
	}
	return "SHOT"
}
