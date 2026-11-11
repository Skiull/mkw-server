package core

import (
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
