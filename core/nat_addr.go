package core

import (
	"net"

	"mkw-server/logging"
)

// A player's room packets don't always come from the address wfc-server registered: a
// router can give the room connection its own port (hairpin NAT, symmetric NAT). Such a
// player is matched by IP instead, and their real address is remembered here.

// Learned room addresses of aids, keyed by UDP address. aids stays keyed by the registered
// address, which wfc-server uses to remove and kick. Guarded by mu.
var learnedAddrs = make(map[string]*Aid)

// aidForAddr finds the aid a packet came from. Caller must hold mu.
func aidForAddr(addr net.Addr, data []byte) *Aid {
	key := addr.String()
	if a := aids[key]; a != nil {
		return a
	}
	if a := learnedAddrs[key]; a != nil {
		return a
	}
	udp, ok := addr.(*net.UDPAddr)
	if !ok {
		return nil
	}

	// Candidates: real aids on the same IP that haven't sent anything from their
	// registered address. A Race packet names its sender, which settles any tie.
	var match *Aid
	count := 0
	for _, a := range aids {
		if a.bot != nil || a.kicked || a.stats.received > 0 || !a.addr.IP.Equal(udp.IP) {
			continue
		}
		if len(data) >= 2 && data[0] == RacePacketMagic && data[1] != a.aid {
			continue
		}
		match = a
		count++
	}
	if count != 1 {
		return nil
	}

	logging.Log("Aid %d's room packets come from %v, not %v (router changed the port); using it", match.aid, udp, match.addr)
	learnedAddrs[key] = match
	match.addr = udp
	return match
}

// forgetLearnedAddrs drops an aid's learned addresses. Caller must hold mu.
func forgetLearnedAddrs(a *Aid) {
	for k, v := range learnedAddrs {
		if v == a {
			delete(learnedAddrs, k)
		}
	}
}
